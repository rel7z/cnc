package store

import (
	"context"
	"database/sql"
)

func saveCaptureCursor(ctx context.Context, tx *sql.Tx, cursor *CaptureCursor) error {
	if cursor == nil {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO capture_cursors(filename,identity,generation,offset,prefix_hash) VALUES(?,?,?,?,?) ON CONFLICT(filename) DO UPDATE SET identity=excluded.identity,generation=excluded.generation,offset=excluded.offset,prefix_hash=excluded.prefix_hash`, cursor.Filename, cursor.Identity, cursor.Generation, cursor.Offset, cursor.PrefixHash)
	return err
}

// Commit every worker share and the cursor together. A partial commit must
// never advance the input past a share that was not durably queued.
func (s *Store) AcceptSpreadCaptured(ctx context.Context, batches []Batch, programs []string, cursor CaptureCursor) error {
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
	for _, b := range batches {
		if _, _, err = s.acceptBatchTx(ctx, tx, b, programs); err != nil {
			return err
		}
	}
	if err = saveCaptureCursor(ctx, tx, &cursor); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SpreadRecordCount(ctx context.Context, name, generation string) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(json_array_length(records_json)),0) FROM batches WHERE source_id='capture-spread' AND filename=? AND generation=?`, name, generation).Scan(&count)
	return count, err
}

func (s *Store) AssignmentInfo(ctx context.Context, run string) (name, worker string, records int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT b.filename,COALESCE(w.worker_id,''),json_array_length(b.records_json) FROM runs r JOIN batches b ON b.acceptance_id=r.batch_acceptance_id LEFT JOIN processor_batch_workers w ON w.acceptance_id=b.acceptance_id WHERE r.id=?`, run).Scan(&name, &worker, &records)
	return
}
