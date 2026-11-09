package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

var ErrPriorityConflict = errors.New("tool rank changed; refresh priorities before saving again")
var ErrPriorityNotFound = errors.New("registered tool not found")

type ProgramPriority struct {
	ProgramID      string `json:"program_id"`
	ConfiguredRank int    `json:"configured_rank"`
	OverrideRank   *int   `json:"override_rank"`
	EffectiveRank  int    `json:"effective_rank"`
	Revision       int64  `json:"revision"`
}

const priorityColumns = `program_id,configured_rank,override_rank,COALESCE(override_rank,configured_rank),revision`

func scanPriority(row interface{ Scan(...any) error }) (p ProgramPriority, err error) {
	err = row.Scan(&p.ProgramID, &p.ConfiguredRank, &p.OverrideRank, &p.EffectiveRank, &p.Revision)
	return
}
func (s *Store) ProgramPriorities(ctx context.Context) ([]ProgramPriority, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+priorityColumns+` FROM processor_program_priorities ORDER BY COALESCE(override_rank,configured_rank) DESC,program_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProgramPriority{}
	for rows.Next() {
		p, e := scanPriority(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) SetConfiguredRanks(ctx context.Context, ranks map[string]int) error {
	for _, rank := range ranks {
		if rank < 1 || rank > 5 {
			return errors.New("rank must be between 1 and 5")
		}
	}
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	changed := false
	for id, rank := range ranks {
		result, e := tx.ExecContext(ctx, `INSERT INTO processor_program_priorities(program_id,configured_rank) VALUES(?,?) ON CONFLICT(program_id) DO UPDATE SET configured_rank=excluded.configured_rank,revision=revision+1 WHERE configured_rank!=excluded.configured_rank`, id, rank)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		changed = changed || n > 0
	}
	if changed {
		if _, err = tx.ExecContext(ctx, `UPDATE processor_priority_turn SET program_id='',remaining=0 WHERE id=1`); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) UpdateProgramPriority(ctx context.Context, id string, rank *int, revision int64) (ProgramPriority, error) {
	if revision < 1 || (rank != nil && (*rank < 1 || *rank > 5)) {
		return ProgramPriority{}, errors.New("rank must be 1–5 and revision must be positive")
	}
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProgramPriority{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE processor_program_priorities SET override_rank=?,revision=revision+1 WHERE program_id=? AND revision=?`, rank, id, revision)
	if err != nil {
		return ProgramPriority{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return ProgramPriority{}, err
	}
	if n == 0 {
		var exists bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM processor_program_priorities WHERE program_id=?)`, id).Scan(&exists)
		if err != nil {
			return ProgramPriority{}, err
		}
		if !exists {
			return ProgramPriority{}, ErrPriorityNotFound
		}
		return ProgramPriority{}, ErrPriorityConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE processor_priority_turn SET program_id='',remaining=0 WHERE id=1`); err != nil {
		return ProgramPriority{}, err
	}
	p, err := scanPriority(tx.QueryRowContext(ctx, `SELECT `+priorityColumns+` FROM processor_program_priorities WHERE program_id=?`, id))
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}

