package distributed

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/user/cnc-processor/internal/watcher"
)

func TestManifestCaptureUsesExpandedRegistry(t *testing.T) {
	c, _ := newFixture(t, true, false)
	t.Setenv("CNC_APP_DIR", c.AppDir)
	os.WriteFile(filepath.Join(c.AppDir, "tool.json"), []byte(`{"scripts":{"a":{"path":"fixture.py","outputs":["a.txt"]},"b":{"path":"fixture.py","outputs":["b.txt"]}}}`), 0600)
	os.WriteFile(filepath.Join(c.AppDir, "config.json"), []byte(`{"routes":{"manifest-input.txt":["folder"]},"programs":{"folder":"tool.json"}}`), 0600)
	if err := c.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(c.WatchDir, "manifest-input.txt"), []byte("one\n"), 0600)
	if err := (&watcher.Capturer{Store: c.Store, Dir: c.WatchDir}).Capture(context.Background(), "manifest-input.txt"); err != nil {
		t.Fatal(err)
	}
	counts, err := c.Store.RunCounts(context.Background())
	if err != nil || counts["queued"] != 2 {
		t.Fatalf("counts=%v err=%v", counts, err)
	}
}

func TestPinnedCoreRolloutDoesNotReadSources(t *testing.T) {
	c, _ := newFixture(t, true, false)
	before, err := c.Store.CurrentProgram(context.Background(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(c.AppDir, "fixture.py")); err != nil {
		t.Fatal(err)
	}
	c.PinnedPrograms = map[string]bool{"fixture": true}
	if err := c.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := c.Store.CurrentProgram(context.Background(), "fixture")
	if err != nil || before != after {
		t.Fatalf("version changed: %v %v %v", before, after, err)
	}
	os.WriteFile(filepath.Join(c.AppDir, "config.json"), []byte(`{"routes":{"fixture.log":["fixture"]},"programs":{"fixture":{"entrypoint":"fixture.py","max_parallel":2}}}`), 0600)
	if err := c.Prepare(context.Background()); err == nil {
		t.Fatal("changed settings silently reused pinned version")
	}
}
