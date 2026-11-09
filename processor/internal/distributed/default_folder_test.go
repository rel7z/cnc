package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user/cnc-processor/api"
	"github.com/user/cnc-processor/cluster"
	"github.com/user/cnc-processor/internal/watcher"
)

// Uses only unrestricted fixture files. The real capturer, SQLite queue,
// scheduler, API, bundle installer, supervisor and publication path all run.
func TestDefaultFolderTenThousandLines(t *testing.T) {
	for _, scenario := range []struct{ label, kind, merged string }{{"python", "python", ""}, {"binary", "binary", ""}, {"renamed-workspaces", "python", "apalah.txt"}} {
		kind := scenario.kind
		t.Run(scenario.label, func(t *testing.T) {
			if kind == "binary" && runtime.GOOS != "linux" {
				t.Skip("Linux binary execution is verified on Linux")
			}
			if _, err := exec.LookPath("python3"); err != nil {
				t.Fatal(err)
			}
			c, _ := newFixture(t, true, false)
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			folder := filepath.Join(c.AppDir, "simulation")
			if err := os.Mkdir(folder, 0700); err != nil {
				t.Fatal(err)
			}
			entry := "scanner.py"
			fixture := filepath.Join("..", "..", "examples", "default-folder", entry)
			if kind == "binary" {
				entry = "rr"
				fixture = os.Getenv("CNC_FOLDER_FIXTURE_BINARY")
				if fixture == "" {
					fixture = filepath.Join(t.TempDir(), "rr")
					build := exec.CommandContext(ctx, "go", "build", "-o", fixture, filepath.Join("..", "..", "examples", "default-folder", "scanner.go"))
					if out, err := build.CombinedOutput(); err != nil {
						t.Fatalf("build fixture: %s %v", out, err)
					}
				}
			}
			data, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(folder, entry), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(folder, "pass.txt"), []byte("fixture-support\n"), 0600); err != nil {
				t.Fatal(err)
			}
			manifest := cluster.ToolManifest{Input: "wordpress.txt", Files: []string{"pass.txt"}, Scripts: map[string]cluster.ManifestScript{
				"scanner": {Path: entry, Runtime: kind, Outputs: []cluster.OutputPolicy{{Path: "results.txt", MergedName: scenario.merged, Drive: true}}},
			}}
			data, _ = json.Marshal(manifest)
			if err = os.WriteFile(filepath.Join(folder, "tool.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			config := cluster.Config{Routes: map[string][]string{"wordpress.txt": {"folder-demo"}}, Programs: map[string]cluster.Program{"folder-demo": {Manifest: "simulation/tool.json"}}}
			data, _ = json.Marshal(config)
			if err = os.WriteFile(filepath.Join(c.AppDir, "config.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err = c.Prepare(ctx); err != nil {
				t.Fatal(err)
			}
			lines := make([]string, 10000)
			for i := range lines {
				lines[i] = fmt.Sprintf("fixture-%05d\n", i)
			}
			if err = os.WriteFile(filepath.Join(c.WatchDir, "wordpress.txt"), []byte(strings.Join(lines, "")), 0600); err != nil {
				t.Fatal(err)
			}
			capture := &watcher.Capturer{Store: c.Store, Dir: c.WatchDir, Routes: c.EditorRoutes()}
			if err = capture.Capture(ctx, "wordpress.txt"); err != nil {
				t.Fatal(err)
			}
			// Repeated observation of the same source must not add jobs.
			if err = capture.Capture(ctx, "wordpress.txt"); err != nil {
				t.Fatal(err)
			}
			runs, err := c.Store.ListRuns(ctx)
			if err != nil || len(runs) != 10 {
				t.Fatalf("want ten 1000-line jobs, got %d: %v", len(runs), err)
			}
			spec, err := c.Store.PinRun(ctx, runs[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer((&api.Server{Store: c.Store, WorkerAPI: http.HandlerFunc(c.WorkerHandler)}).Handler(c.Token))
			defer server.Close()
			c.WorkerURL = server.URL + "/v1"
			var mu sync.Mutex
			var wg sync.WaitGroup
			workers := []cluster.Worker{}
			runners := map[string]*cluster.Runner{}
			roots := map[string]string{}
			assigned := map[string]string{}
			failures := []error{}
			live := false
			for i := 0; i < 6; i++ {
				id, root := fmt.Sprintf("simulation-worker-%d", i), t.TempDir()
				if _, err = cluster.InstallBundle(ctx, root, cluster.Install{Version: spec.Version, BundleURL: c.WorkerURL + "/bundles/" + spec.Version, Token: c.installToken(spec.Version)}); err != nil {
					t.Fatal(err)
				}
				roots[id] = root
				runners[id] = &cluster.Runner{Root: root}
				if scenario.merged != "" {
					runners[id].WorkspacesRoot = filepath.Join(root, "visible-programs")
				}
				workers = append(workers, cluster.Worker{ID: id, Status: "online", MaxTasks: 1, Protocol: cluster.ProtocolVersion, Versions: []string{spec.Version}, Resources: cluster.ResourceSnapshot{CPUCores: 4, TotalMemoryMB: 4096, AvailableMemoryMB: 4096, Capabilities: cluster.DetectRuntimeCapabilities()}})
			}
			broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if strings.HasSuffix(r.URL.Path, "/publish") {
					var publication cluster.Publication
					if err := json.NewDecoder(r.Body).Decode(&publication); err != nil {
						http.Error(w, err.Error(), 400)
						return
					}
					if err := cluster.MergeText(c.WatchDir, publication.Name, publication.Lines); err != nil {
						http.Error(w, err.Error(), 500)
						return
					}
					counts, _ := c.Store.RunCounts(ctx)
					live = live || counts["running"] > 0
					w.Write([]byte(`{}`))
					return
				}
				if r.Method == "GET" {
					for i := range workers {
						workers[i].Resources.SampledAt = time.Now().UnixMilli()
					}
					json.NewEncoder(w).Encode(workers)
					return
				}
				var request struct {
					WorkerID   string             `json:"worker_id"`
					Assignment cluster.Assignment `json:"assignment"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				assigned[request.Assignment.RunID] = request.WorkerID
				for i := range workers {
					if workers[i].ID == request.WorkerID {
						if workers[i].CurrentLoad != 0 {
							t.Error("worker overbooked")
						}
						workers[i].CurrentLoad++
					}
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					err := runners[request.WorkerID].Run(ctx, request.Assignment)
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						failures = append(failures, err)
					}
					for i := range workers {
						if workers[i].ID == request.WorkerID {
							workers[i].CurrentLoad--
						}
					}
				}()
				w.Write([]byte(`{"accepted":true}`))
			}))
			defer broker.Close()
			c.BrokerURL = broker.URL
			for {
				if err = c.step(ctx); err != nil {
					t.Fatal(err)
				}
				counts, err := c.Store.RunCounts(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if counts["completed"] == 10 {
					break
				}
				if counts["failed"] > 0 || ctx.Err() != nil {
					t.Fatalf("simulation did not finish: %v %v", counts, ctx.Err())
				}
				time.Sleep(30 * time.Millisecond)
			}
			wg.Wait()
			// Execution completion and publication transfer are separate states.
			// Each tick transfers one bounded batch; a slow Linux/race run can
			// finish execution with more than one batch still in the outbox.
			for {
				pending, err := c.Store.PendingOutput(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(pending) == 0 {
					break
				}
				if err = c.flushPublications(ctx); err != nil {
					t.Fatal(err)
				}
			}
			mu.Lock()
			used := map[string]bool{}
			for _, id := range assigned {
				used[id] = true
			}
			if len(failures) != 0 || len(assigned) != 10 || len(used) != 6 || !live {
				t.Errorf("failures=%v starts=%d workers=%d live=%v", failures, len(assigned), len(used), live)
			}
			mu.Unlock()
			mergedName := "folder-demo--scanner__results.txt"
			if scenario.merged != "" {
				mergedName = scenario.merged
			}
			data, err = os.ReadFile(filepath.Join(c.WatchDir, mergedName))
			if err != nil {
				t.Fatal(err)
			}
			unique := map[string]bool{}
			for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
				if unique[line] {
					t.Fatalf("duplicate result %s", line)
				}
				unique[line] = true
			}
			if len(unique) != 10000 {
				t.Fatalf("result lines=%d, want 10000", len(unique))
			}
			for _, line := range lines {
				if !unique[strings.TrimSuffix(line, "\n")] {
					t.Fatalf("missing result %q", line)
				}
			}
			data, err = os.ReadFile(filepath.Join(folder, "pass.txt"))
			if err != nil || string(data) != "fixture-support\n" {
				t.Fatal("source supporting file was changed")
			}
			if _, err = os.Stat(filepath.Join(folder, "wordpress.txt")); !os.IsNotExist(err) {
				t.Fatal("input was written into the source folder")
			}
			for _, root := range roots {
				if _, err := cluster.LoadBundle(root, spec.Version); err != nil {
					t.Fatalf("shared bundle cache was changed: %v", err)
				}
			}
			t.Logf("%s: 10000 lines, 10 jobs, 6 workers, live publication, unchanged source and cache", kind)
		})
	}
}
