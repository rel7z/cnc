package cluster

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt-in only: the caller must isolate networking and explicitly supply a
// permitted executable fixture. No production tool path is discovered here.
func TestRuntimeBinaryEmptyInputSummary(t *testing.T) {
	path := os.Getenv("CNC_TEST_EMPTY_BINARY")
	expected := os.Getenv("CNC_TEST_EMPTY_SUMMARY")
	if runtime.GOOS != "linux" || path == "" || expected == "" {
		t.Skip("requires an explicitly supplied Linux fixture in a network-isolated test service")
	}
	if !filepath.IsAbs(path) {
		t.Fatal("fixture path must be absolute")
	}
	source := t.TempDir()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"fixture": data, "pass.txt": []byte("fixture-user:fixture-password\n")} {
		if err := os.WriteFile(filepath.Join(source, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := Program{Entrypoint: "fixture", Files: []string{"pass.txt"}, AutoPublish: AutoPublish{Enabled: true, Directory: "work"}, Script: &ScriptExecution{Runtime: "binary", BinaryArch: runtime.GOARCH, Input: "wordpress.txt", Outputs: []string{"results.txt"}}}
	bundle, version, err := BuildBundle(source, "empty-fixture", p)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var log string
	completed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case "bundle":
			w.Write(bundle)
		case "renew":
			json.NewEncoder(w).Encode(Lease{Seconds: 30})
		case "input":
			w.Write([]byte{}) // No targets, including no localhost login targets.
		case "log":
			body, e := io.ReadAll(r.Body)
			if e != nil {
				t.Error(e)
			}
			mu.Lock()
			log = string(body)
			mu.Unlock()
			w.Write([]byte(`{}`))
		case "complete":
			var c Completion
			if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
				t.Error(err)
			}
			mu.Lock()
			completed = c.ExitCode == 0 && c.Error == "" && strings.Contains(log, expected)
			mu.Unlock()
			w.Write([]byte(`{}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := InstallBundle(ctx, root, Install{Version: version, BundleURL: srv.URL + "/bundle"}); err != nil {
		t.Fatal(err)
	}
	a := Assignment{AttemptID: "summary-fixture", RunID: "summary-fixture", ProgramID: "empty-fixture", Version: version, BaseURL: srv.URL, Token: "dummy-only", PublishOnly: true, TimeoutSeconds: 10}
	if err := (&Runner{Root: root}).Run(ctx, a); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !completed {
		t.Fatalf("completion did not retain the expected summary; log bytes=%d", len(log))
	}
	t.Log("VERIFIED: empty-input binary summary retained through supervisor, worker log upload and completion acknowledgment")
}
