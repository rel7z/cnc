package store

import (
	"context"
	"sort"
)

func (s *Store) PendingShareWorkers(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT w.worker_id FROM processor_batch_workers w JOIN runs r ON r.batch_acceptance_id=w.acceptance_id WHERE r.status='queued' ORDER BY w.worker_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ReassignWaitingShares preserves the original input and version pins. An active
// lease or an uncertain non-retry-safe execution prevents reassignment.
func (s *Store) ReassignWaitingShares(ctx context.Context, unavailable string, workers []string) (int, error) {
	if len(workers) == 0 {
		return 0, nil
	}
	workers = append([]string(nil), workers...)
	sort.Strings(workers)
	for i, id := range workers {
		if id == "" || id == unavailable || i > 0 && id == workers[i-1] {
			return 0, ErrUnavailable
		}
	}
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT w.acceptance_id FROM processor_batch_workers w
 WHERE w.worker_id=? AND EXISTS(SELECT 1 FROM runs r WHERE r.batch_acceptance_id=w.acceptance_id AND r.status='queued')
 AND NOT EXISTS(SELECT 1 FROM runs r JOIN processor_attempts a ON a.run_id=r.id WHERE r.batch_acceptance_id=w.acceptance_id AND a.phase IN ('assigned','running','uploading'))
 AND NOT EXISTS(SELECT 1 FROM runs r JOIN processor_run_specs p ON p.run_id=r.id WHERE r.batch_acceptance_id=w.acceptance_id AND r.status='queued' AND p.retry_safe=0 AND EXISTS(SELECT 1 FROM processor_attempts a WHERE a.run_id=r.id AND a.phase!='rejected')) ORDER BY w.acceptance_id`, unavailable)
	if err != nil {
		return 0, err
	}
	var batches []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		batches = append(batches, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	loads := map[string]int{}
	for _, id := range workers {
		var load int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM processor_batch_workers w WHERE w.worker_id=? AND EXISTS(SELECT 1 FROM runs r WHERE r.batch_acceptance_id=w.acceptance_id AND r.status IN ('queued','running'))`, id).Scan(&load); err != nil {
			return 0, err
		}
		loads[id] = load
	}
	for _, batch := range batches {
		chosen := workers[0]
		for _, id := range workers {
			if loads[id] < loads[chosen] {
				chosen = id
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE processor_batch_workers SET worker_id=? WHERE acceptance_id=?`, chosen, batch); err != nil {
			return 0, err
		}
		loads[chosen]++
	}
	return len(batches), tx.Commit()
}
