package store

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db         *sql.DB
	scheduleMu sync.Mutex
}

type Batch struct {
	AcceptanceID       string
	SourceID           string
	BatchID            string
	Filename           string
	Generation         string
	PayloadFingerprint string
	RecordsJSON        string
	PreferredWorker    string
}

type Run struct {
	ID                string
	BatchAcceptanceID string
	ProgramID         string
	Status            string
	LogFilePath       string
	OutputFilePath    string
	WorkerID          string
	AttemptID         string
	ProgramVersion    string
	Phase             string
	BlockedReason     string
	Error             string
	PublishedLines    int
	PendingLines      int
}

func NewStore(dbPath string) (*Store, error) {
	// Enable WAL mode and 5 second busy timeout for high concurrency
	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) IsEnabled(ctx context.Context) (bool, error) {
	row := s.db.QueryRowContext(ctx, "SELECT enabled FROM system_state WHERE id = 1")
	var enabled bool
	if err := row.Scan(&enabled); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return enabled, nil
}

func (s *Store) SetEnabled(ctx context.Context, enabled bool) error {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	_, err := s.db.ExecContext(ctx, "INSERT INTO system_state (id, enabled) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET enabled = excluded.enabled", enabled)
	return err
}

func (s *Store) AcceptBatch(ctx context.Context, b Batch, programIDs []string) (string, []string, error) {
	return s.acceptBatch(ctx, b, programIDs, nil)
}

func (s *Store) AcceptCaptured(ctx context.Context, b Batch, programs []string, cursor CaptureCursor) error {
	_, _, err := s.acceptBatch(ctx, b, programs, &cursor)
	return err
}

func (s *Store) acceptBatch(ctx context.Context, b Batch, programIDs []string, cursor *CaptureCursor) (string, []string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()
	accID, runs, err := s.acceptBatchTx(ctx, tx, b, programIDs)
	if err != nil {
		return "", nil, err
	}
	if err = saveCaptureCursor(ctx, tx, cursor); err != nil {
		return "", nil, err
	}
	if err = tx.Commit(); err != nil {
		return "", nil, err
	}
	return accID, runs, nil
}

