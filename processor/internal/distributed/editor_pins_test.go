package distributed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/user/cnc-processor/cluster"
)

func TestExplicitEditorApplyRefreshesRetainedSelectedBundle(t *testing.T) {
	c, _ := newFixture(t, true, false)
	ctx := context.Background()
	c.PinnedPrograms = map[string]bool{"fixture": true}
	folder := filepath.Join(c.AppDir, "uploads")
	os.MkdirAll(folder, 0700)
	os.WriteFile(filepath.Join(folder, "dummy.py"), []byte("print('first')\n"), 0600)
	d := cluster.EditorDefinition{ToolID: "owned", Incoming: "dummy.txt", Folder: "uploads", Manifest: cluster.ToolManifest{Scripts: map[string]cluster.ManifestScript{"run": {Path: "dummy.py", Outputs: []cluster.OutputPolicy{{Path: "results.txt"}}}}}}
	state, e := c.EditorList(ctx)
	if e != nil {
		t.Fatal(e)
	}
	first, e := c.EditorApply(ctx, d, state.(EditorState).Revision)
	if e != nil {
		t.Fatal(e)
	}
	old, _ := c.Store.CurrentProgram(ctx, "owned--run")
	external, _ := c.Store.CurrentProgram(ctx, "fixture")
	c.PinnedPrograms["owned--run"] = true
	os.WriteFile(filepath.Join(folder, "dummy.py"), []byte("print('updated')\n"), 0600)
	if _, e = c.EditorApply(ctx, d, first.(EditorState).Revision); e != nil {
		t.Fatal(e)
	}
	next, _ := c.Store.CurrentProgram(ctx, "owned--run")
	unchanged, _ := c.Store.CurrentProgram(ctx, "fixture")
	if next.Version == old.Version || external != unchanged || !c.PinnedPrograms["owned--run"] {
		t.Fatal("explicit refresh or unrelated bundle retention failed")
	}
}

func TestRetainedConnectedProgramsStartWithoutReopeningSources(t *testing.T) {
	c, _ := newFixture(t, true, false)
	ctx := context.Background()
	folder := filepath.Join(c.AppDir, "uploads")
	if e := os.MkdirAll(folder, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"first.py", "second.py"} {
		if e := os.WriteFile(filepath.Join(folder, name), []byte("print('dummy')\n"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	d := cluster.EditorDefinition{ToolID: "chain", Incoming: "dummy.txt", Folder: "uploads", Manifest: cluster.ToolManifest{Scripts: map[string]cluster.ManifestScript{
		"first":  {Path: "first.py", Outputs: []cluster.OutputPolicy{{Path: "next.txt", SendTo: "second"}}},
		"second": {Path: "second.py", Outputs: []cluster.OutputPolicy{{Path: "result.txt"}}},
	}}}
	state, e := c.EditorList(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.EditorApply(ctx, d, state.(EditorState).Revision); e != nil {
		t.Fatal(e)
	}
	c.PinnedPrograms = map[string]bool{"fixture": true, "chain--first": true, "chain--second": true}
	first, _ := c.Store.CurrentProgram(ctx, "chain--first")
	second, _ := c.Store.CurrentProgram(ctx, "chain--second")
	for _, name := range []string{"first.py", "second.py"} {
		if e := os.Remove(filepath.Join(folder, name)); e != nil {
			t.Fatal(e)
		}
	}
	data, e := os.ReadFile(filepath.Join(c.AppDir, "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	var config cluster.Config
	if e = json.Unmarshal(data, &config); e != nil {
		t.Fatal(e)
	}
	if config.Programs["chain"].Definition == nil || config.Programs["chain"].Manifest != "" {
		t.Fatal("setup was not stored in the controller")
	}
	if e = os.RemoveAll(filepath.Join(c.AppDir, config.Programs["chain"].Folder)); e != nil {
		t.Fatal(e)
	}
	if e := c.Prepare(ctx); e != nil {
		t.Fatal(e)
	}
	afterFirst, _ := c.Store.CurrentProgram(ctx, "chain--first")
	afterSecond, _ := c.Store.CurrentProgram(ctx, "chain--second")
	if first != afterFirst || second != afterSecond {
		t.Fatal("retained program versions changed")
	}
}
