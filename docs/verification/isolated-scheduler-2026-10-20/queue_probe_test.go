package distributed

// Diagnostic tests only. A Go overlay attaches this file to the scheduler's
// package. No worker runner, bundle builder, program or production API is used.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user/cnc-processor/cluster"
	"github.com/user/cnc-processor/internal/store"
)

func isolatedProbe(t *testing.T, enabled, rejectFirst bool) (*Controller, func() []string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.NewStore(filepath.Join(t.TempDir(), "dummy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled(ctx, enabled); err != nil {
		t.Fatal(err)
	}
	version := strings.Repeat("a", 64)
	if err := s.RegisterProgram(ctx, "dummy", store.ProgramSpec{Version: version, Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterSettings(ctx, version, cluster.Program{CPUCores: 1, MemoryMB: 512}); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.AcceptBatch(ctx, store.Batch{SourceID: "isolated", BatchID: "one", Filename: "dummy.txt", Generation: "dummy", PayloadFingerprint: "dummy", RecordsJSON: `["DUMMY RECORD\n"]`}, []string{"dummy"})
	if err != nil {
		t.Fatal(err)
	}
	c := &Controller{Store: s, AppDir: t.TempDir(), WatchDir: t.TempDir(), Token: "local-dummy-token-not-a-production-secret"}
	var mu sync.Mutex
	var assignments []string
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+c.Token {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/processor/cluster/workers" {
			workers := []cluster.Worker{}
			for _, id := range []string{"worker-a", "worker-b"} {
				workers = append(workers, cluster.Worker{ID: id, Status: "online", MaxTasks: 2, Protocol: cluster.ProtocolVersion, Versions: []string{version}, Resources: cluster.ResourceSnapshot{SampledAt: time.Now().UnixMilli(), CPUCores: 2, TotalMemoryMB: 4096, AvailableMemoryMB: 4096, Cgroups: true}})
			}
			json.NewEncoder(w).Encode(workers)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/api/processor/cluster/assign" {
			t.Errorf("unexpected endpoint: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}
		var request struct {
			WorkerID   string              `json:"worker_id"`
			Assignment *cluster.Assignment `json:"assignment"`
			Install    *cluster.Install    `json:"install"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		if request.Install != nil || request.Assignment == nil {
			t.Error("probe must never install a bundle")
			http.Error(w, "forbidden", 403)
			return
		}
		if request.Assignment.ProgramID != "dummy" {
			t.Error("non-dummy program refused")
			http.Error(w, "forbidden", 403)
			return
		}
		mu.Lock()
		assignments = append(assignments, request.WorkerID)
		mu.Unlock()
		if rejectFirst && request.WorkerID == "worker-a" {
			http.Error(w, "waiting for shared job memory budget", 409)
			return
		}
		// Acknowledge only; deliberately do not instantiate a runner or execute.
		w.Write([]byte(`{"accepted":true}`))
	}))
	t.Cleanup(broker.Close)
	c.BrokerURL = broker.URL
	return c, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), assignments...) }
}

func TestIsolated409Failover(t *testing.T) {
	c, assignments := isolatedProbe(t, true, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.step(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := c.Store.ListRuns(ctx)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs: %v, %v", runs, err)
	}
	if runs[0].Phase != "rejected" || !strings.Contains(runs[0].Error, "waiting for shared job memory budget") || !strings.Contains(runs[0].BlockedReason, "worker-a") {
		t.Fatalf("specific rejection reason not retained: %+v", runs[0])
	}
	for i := 0; i < 2; i++ {
		if err := c.step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got := assignments()
	if !reflect.DeepEqual(got, []string{"worker-a", "worker-b"}) {
		t.Fatalf("healthy worker not selected after rejection: %v", got)
	}
	counts, err := c.Store.RunCounts(ctx)
	if err != nil || counts["queued"] != 0 || counts["running"] != 1 {
		t.Fatalf("counts: %v, %v", counts, err)
	}
	t.Log("VERIFIED: rejected worker-a enters cooldown; worker-b accepts next step; specific HTTP reason retained; no program executed")
}

func TestIsolatedAcceptControl(t *testing.T) {
	c, assignments := isolatedProbe(t, true, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.step(ctx); err != nil {
		t.Fatal(err)
	}
	counts, err := c.Store.RunCounts(ctx)
	if err != nil || counts["running"] != 1 || len(assignments()) != 1 {
		t.Fatalf("control failed: %v, %v, %v", counts, assignments(), err)
	}
	active, err := c.Store.ActiveAttempts(ctx)
	if err != nil || len(active) != 1 || active[0].Phase != "assigned" {
		t.Fatalf("expected allocation without execution: %v, %v", active, err)
	}
	t.Log("CONTROL: broker acknowledgment marks run running while attempt is only assigned; no program execution occurred")
}

func TestIsolatedPausedControl(t *testing.T) {
	c, assignments := isolatedProbe(t, false, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.step(ctx); err != nil {
		t.Fatal(err)
	}
	counts, err := c.Store.RunCounts(ctx)
	if err != nil || counts["queued"] != 1 || len(assignments()) != 0 {
		t.Fatalf("paused control failed: %v, %v, %v", counts, assignments(), err)
	}
	t.Log("CONTROL: paused scheduler retains queued dummy and sends no assignment")
}
