package cluster

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureBundle(t *testing.T, root, script string, options ...Program) string {
	t.Helper()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "fixture.py"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	program := Program{Entrypoint: "fixture.py"}
	if len(options) > 0 {
		program = options[0]
		program.Entrypoint = "fixture.py"
	}
	data, version, err := BuildBundle(source, "fixture", program)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer srv.Close()
	if _, err = InstallBundle(context.Background(), root, Install{Version: version, BundleURL: srv.URL, Token: "install"}); err != nil {
		t.Fatal(err)
	}
	return version
}
func TestRunnerExecutesOnceAndReturnsVerifiedArtifact(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "executions")
	version := fixtureBundle(t, root, "import sys,pathlib\nwith open("+pythonString(marker)+",'a') as f: f.write('execution\\n')\npathlib.Path(sys.argv[2],'result.txt').write_text(pathlib.Path(sys.argv[1]).read_text().upper())\nprint('fixture complete')\n")
	var mu sync.Mutex
	files := map[string][]byte{}
	var completions []Completion
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer scoped" {
			w.WriteHeader(401)
			return
		}
		action := strings.TrimPrefix(r.URL.Path, "/attempts/a1/")
		switch action {
		case "renew":
			json.NewEncoder(w).Encode(Lease{Seconds: 30, Phase: "running"})
		case "input":
			w.Write([]byte("alpha\nbeta\n"))
		case "start":
			w.Write([]byte("{}"))
		case "complete":
			var c Completion
			if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
				t.Error(err)
			}
			mu.Lock()
			completions = append(completions, c)
			mu.Unlock()
		default:
			var data []byte
			buf := make([]byte, 4096)
			for {
				n, err := r.Body.Read(buf)
				data = append(data, buf[:n]...)
				if err != nil {
					break
				}
			}
			mu.Lock()
			files[action] = data
			mu.Unlock()
		}
	}))
	defer srv.Close()
	a := Assignment{AttemptID: "a1", RunID: "run1", ProgramID: "fixture", Version: version, Token: "scoped", BaseURL: srv.URL, TimeoutSeconds: 10}
	runner := &Runner{Root: root}
	if err := runner.Run(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	count, err := os.ReadFile(marker)
	if err != nil || string(count) != "execution\n" {
		t.Fatalf("execution count: %q %v", count, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(completions) != 1 || completions[0].ExitCode != 0 || len(completions[0].Artifacts) != 1 {
		t.Fatalf("completion: %+v", completions)
	}
	artifact := completions[0].Artifacts[0]
	data := files["artifacts/"+artifact.Name]
	if string(data) != "ALPHA\nBETA\n" || Digest(data) != artifact.SHA256 || int64(len(data)) != artifact.Size {
		t.Fatalf("artifact mismatch: %+v %q", artifact, data)
	}
	if !strings.Contains(string(files["log"]), "fixture complete") {
		t.Fatal("missing execution log")
	}
}
func pythonString(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestRunnerRestartDoesNotRerunUncertainExecution(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "attempts", "a1")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "started"), []byte("authorized"), 0600)
	var c Completion
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "renew") {
			json.NewEncoder(w).Encode(Lease{Seconds: 30})
			return
		}
		if strings.HasSuffix(r.URL.Path, "complete") {
			json.NewDecoder(r.Body).Decode(&c)
		}
		if strings.HasSuffix(r.URL.Path, "start") || strings.HasSuffix(r.URL.Path, "input") {
			t.Error("restarted uncertain attempt tried to execute")
		}
	}))
	defer srv.Close()
	a := Assignment{AttemptID: "a1", RunID: "r1", BaseURL: srv.URL, Token: "scoped"}
	if err := (&Runner{Root: root}).Run(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if !c.Interrupted || c.ExitCode == 0 || !strings.Contains(c.Error, "uncertain") {
		t.Fatalf("uncertain completion: %+v", c)
	}
}

