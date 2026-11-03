package cluster

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkspaceRunsInStableFolderAndRetainsPreviousOutput(t *testing.T) {
	source := t.TempDir()
	os.Mkdir(filepath.Join(source, "demo"), 0700)
	script := "pwd > cwd.receipt\ncat input.txt > x.txt\nprintf 'not selected\\n' > ignored.txt\n"
	if err := os.WriteFile(filepath.Join(source, "demo", "run.sh"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	p := Program{Entrypoint: "demo/run.sh", Script: &ScriptExecution{Runtime: "bash", Input: "input.txt", Outputs: []string{"x.txt"}}, AutoPublish: AutoPublish{Enabled: true, Directory: "work"}}
	data, version, err := BuildBundle(source, "demo--scanner", p)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	published := map[string][]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch parts[len(parts)-1] {
		case "bundle":
			w.Write(data)
		case "renew":
			json.NewEncoder(w).Encode(Lease{Seconds: 30})
		case "input":
			w.Write([]byte(parts[1] + "\n"))
		case "publish":
			var pub Publication
			json.NewDecoder(r.Body).Decode(&pub)
			if pub.Name != "demo--scanner__x.txt" {
				t.Errorf("unexpected file published: %s", pub.Name)
			}
			mu.Lock()
			published[parts[1]] = append(published[parts[1]], pub.Lines...)
			mu.Unlock()
			w.Write([]byte(`{"accepted":true}`))
		case "complete":
			var c Completion
			json.NewDecoder(r.Body).Decode(&c)
			if c.ExitCode != 0 {
				t.Errorf("fixture failed: %+v", c)
			}
			w.Write([]byte(`{}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	runner := &Runner{Root: t.TempDir(), WorkspacesRoot: t.TempDir()}
	if _, err = InstallBundle(context.Background(), runner.Root, Install{Version: version, BundleURL: srv.URL + "/bundle"}); err != nil {
		t.Fatal(err)
	}
	work, err := runner.PrepareWorkspace(context.Background(), version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(work, "run.sh")); err != nil {
		t.Fatal("program not beside workspace", err)
	}
	for _, id := range []string{"first", "second"} {
		err = runner.Run(context.Background(), Assignment{AttemptID: id, RunID: id, ProgramID: "demo--scanner", Version: version, BaseURL: srv.URL, PublishOnly: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	cwd, err := os.ReadFile(filepath.Join(work, "cwd.receipt"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(work)
	if strings.TrimSpace(string(cwd)) != resolved {
		t.Fatalf("ran in %q, want %q", cwd, resolved)
	}
	current, _ := os.ReadFile(filepath.Join(work, "x.txt"))
	previous, _ := os.ReadFile(filepath.Join(work, ".cnc/history/second/x.txt"))
	if string(current) != "second\n" || string(previous) != "first\n" {
		t.Fatalf("current=%q previous=%q", current, previous)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(published["first"], "") != "first\n" || strings.Join(published["second"], "") != "second\n" {
		t.Fatal("stale output was republished", published)
	}
}

func TestWorkspaceLockSerializesManifestStepsAndHonorsCancellation(t *testing.T) {
	r := &Runner{WorkspacesRoot: t.TempDir()}
	_, release, err := r.lockWorkspace(context.Background(), "demo--first")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, unlock, err := r.lockWorkspace(ctx, "demo--second"); err == nil {
		unlock()
		t.Fatal("same folder admitted two concurrent runs")
	}
	release()
	_, release, err = r.lockWorkspace(context.Background(), "demo--second")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, _, err := r.lockWorkspace(context.Background(), "../escape"); err == nil {
		t.Fatal("accepted invalid folder")
	}
}

func TestWorkspaceRejectsSymlinkOutputWithoutFollowingIt(t *testing.T) {
	work := t.TempDir()
	outside := filepath.Join(t.TempDir(), "kept.txt")
	os.WriteFile(outside, []byte("kept"), 0600)
	os.Symlink(outside, filepath.Join(work, "x.txt"))
	err := archiveWorkspaceOutputs(work, &Bundle{Script: &ScriptExecution{Outputs: []string{"x.txt"}}}, "a1")
	if err == nil {
		t.Fatal("followed output link")
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "kept" {
		t.Fatal("outside file changed")
	}
}

func TestWorkspaceLegacyExportNeverPublishesItsInput(t *testing.T) {
	runner := &Runner{Root: t.TempDir(), WorkspacesRoot: t.TempDir()}
	version := fixtureBundle(t, runner.Root, "import sys,pathlib\npathlib.Path(sys.argv[2], 'result.txt').write_text('result\\n')\n")
	var completion Completion
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case "renew":
			json.NewEncoder(w).Encode(Lease{Seconds: 30})
		case "input":
			w.Write([]byte("input must stay local\n"))
		case "complete":
			json.NewDecoder(r.Body).Decode(&completion)
			w.Write([]byte(`{}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	if err := runner.Run(context.Background(), Assignment{AttemptID: "legacy", RunID: "legacy", ProgramID: "fixture", Version: version, BaseURL: srv.URL, Export: true}); err != nil {
		t.Fatal(err)
	}
	if completion.ExitCode != 0 || len(completion.Artifacts) != 1 || completion.Artifacts[0].Name != "result_legacy.txt" {
		t.Fatalf("input exported: %+v", completion)
	}
}
