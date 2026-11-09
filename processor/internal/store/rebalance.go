package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type RebalanceResult struct {
	ReplacedRuns int `json:"replaced_runs"`
	NewRuns      int `json:"new_runs"`
	Records      int `json:"records"`
}

// Repartition only unstarted, unassigned work while dispatch is paused. Active
// attempts, results, captured cursors, version pins and ancestry are preserved.
func (s *Store) RebalanceQueuedFile(ctx context.Context, filename string, workers []string) (RebalanceResult, error) {
	var result RebalanceResult
	workers = append([]string(nil), workers...)
	sort.Strings(workers)
	if len(workers) == 0 {
		return result, fmt.Errorf("no workers for distribution")
	}
	for i, w := range workers {
		if w == "" || (i > 0 && w == workers[i-1]) {
			return result, fmt.Errorf("invalid worker list")
		}
	}
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	enabled, err := s.IsEnabled(ctx)
	if err != nil {
		return result, err
	}
	if enabled {
		return result, fmt.Errorf("pause dispatch before repartitioning queued input")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	type key struct {
		Program, Version, Generation, Ancestry string
		Limit                                  int
		Retry, Export                          bool
	}
	type group struct {
		key     key
		ids     []string
		records []string
	}
	groups := map[key]*group{}
	var order []key
	rows, err := tx.QueryContext(ctx, `SELECT r.id,r.program_id,p.version,p.parallel_limit,p.retry_safe,p.export_results,b.generation,b.records_json,COALESCE(a.ancestry,'[]') FROM runs r JOIN batches b ON b.acceptance_id=r.batch_acceptance_id JOIN processor_run_specs p ON p.run_id=r.id LEFT JOIN processor_batch_workers w ON w.acceptance_id=b.acceptance_id LEFT JOIN processor_run_ancestry a ON a.run_id=r.id WHERE b.filename=? AND r.status='queued' AND w.worker_id IS NULL AND NOT EXISTS(SELECT 1 FROM processor_attempts x WHERE x.run_id=r.id AND x.phase!='rejected') ORDER BY r.created_at,r.id`, filename)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var id, raw string
		var k key
		if err = rows.Scan(&id, &k.Program, &k.Version, &k.Limit, &k.Retry, &k.Export, &k.Generation, &raw, &k.Ancestry); err != nil {
			rows.Close()
			return result, err
		}
		var records []string
		if err = json.Unmarshal([]byte(raw), &records); err != nil {
			rows.Close()
			return result, err
		}
		g := groups[k]
		if g == nil {
			g = &group{key: k}
			groups[k] = g
			order = append(order, k)
		}
		g.ids = append(g.ids, id)
		g.records = append(g.records, records...)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	nonce := time.Now().UnixNano()
	for gi, k := range order {
		g := groups[k]
		if len(g.records) == 0 {
			continue
		}
		var baseline int64
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(json_array_length(records_json)),0) FROM batches WHERE source_id='capture-spread' AND filename=? AND generation=?`, filename, k.Generation).Scan(&baseline); err != nil {
			return result, err
		}
		chunks := make([][][]string, len(workers))
		sizes := make([]int, len(workers))
		for i, line := range g.records {
			if len(line) > 1<<20 {
				return result, fmt.Errorf("input record exceeds 1 MiB")
			}
			j := (int(baseline%int64(len(workers))) + i) % len(workers)
			if len(chunks[j]) == 0 || sizes[j]+len(line) > 8<<20 {
				chunks[j] = append(chunks[j], nil)
				sizes[j] = 0
			}
			last := len(chunks[j]) - 1
			chunks[j][last] = append(chunks[j][last], line)
			sizes[j] += len(line)
		}
		for wi, parts := range chunks {
			for ci, records := range parts {
				data, err := json.Marshal(records)
				if err != nil {
					return result, err
				}
				sum := sha256.Sum256(data)
				b := Batch{SourceID: "capture-spread", BatchID: fmt.Sprintf("repartition:%d:%d:%s:%d", nonce, gi, workers[wi], ci), Filename: filename, Generation: k.Generation, PayloadFingerprint: hex.EncodeToString(sum[:]), RecordsJSON: string(data), PreferredWorker: workers[wi]}
				_, runs, err := s.acceptBatchTx(ctx, tx, b, []string{k.Program})
				if err != nil {
					return result, err
				}
				for _, id := range runs {
					if _, err = tx.ExecContext(ctx, `UPDATE processor_run_specs SET version=?,parallel_limit=?,retry_safe=?,export_results=? WHERE run_id=?`, k.Version, k.Limit, k.Retry, k.Export, id); err != nil {
						return result, err
					}
					if _, err = tx.ExecContext(ctx, `INSERT INTO processor_run_ancestry VALUES(?,?)`, id, k.Ancestry); err != nil {
						return result, err
					}
					result.NewRuns++
				}
				result.Records += len(records)
			}
		}
		for _, id := range g.ids {
			if _, err = tx.ExecContext(ctx, `UPDATE runs SET status='interrupted' WHERE id=? AND status='queued'`, id); err != nil {
				return result, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO processor_blocks VALUES(?,?) ON CONFLICT(run_id) DO UPDATE SET reason=excluded.reason`, id, "Repartitioned into even worker shares; original input retained"); err != nil {
				return result, err
			}
			result.ReplacedRuns++
		}
	}
	return result, tx.Commit()
}
