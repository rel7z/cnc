package store

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var ErrFenced = errors.New("attempt is expired or superseded")
var ErrUnavailable = errors.New("paused, unavailable, or program concurrency limit reached")

type ProgramSpec struct {
	Version           string
	Limit             int
	RetrySafe, Export bool
}
type Attempt struct {
	ID         string `json:"attempt_id"`
	RunID      string `json:"run_id"`
	WorkerID   string `json:"worker_id"`
	Version    string `json:"program_version"`
	Token      string `json:"-"`
	Phase      string `json:"phase"`
	LeaseUntil int64  `json:"lease_until"`
	Completion string `json:"-"`
	Error      string `json:"error,omitempty"`
}

func (s *Store) CurrentProgram(ctx context.Context, id string) (ProgramSpec, error) {
	var p ProgramSpec
	err := s.db.QueryRowContext(ctx, `SELECT version,parallel_limit,retry_safe,export_results FROM processor_programs WHERE program_id=?`, id).Scan(&p.Version, &p.Limit, &p.RetrySafe, &p.Export)
	return p, err
}

func (s *Store) RegisterProgram(ctx context.Context, id string, p ProgramSpec) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO processor_programs VALUES(?,?,?,?,?) ON CONFLICT(program_id) DO UPDATE SET version=excluded.version,parallel_limit=excluded.parallel_limit,retry_safe=excluded.retry_safe,export_results=excluded.export_results`, id, p.Version, p.Limit, p.RetrySafe, p.Export)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO processor_program_priorities(program_id) VALUES(?)`, id)
	return err
}
func (s *Store) PinRun(ctx context.Context, id string) (ProgramSpec, error) {
	// Existing queued input acquires a version once; later program updates cannot change it.
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO processor_run_specs SELECT r.id,p.version,p.parallel_limit,p.retry_safe,p.export_results FROM runs r JOIN processor_programs p ON r.program_id=p.program_id WHERE r.id=?`, id)
	if err != nil {
		return ProgramSpec{}, err
	}
	var p ProgramSpec
	err = s.db.QueryRowContext(ctx, `SELECT version,parallel_limit,retry_safe,export_results FROM processor_run_specs WHERE run_id=?`, id).Scan(&p.Version, &p.Limit, &p.RetrySafe, &p.Export)
	return p, err
}
func (s *Store) BlockRun(ctx context.Context, id, reason string) error {
	if reason == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM processor_blocks WHERE run_id=?`, id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO processor_blocks VALUES(?,?) ON CONFLICT(run_id) DO UPDATE SET reason=excluded.reason`, id, reason)
	return err
}
func (s *Store) AllocateAttempt(ctx context.Context, id, worker string, lease time.Duration) (*Attempt, error) {
	return s.allocateAttempt(ctx, id, worker, lease, 0)
}

// AllocateBalancedAttempt changes only scheduling capacity, never a pinned
// executable version, input, retry-safety setting, or publication policy.
func (s *Store) AllocateBalancedAttempt(ctx context.Context, id, worker string, lease time.Duration, limit int) (*Attempt, error) {
	if limit < 1 {
		return nil, ErrUnavailable
	}
	return s.allocateAttempt(ctx, id, worker, lease, limit)
}

func (s *Store) allocateAttempt(ctx context.Context, id, worker string, lease time.Duration, balancedLimit int) (*Attempt, error) {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	enabled, err := s.IsEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrUnavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var status, pid string
	var limit int
	var version string
	var preferred string
	err = tx.QueryRowContext(ctx, `SELECT r.status,r.program_id,p.parallel_limit,p.version,COALESCE(w.worker_id,'') FROM runs r JOIN processor_run_specs p ON r.id=p.run_id LEFT JOIN processor_batch_workers w ON w.acceptance_id=r.batch_acceptance_id WHERE r.id=?`, id).Scan(&status, &pid, &limit, &version, &preferred)
	if err != nil {
		return nil, err
	}
	if preferred != "" {
		if preferred != worker {
			return nil, ErrUnavailable
		}
		var occupied bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM processor_attempts WHERE worker_id=? AND phase IN ('assigned','running','uploading'))`, worker).Scan(&occupied); err != nil {
			return nil, err
		}
		if occupied {
			return nil, ErrUnavailable
		}
	}
	if balancedLimit > 0 {
		limit = balancedLimit
	}
	var active int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE program_id=? AND status='running'`, pid).Scan(&active)
	if err != nil {
		return nil, err
	}
	if status != "queued" || active >= limit {
		return nil, ErrUnavailable
	}
	random := make([]byte, 32)
	if _, err = rand.Read(random); err != nil {
		return nil, err
	}
	now := time.Now()
	a := &Attempt{ID: fmt.Sprintf("attempt-%d-%s", now.UnixNano(), hex.EncodeToString(random[:4])), RunID: id, WorkerID: worker, Version: version, Token: hex.EncodeToString(random), Phase: "assigned", LeaseUntil: now.Add(lease).UnixMilli()}
	_, err = tx.ExecContext(ctx, `INSERT INTO processor_attempts(id,run_id,worker_id,version,token,phase,lease_until,created_at) VALUES(?,?,?,?,?,?,?,?)`, a.ID, id, worker, version, a.Token, a.Phase, a.LeaseUntil, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE runs SET status='running' WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM processor_blocks WHERE run_id=?`, id)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO processor_dispatch_order SELECT r.program_id,b.filename,? FROM runs r JOIN batches b ON r.batch_acceptance_id=b.acceptance_id WHERE r.id=? ON CONFLICT(program_id,filename) DO UPDATE SET last_started=excluded.last_started`, now.UnixNano(), id)
	if err != nil {
		return nil, err
	}
	return a, tx.Commit()
}
func (s *Store) Attempt(ctx context.Context, id string) (*Attempt, error) {
	var a Attempt
	var result sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,run_id,worker_id,version,token,phase,lease_until,completion_json,error FROM processor_attempts WHERE id=?`, id).Scan(&a.ID, &a.RunID, &a.WorkerID, &a.Version, &a.Token, &a.Phase, &a.LeaseUntil, &result, &a.Error)
	a.Completion = result.String
	return &a, err
}
func (s *Store) AuthenticateAttempt(ctx context.Context, id, token string) (*Attempt, error) {
	a, err := s.Attempt(ctx, id)
	if err != nil || subtle.ConstantTimeCompare([]byte(a.Token), []byte(token)) != 1 {
		return nil, ErrFenced
	}
	return a, nil
}
func (s *Store) RenewAttempt(ctx context.Context, id string, d time.Duration) error {
	result, err := s.db.ExecContext(ctx, `UPDATE processor_attempts SET lease_until=? WHERE id=? AND phase IN ('assigned','running','uploading') AND lease_until>?`, time.Now().Add(d).UnixMilli(), id, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrFenced
	}
	return nil
}
func (s *Store) AuthorizeAttempt(ctx context.Context, id string) error {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	enabled, err := s.IsEnabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrUnavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE processor_attempts SET phase='running',started_at=COALESCE(started_at,?) WHERE id=? AND phase IN ('assigned','running') AND lease_until>?`, time.Now().UnixMilli(), id, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrFenced
	}
	if err = commitPriorityTurn(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) StageCompletion(ctx context.Context, id, body string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var phase string
	var existing sql.NullString
	var until int64
	err = tx.QueryRowContext(ctx, `SELECT phase,completion_json,lease_until FROM processor_attempts WHERE id=?`, id).Scan(&phase, &existing, &until)
	if err != nil {
		return err
	}
	if phase == "completed" || phase == "uploading" {
		if existing.String == body {
			return nil
		}
		return ErrFenced
	}
	if phase != "running" || until <= time.Now().UnixMilli() {
		return ErrFenced
	}
	_, err = tx.ExecContext(ctx, `UPDATE processor_attempts SET phase='uploading',completion_json=? WHERE id=?`, body, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) FinishAttempt(ctx context.Context, id, status, logPath, outputPath, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var run, phase string
	var until int64
	err = tx.QueryRowContext(ctx, `SELECT run_id,phase,lease_until FROM processor_attempts WHERE id=?`, id).Scan(&run, &phase, &until)
	if err != nil {
		return err
	}
	if phase == status && (status == "completed" || status == "failed" || status == "interrupted") {
		return nil
	}
	if phase != "assigned" && phase != "running" && phase != "uploading" {
		return ErrFenced
	}
	// Staged results remain recoverable after worker departure; they are not rerun.
	if phase != "uploading" && until <= time.Now().UnixMilli() {
		return ErrFenced
	}
	_, err = tx.ExecContext(ctx, `UPDATE processor_attempts SET phase=?,finished_at=?,error=? WHERE id=?`, status, time.Now().UnixMilli(), reason, id)
	if err != nil {
		return err
	}
	runStatus := status
	if status == "interrupted" {
		var retry bool
		var count int
		err = tx.QueryRowContext(ctx, `SELECT p.retry_safe,(SELECT COUNT(*) FROM processor_attempts a WHERE a.run_id=p.run_id AND a.phase!='rejected')-COALESCE((SELECT baseline FROM processor_retry_windows WHERE run_id=p.run_id),0) FROM processor_run_specs p WHERE p.run_id=?`, run).Scan(&retry, &count)
		if err != nil {
			return err
		}
		if retry && count < 3 {
			runStatus = "queued"
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE runs SET status=?,log_file_path=?,output_file_path=? WHERE id=?`, runStatus, logPath, outputPath, run)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ActiveAttempts(ctx context.Context) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,run_id,worker_id,version,phase,lease_until,COALESCE(completion_json,''),error FROM processor_attempts WHERE phase IN ('assigned','running','uploading') ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attempt{}
	for rows.Next() {
		var a Attempt
		if err = rows.Scan(&a.ID, &a.RunID, &a.WorkerID, &a.Version, &a.Phase, &a.LeaseUntil, &a.Completion, &a.Error); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) ExpireAttempts(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT a.id,a.run_id,p.retry_safe,(SELECT COUNT(*) FROM processor_attempts c WHERE c.run_id=a.run_id AND c.phase!='rejected')-COALESCE((SELECT baseline FROM processor_retry_windows WHERE run_id=a.run_id),0) FROM processor_attempts a JOIN processor_run_specs p ON a.run_id=p.run_id WHERE a.phase IN ('assigned','running') AND a.lease_until<=?`, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	type item struct {
		id, run string
		retry   bool
		count   int
	}
	items := []item{}
	for rows.Next() {
		var i item
		if err = rows.Scan(&i.id, &i.run, &i.retry, &i.count); err != nil {
			rows.Close()
			return err
		}
		items = append(items, i)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, i := range items {
		status := "interrupted"
		if i.retry && i.count < 3 {
			status = "queued"
		}
		if _, err = tx.ExecContext(ctx, `UPDATE processor_attempts SET phase='interrupted',finished_at=?,error='worker lease expired' WHERE id=?`, time.Now().UnixMilli(), i.id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE runs SET status=? WHERE id=?`, status, i.run); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) RejectAttempt(ctx context.Context, id, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var run string
	err = tx.QueryRowContext(ctx, `SELECT run_id FROM processor_attempts WHERE id=? AND phase='assigned'`, id).Scan(&run)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE processor_attempts SET phase='rejected',error=?,finished_at=? WHERE id=?`, reason, time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE runs SET status='queued' WHERE id=?`, run)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) RetryRun(ctx context.Context, id string) error {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE runs SET status='queued' WHERE id=? AND status IN ('failed','interrupted')`, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("run is not failed or interrupted")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO processor_retry_windows SELECT ?,COUNT(*) FROM processor_attempts WHERE run_id=? AND phase!='rejected' ON CONFLICT(run_id) DO UPDATE SET baseline=excluded.baseline`, id, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Run(ctx context.Context, id string) (*Run, error) {
	var r Run
	err := s.db.QueryRowContext(ctx, `SELECT id,batch_acceptance_id,program_id,status FROM runs WHERE id=?`, id).Scan(&r.ID, &r.BatchAcceptanceID, &r.ProgramID, &r.Status)
	return &r, err
}
