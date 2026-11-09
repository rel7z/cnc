package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/user/cnc-processor/cluster"
)

func (s *Store) RegisterSettings(ctx context.Context, version string, p cluster.Program) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO processor_version_settings VALUES(?,?)`, version, string(data))
	return err
}
func (s *Store) Settings(ctx context.Context, version string) (cluster.Program, error) {
	var data string
	var p cluster.Program
	err := s.db.QueryRowContext(ctx, `SELECT settings FROM processor_version_settings WHERE version=?`, version).Scan(&data)
	if err == sql.ErrNoRows {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal([]byte(data), &p)
	return p, err
}
func (s *Store) Ancestry(ctx context.Context, run string) ([]string, error) {
	var data string
	var result []string
	err := s.db.QueryRowContext(ctx, `SELECT ancestry FROM processor_run_ancestry WHERE run_id=?`, run).Scan(&data)
	if err == sql.ErrNoRows {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(data), &result)
	return result, err
}

// AcceptPublication commits the receipt, unique lines and downstream jobs together.
// A duplicate HTTP delivery is harmless, even after the execution lease expires.
func (s *Store) AcceptPublication(ctx context.Context, attempt *Attempt, p cluster.Publication, routes []string) error {
	if !cluster.SafeRelative(p.Name) || strings.Contains(p.Name, "/") || strings.HasPrefix(p.Name, ".") || p.Generation == "" || len(p.Generation) > 128 || p.Sequence < 1 || len(p.Lines) > 1000 {
		return errors.New("invalid publication")
	}
	size := 0
	for _, line := range p.Lines {
		size += len(line)
		if !utf8.ValidString(line) || !strings.HasSuffix(line, "\n") || strings.Contains(strings.TrimSuffix(line, "\n"), "\n") || len(line) > 1<<20 {
			return errors.New("publication requires complete lines")
		}
	}
	if size > 1<<20 {
		return errors.New("publication exceeds 1 MiB")
	}
	settings, err := s.Settings(ctx, attempt.Version)
	if err != nil {
		return err
	}
	drive := true
	destination := p.Name
	var target *cluster.PinnedTarget
	body, _ := json.Marshal(p)
	sum := sha256.Sum256(body)
	fingerprint := hex.EncodeToString(sum[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old string
	err = tx.QueryRowContext(ctx, `SELECT fingerprint FROM processor_publications WHERE attempt_id=? AND name=? AND generation=? AND sequence=?`, attempt.ID, p.Name, p.Generation, p.Sequence).Scan(&old)
	if err == nil {
		if old != fingerprint {
			return errors.New("publication identity conflicts with existing content")
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	var program, ancestryJSON string
	err = tx.QueryRowContext(ctx, `SELECT r.program_id,COALESCE(a.ancestry,'[]') FROM runs r LEFT JOIN processor_run_ancestry a ON a.run_id=r.id WHERE r.id=?`, attempt.RunID).Scan(&program, &ancestryJSON)
	if err != nil {
		return err
	}
	if settings.Script != nil {
		matched := false
		for _, name := range settings.Script.Outputs {
			if p.Name == program+"__"+filepath.Base(name) {
				matched = true
			}
		}
		if !matched {
			return errors.New("undeclared publication")
		}
		for _, policy := range settings.Script.Policies {
			if p.Name == program+"__"+filepath.Base(policy.Path) {
				drive = policy.Drive
				target = policy.Target
				if policy.Drive && policy.MergedName != "" {
					if !cluster.SafeRelative(policy.MergedName) || filepath.Base(policy.MergedName) != policy.MergedName || strings.HasPrefix(policy.MergedName, ".") || !strings.HasSuffix(policy.MergedName, ".txt") {
						return errors.New("invalid merged filename")
					}
					destination = policy.MergedName
				}
			}
		}
		if target != nil {
			routes = []string{target.ProgramID}
		}
	}
	var ancestry []string
	if err = json.Unmarshal([]byte(ancestryJSON), &ancestry); err != nil {
		return err
	}
	ancestry = append(ancestry, program)
	next := []string{}
	for _, id := range routes {
		seen := false
		for _, previous := range ancestry {
			if previous == id {
				seen = true
				break
			}
		}
		if !seen {
			next = append(next, id)
		}
	}
	added := []string{}
	for _, line := range p.Lines {
		r, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO processor_output_lines(name,line,run_id,rendered) VALUES(?,?,?,?)`, destination, line, attempt.RunID, !drive)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return e
		}
		if n > 0 {
			id, e := r.LastInsertId()
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO processor_output_destinations(output_id,drive,version) VALUES(?,?,?)`, id, drive, attempt.Version); e != nil {
				return e
			}
			added = append(added, line)
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO processor_publications VALUES(?,?,?,?,?)`, attempt.ID, p.Name, p.Generation, p.Sequence, fingerprint)
	if err != nil {
		return err
	}
	if len(added) > 0 && len(next) > 0 {
		id := fmt.Sprintf("publication-%d", time.Now().UnixNano())
		records, _ := json.Marshal(added)
		_, err = tx.ExecContext(ctx, `INSERT INTO batches(acceptance_id,source_id,batch_id,filename,generation,payload_fingerprint,records_json) VALUES(?,?,?,?,?,?,?)`, id, "publication", id, destination, p.Generation, fingerprint, string(records))
		if err != nil {
			return err
		}
		ancestryData, _ := json.Marshal(ancestry)
		for i, program := range next {
			run := fmt.Sprintf("run-%d-%d", time.Now().UnixNano(), i)
			if _, err = tx.ExecContext(ctx, `INSERT INTO runs(id,batch_acceptance_id,program_id,status) VALUES(?,?,?,'queued')`, run, id, program); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO processor_run_ancestry VALUES(?,?)`, run, string(ancestryData)); err != nil {
				return err
			}
			if target != nil && program == target.ProgramID {
				_, err = tx.ExecContext(ctx, `INSERT INTO processor_run_specs(run_id,version,parallel_limit,retry_safe,export_results) VALUES(?,?,?,?,?)`, run, target.Version, target.Limit, target.RetrySafe, target.Export)
			} else {
				_, err = tx.ExecContext(ctx, `INSERT INTO processor_run_specs SELECT ?,version,parallel_limit,retry_safe,export_results FROM processor_programs WHERE program_id=?`, run, program)
			}
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

type OutputLine struct {
	ID         int64
	Name, Line string
}

func (s *Store) PendingOutput(ctx context.Context) ([]OutputLine, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,line FROM processor_output_lines WHERE rendered=0 ORDER BY id LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OutputLine{}
	size := 0
	for rows.Next() {
		var line OutputLine
		if err = rows.Scan(&line.ID, &line.Name, &line.Line); err != nil {
			return nil, err
		}
		if size+len(line.Line) > 1<<20 && len(out) > 0 {
			break
		}
		size += len(line.Line)
		out = append(out, line)
	}
	return out, rows.Err()
}
func (s *Store) MarkOutput(ctx context.Context, ids []int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `UPDATE processor_output_lines SET rendered=1 WHERE id=?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Published lines were already routed with their ancestry. Filesystem capture
// must not enqueue the same text again as a new, ancestry-free chain.
func (s *Store) FilterCaptured(ctx context.Context, name string, lines []string) ([]string, error) {
	out := []string{}
	for _, line := range lines {
		var exists bool
		err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM processor_output_lines WHERE name=? AND line=?)`, name, line).Scan(&exists)
		if err != nil {
			return nil, err
		}
		if !exists {
			out = append(out, line)
		}
	}
	return out, nil
}
func (s *Store) CancelRun(ctx context.Context, id string) error {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `UPDATE runs SET status='failed' WHERE id=? AND status IN ('queued','running')`, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return errors.New("run is not queued or running")
	}
	_, err = tx.ExecContext(ctx, `UPDATE processor_attempts SET phase='failed',finished_at=?,error='cancelled by user' WHERE run_id=? AND phase IN ('assigned','running','uploading')`, time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetRunLog(ctx context.Context, run, path string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET log_file_path=? WHERE id=?`, path, run)
	return err
}
func (s *Store) RunLog(ctx context.Context, run string) (string, error) {
	var path string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(log_file_path,'') FROM runs WHERE id=?`, run).Scan(&path)
	return path, err
}

func (s *Store) OutputNames(ctx context.Context, run string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT name FROM processor_output_lines WHERE run_id=? ORDER BY name`, run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
