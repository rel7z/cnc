package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ProcessorTemplate struct {
	IsDefault  bool            `json:"is_default"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Definition json.RawMessage `json:"definition"`
	Revision   int64           `json:"revision"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
}

func scanProcessorTemplate(row interface{ Scan(...any) error }) (ProcessorTemplate, error) {
	var t ProcessorTemplate
	var raw string
	err := row.Scan(&t.ID, &t.Name, &raw, &t.Revision, &t.CreatedAt, &t.UpdatedAt)
	t.Definition = json.RawMessage(raw)
	t.IsDefault = t.ID == "builtin-processor-bf-20261021" || t.ID == "builtin-processor-divi-20261021"
	if err == sql.ErrNoRows {
		err = ErrTemplateNotFound
	}
	return t, err
}
func (s *Store) ProcessorTemplates(ctx context.Context) ([]ProcessorTemplate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,definition,revision,created_at,updated_at FROM processor_templates ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProcessorTemplate{}
	for rows.Next() {
		t, e := scanProcessorTemplate(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) SaveProcessorTemplate(ctx context.Context, id, name string, definition json.RawMessage, revision int64) (ProcessorTemplate, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 120 {
		return ProcessorTemplate{}, errors.New("enter a template name of up to 120 characters")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if id == "" {
		id = uuid.NewString()
		_, err := s.db.ExecContext(ctx, `INSERT INTO processor_templates VALUES(?,?,?,?,?,?)`, id, name, string(definition), 1, now, now)
		if err != nil {
			return ProcessorTemplate{}, templateDBError(err)
		}
	} else {
		result, err := s.db.ExecContext(ctx, `UPDATE processor_templates SET name=?,definition=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`, name, string(definition), now, id, revision)
		if err != nil {
			return ProcessorTemplate{}, templateDBError(err)
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return ProcessorTemplate{}, ErrTemplateConflict
		}
	}
	return scanProcessorTemplate(s.db.QueryRowContext(ctx, `SELECT id,name,definition,revision,created_at,updated_at FROM processor_templates WHERE id=?`, id))
}
func (s *Store) DeleteProcessorTemplate(ctx context.Context, id string, revision int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM processor_templates WHERE id=? AND revision=?`, id, revision)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrTemplateConflict
	}
	return nil
}

type ProcessorResult struct {
	Name  string `json:"name"`
	Lines int64  `json:"lines"`
	Drive bool   `json:"drive"`
}

func (s *Store) ProcessorResults(ctx context.Context, run string) ([]ProcessorResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.name,COUNT(*),MAX(COALESCE(d.drive,1)) FROM processor_output_lines o LEFT JOIN processor_output_destinations d ON d.output_id=o.id WHERE (?='' OR o.run_id=?) GROUP BY o.name ORDER BY o.name`, run, run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProcessorResult{}
	for rows.Next() {
		var r ProcessorResult
		if err := rows.Scan(&r.Name, &r.Lines, &r.Drive); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) WriteProcessorResult(ctx context.Context, name, run string, w io.Writer) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT line FROM processor_output_lines WHERE name=? AND (?='' OR run_id=?) ORDER BY id`, name, run, run)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return count, err
		}
		if _, err := io.WriteString(w, line); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
}