func (s *Store) acceptBatchTx(ctx context.Context, tx *sql.Tx, b Batch, programIDs []string) (string, []string, error) {

	// Check for deduplication
	var existingAccID, existingFingerprint string
	err := tx.QueryRowContext(ctx, "SELECT acceptance_id, payload_fingerprint FROM batches WHERE source_id = ? AND batch_id = ?", b.SourceID, b.BatchID).Scan(&existingAccID, &existingFingerprint)
	if err == nil {
		if existingFingerprint != b.PayloadFingerprint {
			return "", nil, fmt.Errorf("conflict: same batch_id but different payload")
		}
		var preferred string
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT worker_id FROM processor_batch_workers WHERE acceptance_id=?),'')`, existingAccID).Scan(&preferred); err != nil {
			return "", nil, err
		}
		if preferred != b.PreferredWorker {
			return "", nil, fmt.Errorf("conflict: same batch_id but different assigned worker")
		}
		// Fetch existing runs
		rows, err := tx.QueryContext(ctx, "SELECT id FROM runs WHERE batch_acceptance_id = ?", existingAccID)
		if err != nil {
			return "", nil, err
		}
		defer rows.Close()
		var runIDs []string
		for rows.Next() {
			var rid string
			if err := rows.Scan(&rid); err == nil {
				runIDs = append(runIDs, rid)
			}
		}
		return existingAccID, runIDs, nil
	} else if err != sql.ErrNoRows {
		return "", nil, err
	}

	accID := fmt.Sprintf("acc-%d", time.Now().UnixNano())
	_, err = tx.ExecContext(ctx, "INSERT INTO batches (acceptance_id, source_id, batch_id, filename, generation, payload_fingerprint, records_json) VALUES (?, ?, ?, ?, ?, ?, ?)",
		accID, b.SourceID, b.BatchID, b.Filename, b.Generation, b.PayloadFingerprint, b.RecordsJSON)
	if err != nil {
		return "", nil, err
	}
	if b.PreferredWorker != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO processor_batch_workers VALUES(?,?)`, accID, b.PreferredWorker); err != nil {
			return "", nil, err
		}
	}

	var runIDs []string
	for i, pid := range programIDs {
		runID := fmt.Sprintf("run-%d-%d", time.Now().UnixNano(), i)
		_, err = tx.ExecContext(ctx, "INSERT INTO runs (id, batch_acceptance_id, program_id, status) VALUES (?, ?, ?, 'queued')", runID, accID, pid)
		if err != nil {
			return "", nil, err
		}
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO processor_run_specs SELECT ?,version,parallel_limit,retry_safe,export_results FROM processor_programs WHERE program_id=?`, runID, pid)
		if err != nil {
			return "", nil, err
		}
		runIDs = append(runIDs, runID)
	}

	return accID, runIDs, nil
}

func (s *Store) ListRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.batch_acceptance_id,r.program_id,r.status,COALESCE(r.log_file_path,''),COALESCE(r.output_file_path,''),COALESCE(a.worker_id,''),COALESCE(a.id,''),COALESCE(p.version,''),COALESCE(a.phase,''),COALESCE(b.reason,''),COALESCE(a.error,''),(SELECT COUNT(*) FROM processor_output_lines o WHERE o.run_id=r.id),(SELECT COUNT(*) FROM processor_output_lines o WHERE o.run_id=r.id AND o.rendered=0) FROM runs r LEFT JOIN processor_run_specs p ON p.run_id=r.id LEFT JOIN processor_blocks b ON b.run_id=r.id LEFT JOIN processor_attempts a ON a.id=(SELECT id FROM processor_attempts WHERE run_id=r.id ORDER BY created_at DESC,id DESC LIMIT 1) ORDER BY r.created_at DESC,r.id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.BatchAcceptanceID, &r.ProgramID, &r.Status, &r.LogFilePath, &r.OutputFilePath, &r.WorkerID, &r.AttemptID, &r.ProgramVersion, &r.Phase, &r.BlockedReason, &r.Error, &r.PublishedLines, &r.PendingLines); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

func (s *Store) GetCursor(ctx context.Context, filename string) (int64, error) {
	row := s.db.QueryRowContext(ctx, "SELECT last_offset FROM cursors WHERE filename = ?", filename)
	var offset int64
	if err := row.Scan(&offset); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return offset, nil
}

func (s *Store) SetCursor(ctx context.Context, filename string, offset int64) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO cursors (filename, last_offset, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP) ON CONFLICT(filename) DO UPDATE SET last_offset = excluded.last_offset, updated_at = CURRENT_TIMESTAMP", filename, offset)
	return err
}

func (s *Store) ClaimNextRun(ctx context.Context) (*Run, error) {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	enabled, err := s.IsEnabled(ctx)
	if err != nil || !enabled {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, "SELECT id, batch_acceptance_id, program_id, status FROM runs WHERE status = 'queued' AND NOT EXISTS (SELECT 1 FROM runs active WHERE active.program_id=runs.program_id AND active.status='running') ORDER BY created_at, id LIMIT 1")
	var r Run
	if err := row.Scan(&r.ID, &r.BatchAcceptanceID, &r.ProgramID, &r.Status); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No runs queued
		}
		return nil, err
	}

	_, err = tx.ExecContext(ctx, "UPDATE runs SET status = 'running' WHERE id = ?", r.ID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	r.Status = "running"
	return &r, nil
}

func (s *Store) UpdateRunStatus(ctx context.Context, id, status, logFile, outputFile string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE runs SET status = ?, log_file_path = ?, output_file_path = ? WHERE id = ?", status, logFile, outputFile, id)
	return err
}

func (s *Store) GetBatchRecords(ctx context.Context, acceptanceID string) (string, error) {
	row := s.db.QueryRowContext(ctx, "SELECT records_json FROM batches WHERE acceptance_id = ?", acceptanceID)
	var records string
	if err := row.Scan(&records); err != nil {
		return "", err
	}
	return records, nil
}

func (s *Store) RunCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT status, COUNT(*) FROM runs GROUP BY status")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{"queued": 0, "running": 0, "completed": 0, "failed": 0, "interrupted": 0}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

// RecoverInterrupted is called once at startup, before scheduling any jobs.
func (s *Store) RecoverInterrupted(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "UPDATE runs SET status='interrupted' WHERE status='running' AND NOT EXISTS (SELECT 1 FROM processor_attempts a WHERE a.run_id=runs.id AND a.phase IN ('assigned','running','uploading'))")
	return err
}

type CaptureCursor struct {
	Filename, Identity, Generation, PrefixHash string
	Offset                                     int64
}

func (s *Store) CaptureCursor(ctx context.Context, filename string) (CaptureCursor, error) {
	c := CaptureCursor{Filename: filename}
	err := s.db.QueryRowContext(ctx, "SELECT identity,generation,offset,prefix_hash FROM capture_cursors WHERE filename=?", filename).Scan(&c.Identity, &c.Generation, &c.Offset, &c.PrefixHash)
	if err == sql.ErrNoRows {
		return c, nil
	}
	return c, err
}

// Serialize the actual process start with pause acknowledgment. A claimed run
// waits here during pause rather than starting after the API confirms pause.
func (s *Store) StartCommand(ctx context.Context, start func() error) error {
	for {
		s.scheduleMu.Lock()
		enabled, err := s.IsEnabled(ctx)
		if err == nil && enabled {
			err = start()
			s.scheduleMu.Unlock()
			return err
		}
		s.scheduleMu.Unlock()
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
