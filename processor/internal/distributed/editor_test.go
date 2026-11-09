package distributed

import (
	"context"
	"encoding/json"
	"github.com/user/cnc-processor/cluster"
	"github.com/user/cnc-processor/internal/watcher"
	"os"
	"path/filepath"
	"testing"
)

func TestEditorAppliesManagedChainAndPreservesExternalRegistry(t *testing.T) {
	c, _ := newFixture(t, true, false)
	ctx := context.Background()
	c.PinnedPrograms = map[string]bool{"fixture": true}
	original, _ := c.Store.CurrentProgram(ctx, "fixture")
	os.Remove(filepath.Join(c.AppDir, "fixture.py"))
	os.MkdirAll(filepath.Join(c.AppDir, "uploads"), 0700)
	os.WriteFile(filepath.Join(c.AppDir, "uploads/scanner.py"), []byte("pass\n"), 0600)
	os.WriteFile(filepath.Join(c.AppDir, "uploads/check.sh"), []byte("true\n"), 0600)
	var definition cluster.EditorDefinition
	json.Unmarshal([]byte(`{"tool_id":"owned","incoming":"incoming.txt","folder":"uploads","manifest":{"input":"targets.txt","scripts":{"scanner":{"path":"scanner.py","outputs":[{"path":"results.txt","send_to":"checker","drive":false}]},"checker":{"path":"check.sh","input":"results.txt","outputs":["success.txt"]}}}}`), &definition)
	state, _ := c.EditorList(ctx)
	revision := state.(EditorState).Revision
	result, err := c.EditorApply(ctx, definition, revision)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.(EditorState).Definitions) != 1 {
		t.Fatal(result)
	}
	current, _ := c.Store.CurrentProgram(ctx, "fixture")
	if current != original {
		t.Fatal("external version changed")
	}
	if _, err := c.EditorApply(ctx, definition, revision); err == nil {
		t.Fatal("stale apply accepted")
	}
	paths := c.EditorRoutes()
	if len(paths["incoming.txt"]) != 1 || paths["incoming.txt"][0] != "owned--scanner" {
		t.Fatal(paths)
	}
	capture := &watcher.Capturer{Store: c.Store, Dir: c.WatchDir}
	if err := capture.Reconfigure(func() (map[string][]string, error) { return paths, nil }); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(c.WatchDir, "incoming.txt"), []byte("owned\n"), 0600)
	if err := capture.Capture(ctx, "incoming.txt"); err != nil {
		t.Fatal(err)
	}
	queued, err := c.Store.QueuedRuns(ctx)
	if err != nil || len(queued) != 1 || queued[0].ProgramID != "owned--scanner" {
		t.Fatal(queued, err)
	}
	// Failed updates retain the previous configuration and queued work.
	before, _ := os.ReadFile(filepath.Join(c.AppDir, "config.json"))
	definition.Manifest.Scripts["scanner"] = cluster.ManifestScript{Path: "missing.py", Outputs: []cluster.OutputPolicy{{Path: "results.txt", Drive: true}}}
	if _, err := c.EditorApply(ctx, definition, result.(EditorState).Revision); err == nil {
		t.Fatal("missing source accepted")
	}
	after, _ := os.ReadFile(filepath.Join(c.AppDir, "config.json"))
	if string(before) != string(after) {
		t.Fatal("failed apply replaced live config")
	}
	if err := c.Prepare(ctx); err != nil {
		t.Fatal("restart failed", err)
	}
}

func TestEditorMultipleToolsKeepSeparateRoutesAndFailedSecondApply(t *testing.T) {
	c, _ := newFixture(t, true, false)
	ctx := context.Background()
	c.PinnedPrograms = map[string]bool{"fixture": true}
	os.MkdirAll(filepath.Join(c.AppDir, "uploads"), 0700)
	os.WriteFile(filepath.Join(c.AppDir, "uploads", "one.py"), []byte("pass\n"), 0600)
	os.WriteFile(filepath.Join(c.AppDir, "uploads", "two.py"), []byte("pass\n"), 0600)
	definition := func(id, incoming, path string) cluster.EditorDefinition {
		var d cluster.EditorDefinition
		raw := `{"tool_id":"` + id + `","incoming":"` + incoming + `","folder":"uploads","manifest":{"input":"` + incoming + `","scripts":{"scanner":{"path":"` + path + `","outputs":["results.txt"]}}}}`
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	state, err := c.EditorList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := c.EditorApply(ctx, definition("first", "first.txt", "one.py"), state.(EditorState).Revision)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(c.AppDir, "config.json"))
	if _, err = c.EditorApply(ctx, definition("second", "second.txt", "missing.py"), first.(EditorState).Revision); err == nil {
		t.Fatal("missing second source accepted")
	}
	after, _ := os.ReadFile(filepath.Join(c.AppDir, "config.json"))
	if string(before) != string(after) {
		t.Fatal("failed second apply changed first tool")
	}
	second, err := c.EditorApply(ctx, definition("second", "second.txt", "two.py"), first.(EditorState).Revision)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.(EditorState).Definitions) != 2 {
		t.Fatal("first setup was replaced", second)
	}
	routes := c.EditorRoutes()
	if len(routes["first.txt"]) != 1 || routes["first.txt"][0] != "first--scanner" || len(routes["second.txt"]) != 1 || routes["second.txt"][0] != "second--scanner" {
		t.Fatal(routes)
	}
	if err := c.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	restarted, err := c.EditorList(ctx)
	if err != nil || len(restarted.(EditorState).Definitions) != 2 {
		t.Fatal("restart lost setup", restarted, err)
	}
}
