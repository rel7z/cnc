package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/user/cnc-processor/api"
	"github.com/user/cnc-processor/cluster"
	"github.com/user/cnc-processor/internal/store"
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

func newFixture(t *testing.T, parallel, retry bool) (*Controller, string) {
	t.Helper()
	app := t.TempDir()
	watch := t.TempDir()
	db := filepath.Join(app, "processor.db")
	s, err := store.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	s.SetEnabled(context.Background(), true)
	script := "import sys,pathlib,time\nfrom processor_publish import publish\ntime.sleep(0.5)\np=pathlib.Path(sys.argv[2],'result.txt')\np.write_text(pathlib.Path(sys.argv[1]).read_text().upper())\npublish(p)\nprint('fixture done')\n"
	os.WriteFile(filepath.Join(app, "fixture.py"), []byte(script), 0600)
	config := cluster.Config{ExecutionMode: "queue", Routes: map[string][]string{"fixture.log": {"fixture"}}, Programs: map[string]cluster.Program{"fixture": {Entrypoint: "fixture.py", ParallelSafe: parallel, RetrySafe: retry, MaxParallel: 6}}}
	data, _ := json.Marshal(config)
	os.WriteFile(filepath.Join(app, "config.json"), data, 0600)
	c := &Controller{Store: s, AppDir: app, WatchDir: watch, Token: "fixture-controller-token-0000000000000000"}
	if err = c.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c, db
}
func acceptFixture(t *testing.T, c *Controller, id string) string {
	t.Helper()
	_, runs, err := c.Store.AcceptBatch(context.Background(), store.Batch{SourceID: "fixture", BatchID: id, Filename: "fixture.log", Generation: "g", PayloadFingerprint: id, RecordsJSON: `["alpha\n","beta\n"]`}, []string{"fixture"})
	if err != nil {
		t.Fatal(err)
	}
	return runs[0]
}
func TestSixWorkerExecutionThroughProcessorAPI(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	c, _ := newFixture(t, true, true)
	ctx := context.Background()
	ids := []string{}
	for i := 0; i < 6; i++ {
		ids = append(ids, acceptFixture(t, c, fmt.Sprint(i)))
	}
	spec, err := c.Store.PinRun(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	apiServer := &api.Server{Store: c.Store, WorkerAPI: http.HandlerFunc(c.WorkerHandler), ClusterStatus: c.Status}
	srv := httptest.NewServer(apiServer.Handler(c.Token))
	defer srv.Close()
	c.WorkerURL = srv.URL + "/v1"
	var mu sync.Mutex
	workers := []cluster.Worker{}
	runners := map[string]*cluster.Runner{}
	assigned := map[string]string{}
	failures := []error{}
	var wg sync.WaitGroup
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("worker-%d", i)
		root := t.TempDir()
		if _, err = cluster.InstallBundle(ctx, root, cluster.Install{Version: spec.Version, BundleURL: c.WorkerURL + "/bundles/" + spec.Version, Token: c.installToken(spec.Version)}); err != nil {
			t.Fatal(err)
		}
		runners[id] = &cluster.Runner{Root: root}
		workers = append(workers, cluster.Worker{ID: id, Status: "online", MaxTasks: 1, Resources: cluster.ResourceSnapshot{SampledAt: time.Now().UnixMilli(), CPUCores: 4, TotalMemoryMB: 4096, AvailableMemoryMB: 4096}, Protocol: cluster.ProtocolVersion, Versions: []string{spec.Version}})
	}
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+c.Token {
			w.WriteHeader(401)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/publish") {
			var p cluster.Publication
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if err := cluster.MergeText(c.WatchDir, p.Name, p.Lines); err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			w.Write([]byte(`{}`))
			return
		}
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(workers)
			return
		}
		var req struct {
			WorkerID   string             `json:"worker_id"`
			Assignment cluster.Assignment `json:"assignment"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		assigned[req.Assignment.RunID] = req.WorkerID
		for i := range workers {
			if workers[i].ID == req.WorkerID {
				if workers[i].CurrentLoad >= 1 {
					t.Error("worker overbooked")
				}
				workers[i].CurrentLoad++
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := runners[req.WorkerID].Run(ctx, req.Assignment)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, err)
			}
			for i := range workers {
				if workers[i].ID == req.WorkerID {
					workers[i].CurrentLoad--
				}
			}
		}()
		w.Write([]byte(`{"accepted":true}`))
	}))
	defer broker.Close()
	c.BrokerURL = broker.URL
	for i := 0; i < 6; i++ {
		if err = c.step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	if err = c.flushPublications(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(failures) > 0 {
		t.Fatalf("worker failures: %v", failures)
	}
	used := map[string]bool{}
	for _, id := range assigned {
		used[id] = true
	}
	if len(used) != 6 || len(assigned) != 6 {
		t.Fatalf("not all six used: %v", assigned)
	}
	counts, err := c.Store.RunCounts(ctx)
	if err != nil || counts["completed"] != 6 {
		t.Fatalf("counts: %v %v", counts, err)
	}
	for _, id := range ids {
		data, err := os.ReadFile(filepath.Join(c.WatchDir, "result.txt"))
		if err != nil || string(data) != "ALPHA\nBETA\n" {
			t.Fatalf("output %s: %q %v", id, data, err)
		}
	}
	runs, err := c.Store.ListRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if run.WorkerID == "" || run.AttemptID == "" || run.Phase != "completed" {
			t.Fatalf("missing run evidence: %+v", run)
		}
	}
}
func TestStagedPublicationSurvivesProcessorRestart(t *testing.T) {
	c, db := newFixture(t, true, true)
	ctx := context.Background()
	run := acceptFixture(t, c, "restart")
	c.Store.PinRun(ctx, run)
	a, err := c.Store.AllocateAttempt(ctx, run, "worker-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Store.AuthorizeAttempt(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	data := []byte("durable fixture result\n")
	name := "result_" + run + ".txt"
	writeAtomic(filepath.Join(c.root(), "attempts", a.ID, "artifacts", name), data)
	writeAtomic(filepath.Join(c.root(), "attempts", a.ID, "log"), []byte("done\n"))
	completion := cluster.Completion{Artifacts: []cluster.Artifact{{Name: name, SHA256: cluster.Digest(data), Size: int64(len(data))}}}
	body, _ := json.Marshal(completion)
	if err = c.Store.StageCompletion(ctx, a.ID, string(body)); err != nil {
		t.Fatal(err)
	}
	c.Store.Close()
	s, err := store.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	restored := &Controller{Store: s, AppDir: c.AppDir, WatchDir: c.WatchDir, Token: c.Token}
	attempts, err := s.ActiveAttempts(ctx)
	if err != nil || len(attempts) != 1 || attempts[0].Phase != "uploading" {
		t.Fatalf("staged attempt lost: %+v %v", attempts, err)
	}
	if err = restored.publish(ctx, &attempts[0]); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(filepath.Join(c.WatchDir, name))
	if err != nil || string(output) != string(data) {
		t.Fatalf("publication lost: %q %v", output, err)
	}
	// A lost HTTP acknowledgment can be recovered by repeating all uploads and
	// the exact completion, while any changed terminal data remains fenced.
	handler := http.HandlerFunc(restored.WorkerHandler)
	for _, item := range []struct{ method, path, body string }{{"PUT", "log", "done\n"}, {"PUT", "artifacts/" + name, string(data)}, {"POST", "complete", string(body)}, {"POST", "renew", ""}} {
		req := httptest.NewRequest(item.method, "/v1/attempts/"+a.ID+"/"+item.path, strings.NewReader(item.body))
		req.Header.Set("Authorization", "Bearer "+a.Token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("idempotent %s: %d %s", item.path, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest("PUT", "/v1/attempts/"+a.ID+"/log", strings.NewReader("changed"))
	req.Header.Set("Authorization", "Bearer "+a.Token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 409 {
		t.Fatalf("changed result accepted: %d", rec.Code)
	}
}

func TestPublicationRecoveryDoesNotRecreateDeliveredArtifact(t *testing.T) {
	c, _ := newFixture(t, true, true)
	ctx := context.Background()
	run := acceptFixture(t, c, "partial-publication")
	c.Store.PinRun(ctx, run)
	a, err := c.Store.AllocateAttempt(ctx, run, "worker-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Store.AuthorizeAttempt(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	completion := cluster.Completion{Artifacts: []cluster.Artifact{}}
	for _, prefix := range []string{"first", "second"} {
		name := prefix + "_" + run + ".txt"
		data := []byte(prefix + " artifact\n")
		if err = writeAtomic(filepath.Join(c.root(), "attempts", a.ID, "artifacts", name), data); err != nil {
			t.Fatal(err)
		}
		completion.Artifacts = append(completion.Artifacts, cluster.Artifact{Name: name, SHA256: cluster.Digest(data), Size: int64(len(data))})
	}
	writeAtomic(filepath.Join(c.root(), "attempts", a.ID, "log"), []byte("done\n"))
	body, _ := json.Marshal(completion)
	if err = c.Store.StageCompletion(ctx, a.ID, string(body)); err != nil {
		t.Fatal(err)
	}
	// First output was delivered and deleted by a downstream consumer before
	// publication of the second output. Recovery must not recreate it.
	first := completion.Artifacts[0]
	writeAtomic(filepath.Join(c.root(), "attempts", a.ID, "published", first.Name), []byte(first.SHA256))
	a.Completion = string(body)
	a.Phase = "uploading"
	if err = c.publish(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(c.WatchDir, first.Name)); !os.IsNotExist(err) {
		t.Fatal("delivered artifact was recreated")
	}
	second := completion.Artifacts[1]
	data, err := os.ReadFile(filepath.Join(c.WatchDir, second.Name))
	if err != nil || cluster.Digest(data) != second.SHA256 {
		t.Fatal("missing artifact was not published")
	}
}

func TestLivePublicationSurvivesMergeOutageAndToolFailure(t *testing.T) {
	c, _ := newFixture(t, true, false)
	script := `import pathlib,sys,time
from processor_publish import publish
p=pathlib.Path(sys.argv[2],'live.txt')
p.write_text('first\nunfinished')
publish(p)
time.sleep(2)
with p.open('a') as f: f.write(' complete\n')
publish(p)
raise SystemExit(7)
`
	if err := os.WriteFile(filepath.Join(c.AppDir, "fixture.py"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	var unavailable atomic.Bool
	unavailable.Store(true)
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable.Load() {
			http.Error(w, "fixture outage", 503)
			return
		}
		var p cluster.Publication
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := cluster.MergeText(c.WatchDir, p.Name, p.Lines); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer broker.Close()
	c.BrokerURL = broker.URL
	server := httptest.NewServer((&api.Server{Store: c.Store, WorkerAPI: http.HandlerFunc(c.WorkerHandler)}).Handler(c.Token))
	defer server.Close()
	c.WorkerURL = server.URL + "/v1"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	run := acceptFixture(t, c, "live")
	spec, err := c.Store.PinRun(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if _, err = cluster.InstallBundle(ctx, root, cluster.Install{Version: spec.Version, BundleURL: c.WorkerURL + "/bundles/" + spec.Version, Token: c.installToken(spec.Version)}); err != nil {
		t.Fatal(err)
	}
	a, err := c.Store.AllocateAttempt(ctx, run, "w", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- (&cluster.Runner{Root: root}).Run(ctx, cluster.Assignment{AttemptID: a.ID, RunID: run, ProgramID: "fixture", WorkerID: "w", Version: spec.Version, Token: a.Token, BaseURL: c.WorkerURL, TimeoutSeconds: -1, PublishOnly: true, Resources: cluster.Program{}.Resources()})
	}()
	deadline := time.Now().Add(time.Second)
	for {
		pending, e := c.Store.PendingOutput(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if len(pending) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no live publication before process completion")
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, _ := c.Store.Run(ctx, run)
	if current.Status != "running" {
		t.Fatalf("publication arrived only after completion: %+v", current)
	}
	if _, e := os.Stat(filepath.Join(c.WatchDir, "live.txt")); !os.IsNotExist(e) {
		t.Fatal("outage fixture unexpectedly rendered")
	}
	c.resultMu.Lock()
	outageErr := c.flushPublications(ctx)
	c.resultMu.Unlock()
	if outageErr == nil {
		t.Fatal("merge outage was not exercised")
	}
	unavailable.Store(false)
	c.resultMu.Lock()
	err = c.flushPublications(ctx)
	c.resultMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(c.WatchDir, "live.txt"))
	if err != nil || string(data) != "first\n" {
		t.Fatalf("partial line published: %q %v", data, err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = c.flushPublications(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ = c.Store.Run(ctx, run)
	if current.Status != "failed" {
		t.Fatalf("exit status lost: %+v", current)
	}
	data, err = os.ReadFile(filepath.Join(c.WatchDir, "live.txt"))
	if err != nil || string(data) != "first\nunfinished complete\n" {
		t.Fatalf("failure removed partial output: %q %v", data, err)
	}
}

func TestRenewalDoesNotWaitForPublicationLock(t *testing.T) {
	c, _ := newFixture(t, true, false)
	ctx := context.Background()
	run := acceptFixture(t, c, "renewal")
	a, err := c.Store.AllocateAttempt(ctx, run, "w", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Store.AuthorizeAttempt(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/v1/attempts/"+a.ID+"/renew", nil)
	request.Header.Set("Authorization", "Bearer "+a.Token)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	c.resultMu.Lock()
	go func() { c.WorkerHandler(response, request); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		c.resultMu.Unlock()
		t.Fatal("publication transfer blocked execution heartbeat")
	}
	c.resultMu.Unlock()
	if response.Code != 200 {
		t.Fatalf("renewal failed: %s", response.Body.String())
	}
}

func TestConcurrentStatusUsesImmutableWorkerSnapshot(t *testing.T) {
	c, _ := newFixture(t, true, false)
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]cluster.Worker{{ID: "busy", MaxTasks: 2, CurrentLoad: 1}, {ID: "idle", MaxTasks: 2}})
	}))
	defer broker.Close()
	c.BrokerURL = broker.URL
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			response := httptest.NewRecorder()
			c.Status(response, httptest.NewRequest("GET", "/v1/cluster", nil))
			if response.Code != 200 {
				t.Errorf("status failed: %d", response.Code)
			}
		}
	}()
	for i := 0; i < 100; i++ {
		if err := c.step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
}
