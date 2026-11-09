package store

import (
	_ "embed"
)

//go:embed schema.sql
var SchemaSQL string

func (s *Store) InitSchema() error {
	if _, err := s.db.Exec(SchemaSQL); err != nil {
		return err
	}
	rows, err := s.db.Query("PRAGMA table_info(version_check_templates)")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var defaultValue any
		if err = rows.Scan(&cid, &name, &kind, &notnull, &defaultValue, &pk); err != nil {
			break
		}
		if name == "is_default" {
			found = true
		}
	}
	if rowsErr := rows.Err(); err == nil {
		err = rowsErr
	}
	rows.Close()
	if err != nil {
		return err
	}
	if !found {
		if _, err = s.db.Exec("ALTER TABLE version_check_templates ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	if _, err = s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS version_templates_one_default ON version_check_templates(is_default) WHERE is_default=1"); err != nil {
		return err
	}
	if err := s.seedDefaultVersionTemplate(); err != nil {
		return err
	}
	return s.seedDefaultProcessorTemplates()
}