func TestRunnerLeaseLossKillsPython(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	version := fixtureBundle(t, root, "import time,pathlib\ntime.sleep(3)\npathlib.Path("+pythonString(marker)+").write_text('late effect')\n")
	var renews atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "renew") {
			renews.Add(1)
			json.NewEncoder(w).Encode(Lease{Seconds: 2})
			return
		}
		if strings.HasSuffix(r.URL.Path, "input") {
			w.Write([]byte("input\n"))
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	err := (&Runner{Root: root}).Run(ctx, Assignment{AttemptID: "a1", RunID: "r1", ProgramID: "fixture", Version: version, BaseURL: srv.URL, Token: "scoped", TimeoutSeconds: 5})
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("lease did not stop execution promptly: %v", err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("Python survived expired lease: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "attempts", "a1", "completion.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c Completion
	json.Unmarshal(data, &c)
	if !c.Interrupted {
		t.Fatalf("lease failure not marked interrupted: %+v", c)
	}
}
func TestModifiedBundleIsNotAdvertised(t *testing.T) {
	root := t.TempDir()
	version := fixtureBundle(t, root, "print('original')\n")
	if err := os.WriteFile(filepath.Join(root, "bundles", version, "fixture.py"), []byte("print('modified')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if len(InstalledVersions(root)) != 0 {
		t.Fatal("modified bundle advertised as ready")
	}
	if _, err := LoadBundle(root, version); err == nil {
		t.Fatal("modified bundle accepted")
	}
}

func TestRunnerCleansUpBackgroundChildrenAfterParentExit(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "orphan-effect")
	child := "import time,pathlib; time.sleep(2); pathlib.Path(" + pythonString(marker) + ").write_text('orphan survived')"
	script := "import sys,subprocess\nsubprocess.Popen([sys.executable,'-c'," + pythonString(child) + "],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)\n"
	version := fixtureBundle(t, root, script)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "renew") {
			json.NewEncoder(w).Encode(Lease{Seconds: 30})
		}
	}))
	defer srv.Close()
	if err := (&Runner{Root: root}).Run(context.Background(), Assignment{AttemptID: "a1", RunID: "r1", ProgramID: "fixture", Version: version, BaseURL: srv.URL, Token: "scoped"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2200 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("background child survived completed attempt")
	}
}

func TestDeliveryRecoveryTransfersOutboxWithoutRerunningTool(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "executions")
	version := fixtureBundle(t, root, "import pathlib,sys\nfrom processor_publish import publish\nwith open("+pythonString(marker)+",'a') as f:f.write('once\\n')\np=pathlib.Path(sys.argv[2],'result.txt')\np.write_text('saved\\n')\npublish(p)\n")
	var available atomic.Bool
	var delivered atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := strings.TrimPrefix(r.URL.Path, "/attempts/a-recovery/")
		switch action {
		case "renew":
			json.NewEncoder(w).Encode(Lease{Seconds: 30})
		case "input":
			w.Write([]byte("dummy\n"))
		case "publish":
			if !available.Load() {
				http.Error(w, "outage", 503)
				return
			}
			var p Publication
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				t.Error(err)
			}
			if len(p.Lines) != 1 || p.Lines[0] != "saved\n" {
				t.Errorf("recovered content: %+v", p)
			}
			delivered.Add(1)
			w.Write([]byte(`{"accepted":true}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	assignment := Assignment{AttemptID: "a-recovery", RunID: "r-recovery", ProgramID: "fixture", Version: version, Token: "scoped", BaseURL: server.URL, PublishOnly: true, Resources: Program{}.Resources(), TimeoutSeconds: -1}
	runner := &Runner{Root: root}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := runner.Run(ctx, assignment); err == nil {
		t.Fatal("outage unexpectedly acknowledged")
	}
	cancel()
	available.Store(true)
	for i := 0; i < 2; i++ {
		if err := runner.DeliverPending(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "once\n" {
		t.Fatalf("tool ran again: %q %v", data, err)
	}
	if delivered.Load() != 1 {
		t.Fatalf("publication repeated: %d", delivered.Load())
	}
}
