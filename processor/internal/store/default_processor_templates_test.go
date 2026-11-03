package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/user/cnc-processor/cluster"
)

func TestProcessorDefaultsPersistWithoutActivationOrOverwriting(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "processor.db")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	list, err := s.ProcessorTemplates(ctx)
	if err != nil || len(list) != 2 {
		t.Fatal("missing defaults", list, err)
	}
	for _, item := range list {
		var d cluster.EditorDefinition
		if err = json.Unmarshal(item.Definition, &d); err != nil {
			t.Fatal(err)
		}
		if err = cluster.ValidateEditorDefinition(d); err != nil {
			t.Fatal(err)
		}
		// Public deployment examples and the database defaults must not drift.
		raw, err := os.ReadFile(filepath.Join("..", "..", "examples", d.ToolID+".template.json"))
		if err != nil {
			t.Fatal(err)
		}
		var example struct {
			Definition cluster.EditorDefinition `json:"definition"`
		}
		if err = json.Unmarshal(raw, &example); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(example.Definition, d) {
			t.Fatal("example/default mismatch", d.ToolID)
		}
		if !item.IsDefault {
			t.Fatal("default marker missing")
		}
	}
	if _, err = s.SaveProcessorTemplate(ctx, list[0].ID, "Owner custom name", list[0].Definition, list[0].Revision); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteProcessorTemplate(ctx, list[1].ID, list[1].Revision); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	list, err = s.ProcessorTemplates(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "Owner custom name" {
		t.Fatal("defaults overwrite owner edits/deletions", list, err)
	}
	enabled, _ := s.IsEnabled(ctx)
	var programs int
	s.db.QueryRow("SELECT COUNT(*) FROM processor_programs").Scan(&programs)
	if enabled || programs != 0 {
		t.Fatal("seeding must not activate or execute programs")
	}
}
