package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MigrateQueue imports pending legacy work once. Active attempts must finish
// before rollout. The deployment keeps a database backup for rollback.
func (s *Store) MigrateQueue(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var done bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM processor_migrations WHERE name='queue-v2')`).Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE status='running'`).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return fmt.Errorf("queue migration requires active jobs to finish")
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.id,r.program_id,b.filename,b.generation,b.records_json,COALESCE(a.ancestry,'[]') FROM runs r JOIN batches b ON r.batch_acceptance_id=b.acceptance_id LEFT JOIN processor_run_ancestry a ON a.run_id=r.id WHERE r.status='queued' ORDER BY r.created_at,r.id`)
	if err != nil {
		return err
	}
	type pending struct{ id, program, name, generation, records, ancestry string }
	items := []pending{}
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.id, &p.program, &p.name, &p.generation, &p.records, &p.ancestry); err != nil {
			rows.Close()
			return err
		}
		items = append(items, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range items {
		var lines []string
		if err = json.Unmarshal([]byte(p.records), &lines); err != nil {
			return err
		}
		chunks := [][]string{}
		chunk := []string{}
		size := 0
		for _, line := range lines {
			if !strings.HasSuffix(line, "\n") {
				line += "\n"
			}
			if len(line) > 1<<20 {
				return fmt.Errorf("legacy run %s contains a line exceeding 1 MiB", p.id)
			}
			if len(chunk) >= 1000 || size+len(line) > 1<<20 {
				chunks = append(chunks, chunk)
				chunk = nil
				size = 0
			}
			chunk = append(chunk, line)
			size += len(line)
		}
		if len(chunk) > 0 {
			chunks = append(chunks, chunk)
		}
		if len(chunks) == 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE runs SET status='completed' WHERE id=?`, p.id); err != nil {
				return err
			}
			continue
		}
		for i, chunk := range chunks {
			batch := fmt.Sprintf("migration-%s-%d", p.id, i)
			data, _ := json.Marshal(chunk)
			if _, err = tx.ExecContext(ctx, `INSERT INTO batches(acceptance_id,source_id,batch_id,filename,generation,payload_fingerprint,records_json) VALUES(?,'queue-migration',?,?,?,?,?)`, batch, batch, p.name, p.generation, batch, string(data)); err != nil {
				return err
			}
			run := p.id
			if i == 0 {
				if _, err = tx.ExecContext(ctx, `UPDATE runs SET batch_acceptance_id=? WHERE id=?`, batch, run); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `DELETE FROM processor_run_specs WHERE run_id=?`, run); err != nil {
					return err
				}
			} else {
				run = fmt.Sprintf("%s-chunk-%d", p.id, i)
				if _, err = tx.ExecContext(ctx, `INSERT INTO runs(id,batch_acceptance_id,program_id,status) VALUES(?,?,?,'queued')`, run, batch, p.program); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO processor_run_ancestry VALUES(?,?)`, run, p.ancestry); err != nil {
					return err
				}
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO processor_run_specs SELECT ?,version,parallel_limit,retry_safe,export_results FROM processor_programs WHERE program_id=?`, run, p.program); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO processor_migrations VALUES('queue-v2',?)`, time.Now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}
