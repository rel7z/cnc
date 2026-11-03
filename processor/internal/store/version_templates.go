package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"cnc-enum/version"
	"github.com/google/uuid"
	"modernc.org/sqlite"
)

var ErrTemplateNotFound = errors.New("template no longer exists; refresh the list")
var ErrTemplateConflict = errors.New("template changed; reload it before updating or deleting")
var ErrTemplateName = errors.New("a template with this name already exists")

//go:embed default-version-checks.json
var defaultVersionChecksJSON []byte

type TemplateCheck struct {
	ID         string                  `json:"id"`
	Name       string                  `json:"name"`
	Target     string                  `json:"target"`
	Identifier string                  `json:"identifier"`
	Group      string                  `json:"group"`
	Clauses    []version.VersionClause `json:"clauses"`
}

type VersionTemplate struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Checks    []TemplateCheck `json:"checks"`
	Revision  int64           `json:"revision"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
	IsDefault bool            `json:"is_default"`
}

// Validate the scanner contract without rewriting the editable draft: clause
// order, original version spelling and blank automatic names must round-trip.
func ValidateTemplate(name string, checks []TemplateCheck) error {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(strings.TrimSpace(name)) > 120 {
		return errors.New("template name is required and must be at most 120 characters")
	}
	if len(checks) == 0 {
		return errors.New("templates require at least one version check")
	}
	rules := make([]version.VersionRule, 0, len(checks))
	for i, c := range checks {
		switch c.Target {
		case "php:php-src", "wordpress:core", "wordpress:plugin", "joomla:core", "joomla:component", "joomla:module", "joomla:plugin", "joomla:library", "joomla:template", "joomla:package":
		default:
			return fmt.Errorf("version check %d has an unsupported component type", i+1)
		}
		component := c.Target
		if c.Target != "php:php-src" && c.Target != "wordpress:core" && c.Target != "joomla:core" {
			if c.Target == "joomla:plugin" {
				component += ":" + c.Group
			}
			component += ":" + c.Identifier
		}
		if len(c.Clauses) == 0 || len(c.Clauses) > 32 {
			return fmt.Errorf("version check %d requires between 1 and 32 conditions", i+1)
		}
		name := strings.TrimSpace(c.Name)
		if name == "" {
			name = templateCheckSummary(c)
		}
		rules = append(rules, version.VersionRule{ID: c.ID, Name: name, Component: component, Clauses: c.Clauses})
	}
	_, err := version.NormalizeRules(rules)
	return err
}

func templateCheckSummary(c TemplateCheck) string {
	parts := strings.Split(c.Target, ":")
	if len(parts) != 2 {
		return c.Target
	}
	cms := "Joomla"
	if parts[0] == "wordpress" {
		cms = "WordPress"
	}
	subject := cms + " " + parts[1]
	if c.Target == "php:php-src" {
		subject = "PHP runtime"
	}
	if parts[1] != "core" && c.Target != "php:php-src" {
		subject += " " + c.Identifier
	}
	if c.Target == "joomla:plugin" {
		subject += " (" + c.Group + ")"
	}
	if len(c.Clauses) == 2 && c.Clauses[0].Operator == ">=" && c.Clauses[1].Operator == "<=" {
		return subject + ", between " + c.Clauses[0].Version + " and " + c.Clauses[1].Version + " (inclusive)"
	}
	labels := map[string]string{"=": "exactly", "<": "older than", "<=": "at most", ">": "newer than", ">=": "at least"}
	conditions := []string{}
	for _, clause := range c.Clauses {
		conditions = append(conditions, labels[clause.Operator]+" "+clause.Version)
	}
	return subject + ", " + strings.Join(conditions, " and ")
}

func templateDBError(err error) error {
	var constraint *sqlite.Error
	if errors.As(err, &constraint) && constraint.Code() == 2067 {
		return ErrTemplateName
	}
	return err
}

func scanTemplate(row interface{ Scan(...any) error }) (VersionTemplate, error) {
	var t VersionTemplate
	var raw string
	err := row.Scan(&t.ID, &t.Name, &raw, &t.Revision, &t.CreatedAt, &t.UpdatedAt, &t.IsDefault)
	if err == sql.ErrNoRows {
		return t, ErrTemplateNotFound
	}
	if err != nil {
		return t, err
	}
	err = json.Unmarshal([]byte(raw), &t.Checks)
	return t, err
}

const templateColumns = "id,name,checks,revision,created_at,updated_at,is_default"

func (s *Store) seedDefaultVersionTemplate() error {
	var checks []TemplateCheck
	if err := json.Unmarshal(defaultVersionChecksJSON, &checks); err != nil {
		return err
	}
	if err := ValidateTemplate("Default CMS version checks", checks); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("INSERT OR IGNORE INTO version_template_seeds(key) VALUES(?)", "default-cms-runtime-20261025")
	if err != nil {
		return err
	}
	added, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if added == 1 {
		if _, err := tx.Exec("UPDATE version_check_templates SET is_default=0,revision=revision+1 WHERE is_default=1"); err != nil {
			return err
		}
		name := "Default CMS and runtime version checks"
		for suffix := 2; ; suffix++ {
			var occupied int
			if err := tx.QueryRow("SELECT COUNT(*) FROM version_check_templates WHERE name=?", name).Scan(&occupied); err != nil {
				return err
			}
			if occupied == 0 {
				break
			}
			name = fmt.Sprintf("Default CMS version checks (%d)", suffix)
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.Exec("INSERT INTO version_check_templates("+templateColumns+") VALUES(?,?,?,?,?,?,1)", "builtin-cms-runtime-default-20261025", name, string(defaultVersionChecksJSON), 1, now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListVersionTemplates(ctx context.Context) ([]VersionTemplate, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+templateColumns+" FROM version_check_templates ORDER BY name COLLATE NOCASE,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VersionTemplate{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) GetVersionTemplate(ctx context.Context, id string) (VersionTemplate, error) {
	return scanTemplate(s.db.QueryRowContext(ctx, "SELECT "+templateColumns+" FROM version_check_templates WHERE id=?", id))
}
func (s *Store) CreateVersionTemplate(ctx context.Context, name string, checks []TemplateCheck) (VersionTemplate, error) {
	if err := ValidateTemplate(name, checks); err != nil {
		return VersionTemplate{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	t := VersionTemplate{ID: uuid.NewString(), Name: strings.TrimSpace(name), Checks: checks, Revision: 1, CreatedAt: now, UpdatedAt: now}
	raw, err := json.Marshal(checks)
	if err != nil {
		return t, err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO version_check_templates("+templateColumns+") VALUES(?,?,?,?,?,?,0)", t.ID, t.Name, string(raw), t.Revision, now, now)
	return t, templateDBError(err)
}
func (s *Store) UpdateVersionTemplate(ctx context.Context, id, name string, checks []TemplateCheck, revision int64) (VersionTemplate, error) {
	if err := ValidateTemplate(name, checks); err != nil {
		return VersionTemplate{}, err
	}
	raw, err := json.Marshal(checks)
	if err != nil {
		return VersionTemplate{}, err
	}
	// RETURNING captures this exact revision even if another client writes next.
	t, err := scanTemplate(s.db.QueryRowContext(ctx, "UPDATE version_check_templates SET name=?,checks=?,revision=revision+1,updated_at=? WHERE id=? AND revision=? RETURNING "+templateColumns, strings.TrimSpace(name), string(raw), time.Now().UTC().Format(time.RFC3339Nano), id, revision))
	if err == ErrTemplateNotFound {
		if _, e := s.GetVersionTemplate(ctx, id); e == nil {
			err = ErrTemplateConflict
		} else {
			err = e
		}
	}
	return t, templateDBError(err)
}
func (s *Store) DeleteVersionTemplate(ctx context.Context, id string, revision int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM version_check_templates WHERE id=? AND revision=?", id, revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		if _, e := s.GetVersionTemplate(ctx, id); e != nil {
			return e
		}
		return ErrTemplateConflict
	}
	return nil
}
