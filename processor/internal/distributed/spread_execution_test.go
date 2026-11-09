package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user/cnc-processor/api"
	"github.com/user/cnc-processor/cluster"
	"github.com/user/cnc-processor/internal/watcher"
)

func TestSpreadBackgroundSummaryEndToEnd(t *testing.T) {
	c, _ := newFixture(t, true, false)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	script := "import pathlib,sys,time\nlines=pathlib.Path(sys.argv[1]).read_text().splitlines()\ntime.sleep(0.2)\nprint(f'processed = {len(lines)}/{len(lines)}',flush=True)\n"
	if e := os.WriteFile(filepath.Join(c.AppDir, "fixture.py"), []byte(script), 0600); e != nil {
		t.Fatal(e)
	}
	if e := c.Prepare(ctx); e != nil {
		t.Fatal(e)
	}
	c.config.FileProcessing = map[string]cluster.FileProcessingPolicy{"fixture.log": {Distribution: "spread", RequireSummary: true}}
	spec, e := c.Store.CurrentProgram(ctx, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer((&api.Server{Store: c.Store, WorkerAPI: http.HandlerFunc(c.WorkerHandler)}).Handler(c.Token))
	defer server.Close()
	c.WorkerURL = server.URL + "/v1"
	var mu sync.Mutex
	var wg sync.WaitGroup
	var failures []error
	var workers []cluster.Worker
	runners := map[string]*cluster.Runner{}
	assigned := map[string]string{}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("dummy-%d", i)
		root := t.TempDir()
		if _, e := cluster.InstallBundle(ctx, root, cluster.Install{Version: spec.Version, BundleURL: c.WorkerURL + "/bundles/" + spec.Version, Token: c.installToken(spec.Version)}); e != nil {
			t.Fatal(e)
		}
		runners[id] = &cluster.Runner{Root: root}
		worker := balanceWorker(id, spec.Version, 10)
		worker.Resources.Cgroups = cluster.CgroupAvailable()
		workers = append(workers, worker)
	}
	c.workers = append([]cluster.Worker(nil), workers...)
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "GET" {
			for i := range workers {
				workers[i].Resources.SampledAt = time.Now().UnixMilli()
			}
			json.NewEncoder(w).Encode(workers)
			return
		}
		var req struct {
			WorkerID   string             `json:"worker_id"`
			Assignment cluster.Assignment `json:"assignment"`
		}
		if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		if _, ok := runners[req.WorkerID]; !ok {
			t.Error("unknown worker")
			w.WriteHeader(400)
			return
		}
		assigned[req.Assignment.RunID] = req.WorkerID
		for i := range workers {
			if workers[i].ID == req.WorkerID {
				if workers[i].ProcessorLoad != 0 {
					t.Error("more than one share on worker")
				}
				workers[i].CurrentLoad++
				workers[i].ProcessorLoad++
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := runners[req.WorkerID].Run(ctx, req.Assignment)
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				failures = append(failures, e)
			}
			for i := range workers {
				if workers[i].ID == req.WorkerID {
					workers[i].CurrentLoad--
					workers[i].ProcessorLoad--
				}
			}
		}()
		w.Write([]byte(`{"accepted":true}`))
	}))
	defer broker.Close()
	c.BrokerURL = broker.URL
	var content strings.Builder
	for i := 0; i < 23; i++ {
		fmt.Fprintf(&content, "dummy-record-%d\n", i)
	}
	if e := os.WriteFile(filepath.Join(c.WatchDir, "fixture.log"), []byte(content.String()), 0600); e != nil {
		t.Fatal(e)
	}
	capture := watcher.Capturer{Store: c.Store, Dir: c.WatchDir, Routes: map[string][]string{"fixture.log": {"fixture"}}, SpreadWorkers: c.CaptureSpreadWorkers}
	if e := capture.Capture(ctx, "fixture.log"); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		if e := c.step(ctx); e != nil {
			t.Fatal(e)
		}
	}
	wg.Wait()
	mu.Lock()
	if len(failures) > 0 {
		t.Errorf("worker errors: %v", failures)
	}
	mu.Unlock()
	counts, _ := c.Store.RunCounts(ctx)
	if counts["completed"] != 3 {
		t.Fatalf("counts=%v", counts)
	}
	runs, _ := c.Store.ListRuns(ctx)
	for _, r := range runs {
		_, preferred, n, e := c.Store.AssignmentInfo(ctx, r.ID)
		if e != nil {
			t.Fatal(e)
		}
		if assigned[r.ID] != preferred {
			t.Fatal("share assigned to wrong worker")
		}
		req, _ := http.NewRequest("GET", server.URL+"/v1/runs/"+r.ID+"/log", nil)
		req.Header.Set("Authorization", "Bearer "+c.Token)
		response, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(response.Body)
		response.Body.Close()
		if e != nil || response.StatusCode != 200 || string(body) != fmt.Sprintf("processed = %d/%d\n", n, n) {
			t.Fatalf("saved summary=%q status=%d err=%v", body, response.StatusCode, e)
		}
	}
	t.Log("VERIFIED: 23 dummy records spread 8/8/7, one background process per worker, matching summaries saved before completed")
}
