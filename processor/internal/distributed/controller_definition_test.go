package distributed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/user/cnc-processor/cluster"
)

func TestControllerConfiguredProgramCanBeEditedWithoutFolderJSON(t *testing.T) {
	c, _ := newFixture(t, true, false)
	ctx := context.Background()
	folder := filepath.Join(c.AppDir, "source")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "scanner.py"), []byte("pass\n"), 0600); err != nil {
		t.Fatal(err)
	}
	definition := cluster.ToolManifest{Input: "input.txt", Scripts: map[string]cluster.ManifestScript{"scanner": {Path: "scanner.py", Outputs: []cluster.OutputPolicy{{Path: "results.txt", Drive: true}}}}}
	config := cluster.Config{ExecutionMode: "queue", Programs: map[string]cluster.Program{"demo": {Folder: "source", Definition: &definition}}, Routes: map[string][]string{"incoming.txt": {"demo"}}}
	raw, _ := json.Marshal(config)
	if err := os.WriteFile(filepath.Join(c.AppDir, "config.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := c.EditorList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	listed := state.(EditorState)
	if len(listed.Definitions) != 1 || len(listed.External) != 0 {
		t.Fatal(listed)
	}
	d := listed.Definitions[0]
	d.Incoming = "next-input.txt"
	result, err := c.EditorApply(ctx, d, listed.Revision)
	if err != nil {
		t.Fatal(err)
	}
	active := result.(EditorState).Definitions[0]
	if active.Incoming != "next-input.txt" {
		t.Fatal(active)
	}
	for _, name := range []string{"config.json", "manifest.cnc.json", "definition.json"} {
		if _, err := os.Lstat(filepath.Join(c.AppDir, active.Folder, name)); !os.IsNotExist(err) {
			t.Fatalf("controller still generated folder JSON %s: %v", name, err)
		}
	}
	if err = c.Prepare(ctx); err != nil {
		t.Fatal("restart without a tool manifest failed", err)
	}
	restarted, err := c.EditorList(ctx)
	if err != nil || len(restarted.(EditorState).Definitions) != 1 {
		t.Fatal(restarted, err)
	}
}

func TestRepositoryRegistrationsAreListedFromControllerSettingsOnly(t *testing.T) {
	// Listing must use only the central config, without opening program folders.
	c := &Controller{AppDir: filepath.Join("..", "..")}
	result, err := c.EditorList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := result.(EditorState)
	if len(state.External) != 0 || len(state.Definitions) != 2 {
		t.Fatalf("registrations still depend on external manifests: %+v", state)
	}
	seen := map[string]bool{}
	for _, d := range state.Definitions {
		if err := cluster.ValidateEditorDefinition(d); err != nil {
			t.Fatal(err)
		}
		seen[d.ToolID] = true
	}
	if !seen["bf"] || !seen["divi"] {
		t.Fatal("existing registrations were lost")
	}
}
