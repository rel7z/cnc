package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDefaultTemplateMigrationPersistenceAndRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "processor.db")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the already deployed template table, which has no default column.
	_, err = s.db.Exec(`CREATE TABLE version_check_templates (
	 id TEXT PRIMARY KEY, name TEXT NOT NULL COLLATE NOCASE UNIQUE,
	 checks TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1,
	 created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListVersionTemplates(context.Background())
	if err != nil || len(list) != 1 || !list[0].IsDefault || len(list[0].Checks) != 3 {
		t.Fatalf("default migration: %v, %d", err, len(list))
	}
	for _, check := range list[0].Checks {
		if check.Target == "php:php-src" {
			t.Fatal(check)
		}
	}
	if err := s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	list, err = s.ListVersionTemplates(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("duplicate after restart: %v, %d", err, len(list))
	}
	if err := s.DeleteVersionTemplate(context.Background(), list[0].ID, list[0].Revision); err != nil {
		t.Fatal(err)
	}
	if err := s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	list, err = s.ListVersionTemplates(context.Background())
	if err != nil || len(list) != 0 {
		t.Fatalf("deleted default was reseeded: %v, %d", err, len(list))
	}
}

func TestRuntimeDefaultUpgradePreservesOldAndCustomTemplates(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "processor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	current, err := s.ListVersionTemplates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	custom, err := s.CreateVersionTemplate(ctx, "Owner custom", current[0].Checks[:1])
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the legacy deployed seed, preserving an owner-created template.
	if _, err := s.db.Exec(`DELETE FROM version_check_templates WHERE is_default=1;
 DELETE FROM version_template_seeds;
 INSERT INTO version_template_seeds(key) VALUES('default-cms-20261014');`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO version_check_templates("+templateColumns+") VALUES(?,?,?,?,?,?,1)", "builtin-cms-default-20261014", "Default CMS version checks", `[{"id":"old","name":"Old owner check","target":"wordpress:core","identifier":"","group":"","clauses":[{"operator":"<","version":"6.0"}]}]`, 4, "old-created", "old-updated"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListVersionTemplates(ctx)
	if err != nil || len(list) != 3 {
		t.Fatalf("upgrade: %v (%d)", err, len(list))
	}
	defaults := 0
	for _, row := range list {
		if row.IsDefault {
			defaults++
			if len(row.Checks) != 3 || row.Checks[2].Identifier != "translatepress-multilingual" || row.Checks[2].Clauses[0].Version != "3.3.1" {
				t.Fatal("wrong updated default")
			}
		}
		if row.ID == "builtin-cms-default-20261014" && (row.IsDefault || row.Checks[0].Name != "Old owner check" || row.Checks[0].Clauses[0].Version != "6.0") {
			t.Fatal("legacy data lost")
		}
		if row.ID == custom.ID && (row.Revision != custom.Revision || row.IsDefault) {
			t.Fatal("custom template changed")
		}
	}
	if defaults != 1 {
		t.Fatalf("%d defaults", defaults)
	}
	if err := s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	again, err := s.ListVersionTemplates(ctx)
	if err != nil || len(again) != 3 {
		t.Fatal("duplicate on future patch")
	}
}