// Candidate limits apply per tool. First candidates rotate among source files;
// the scheduler can skip unavailable tools without hiding another tool's work.
func (s *Store) QueuedRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `WITH sources AS (
 SELECT r.*,ROW_NUMBER() OVER(PARTITION BY r.program_id,b.filename ORDER BY r.created_at,r.id) AS source_position,COALESCE(d.last_started,0) AS source_order
 FROM runs r JOIN batches b ON b.acceptance_id=r.batch_acceptance_id LEFT JOIN processor_dispatch_order d ON d.program_id=r.program_id AND d.filename=b.filename WHERE r.status='queued'
 ), candidates AS (SELECT *,ROW_NUMBER() OVER(PARTITION BY program_id ORDER BY source_position,source_order,created_at,id) AS candidate_position FROM sources)
 SELECT id,batch_acceptance_id,program_id,status,COALESCE((SELECT COALESCE(override_rank,configured_rank) FROM processor_program_priorities p WHERE p.program_id=candidates.program_id),1)
 FROM candidates WHERE candidate_position<=100 ORDER BY candidate_position,source_order,created_at,id`)
	if err != nil {
		return nil, err
	}
	groups := map[string][]Run{}
	ranks := map[string]int{}
	for rows.Next() {
		var r Run
		var rank int
		if err = rows.Scan(&r.ID, &r.BatchAcceptanceID, &r.ProgramID, &r.Status, &rank); err != nil {
			rows.Close()
			return nil, err
		}
		groups[r.ProgramID] = append(groups[r.ProgramID], r)
		ranks[r.ProgramID] = rank
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if ranks[ids[i]] != ranks[ids[j]] {
			return ranks[ids[i]] > ranks[ids[j]]
		}
		return ids[i] < ids[j]
	})
	var cursor string
	var remaining int
	if err = s.db.QueryRowContext(ctx, `SELECT program_id,remaining FROM processor_priority_turn WHERE id=1`).Scan(&cursor, &remaining); err != nil {
		return nil, err
	}
	// Rotate by the canonical rank/ID order even if the saved next tool currently
	// has no queued jobs. This prevents an empty cursor resetting every round.
	priorities, err := s.ProgramPriorities(ctx)
	if err != nil {
		return nil, err
	}
	all := make([]string, 0, len(priorities))
	for _, p := range priorities {
		all = append(all, p.ProgramID)
	}
	start := -1
	for i, id := range all {
		if id == cursor {
			start = i
			break
		}
	}
	if start >= 0 {
		order := map[string]int{}
		for n := 0; n < len(all); n++ {
			order[all[(start+n)%len(all)]] = n
		}
		sort.SliceStable(ids, func(i, j int) bool {
			a, okA := order[ids[i]]
			b, okB := order[ids[j]]
			if !okA {
				return false
			}
			if !okB {
				return true
			}
			return a < b
		})
	}
	out := []Run{}
	// Interleave fallback candidates so a blocked first tool cannot obscure the
	// next tool even with a large input. Candidate zero is each source-fair head.
	for n := 0; ; n++ {
		found := false
		for _, id := range ids {
			if n < len(groups[id]) {
				out = append(out, groups[id][n])
				found = true
			}
		}
		if !found {
			break
		}
	}
	return out, nil
}
func (s *Store) CommitPriorityTurn(ctx context.Context, attempt string) error {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = commitPriorityTurn(ctx, tx, attempt); err != nil {
		return err
	}
	return tx.Commit()
}
func commitPriorityTurn(ctx context.Context, tx *sql.Tx, attempt string) error {
	var done bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM processor_priority_commits WHERE attempt_id=?)`, attempt).Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	var id, phase string
	if err := tx.QueryRowContext(ctx, `SELECT r.program_id,a.phase FROM processor_attempts a JOIN runs r ON r.id=a.run_id WHERE a.id=?`, attempt).Scan(&id, &phase); err != nil {
		return err
	}
	if phase == "rejected" {
		return ErrFenced
	}
	rows, err := tx.QueryContext(ctx, `SELECT program_id,COALESCE(override_rank,configured_rank) FROM processor_program_priorities ORDER BY COALESCE(override_rank,configured_rank) DESC,program_id`)
	if err != nil {
		return err
	}
	ids := []string{}
	rank := 1
	for rows.Next() {
		var pid string
		var weight int
		if err = rows.Scan(&pid, &weight); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, pid)
		if pid == id {
			rank = weight
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var cursor string
	var remaining int
	if err = tx.QueryRowContext(ctx, `SELECT program_id,remaining FROM processor_priority_turn WHERE id=1`).Scan(&cursor, &remaining); err != nil {
		return err
	}
	if cursor != id || remaining <= 0 {
		remaining = rank
	}
	remaining--
	cursor = id
	if remaining == 0 {
		for i, pid := range ids {
			if pid == id {
				cursor = ids[(i+1)%len(ids)]
				break
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE processor_priority_turn SET program_id=?,remaining=? WHERE id=1`, cursor, remaining); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO processor_priority_commits(attempt_id) VALUES(?)`, attempt)
	return err
}
