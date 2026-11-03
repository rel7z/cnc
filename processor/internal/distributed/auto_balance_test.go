package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user/cnc-processor/cluster"
	"github.com/user/cnc-processor/internal/store"
)

func balancePolicy() cluster.AutoBalancePolicy {
	return cluster.AutoBalancePolicy{Enabled: true, PauseCPUPercent: 75, EmergencyCPUPercent: 90, RecoverCPUPercent: 85, CooldownSeconds: 10}
}

func balanceFixture(t *testing.T, enabled bool, explicitLimit int) (*Controller, string) {
	t.Helper()
	app := t.TempDir()
	db := filepath.Join(app, "dummy.db")
	s, err := store.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "dummy.py"), []byte("# inert fixture: never executed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := balancePolicy()
	p.Enabled = enabled
	config := cluster.Config{ExecutionMode: "queue", AutoBalance: p, Routes: map[string][]string{"fixture.log": {"fixture"}}, Programs: map[string]cluster.Program{"fixture": {Entrypoint: "dummy.py", ParallelSafe: true, MaxParallel: explicitLimit}}}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(filepath.Join(app, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	c := &Controller{Store: s, AppDir: app, WatchDir: t.TempDir(), Token: "dummy-test-management-secret-only"}
	if err := c.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c, db
}

func balanceWorker(id, version string, cpu float64) cluster.Worker {
	return cluster.Worker{ID: id, Status: "online", Protocol: cluster.ProtocolVersion, Versions: []string{version}, MaxTasks: 4, Resources: cluster.ResourceSnapshot{CPUCores: 4, CPUPercent: cpu, Cgroups: true, TotalMemoryMB: 8192, AvailableMemoryMB: 7000, SampledAt: time.Now().UnixMilli()}}
}

func TestAutoBalanceRemovesDefaultSixAndTwoLimits(t *testing.T) {
	for _, scenario := range []struct {
		name           string
		enabled        bool
		explicit, want int
	}{{"adaptive", true, 0, 10}, {"legacy", false, 0, 6}, {"explicit-cap", true, 2, 2}} {
		t.Run(scenario.name, func(t *testing.T) {
			c, _ := balanceFixture(t, scenario.enabled, scenario.explicit)
			ctx := context.Background()
			for i := 0; i < 10; i++ {
				acceptFixture(t, c, fmt.Sprint(i))
			}
			spec, err := c.Store.CurrentProgram(ctx, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			workers := []cluster.Worker{balanceWorker("a", spec.Version, 10), balanceWorker("b", spec.Version, 10), balanceWorker("c", spec.Version, 10)}
			var mu sync.Mutex
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
					WorkerID   string              `json:"worker_id"`
					Assignment *cluster.Assignment `json:"assignment"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Assignment == nil {
					t.Errorf("unexpected request: %v", err)
					http.Error(w, "bad fixture request", 400)
					return
				}
				for i := range workers {
					if workers[i].ID == req.WorkerID {
						workers[i].CurrentLoad++
						workers[i].ProcessorLoad++
					}
				}
				if err := c.Store.AuthorizeAttempt(ctx, req.Assignment.AttemptID); err != nil {
					t.Error(err)
				}
				w.Write([]byte(`{"accepted":true}`))
			}))
			defer broker.Close()
			c.BrokerURL = broker.URL
			for i := 0; i < 12; i++ {
				if err := c.step(ctx); err != nil {
					t.Fatal(err)
				}
			}
			counts, err := c.Store.RunCounts(ctx)
			if err != nil || counts["running"] != scenario.want {
				t.Fatalf("counts %v: %v", counts, err)
			}
			if scenario.enabled && scenario.explicit == 0 {
				mu.Lock()
				defer mu.Unlock()
				for _, w := range workers {
					if w.CurrentLoad < 3 || w.CurrentLoad > 4 {
						t.Fatalf("unbalanced: %+v", workers)
					}
				}
				if spec.Limit != 6 {
					t.Fatal("pinned program was changed")
				}
			}
		})
	}
}

func startBalanceAttempt(t *testing.T, c *Controller, worker, key string) *store.Attempt {
	t.Helper()
	ctx := context.Background()
	id := acceptFixture(t, c, key)
	if _, err := c.Store.PinRun(ctx, id); err != nil {
		t.Fatal(err)
	}
	a, err := c.Store.AllocateAttempt(ctx, id, worker, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Store.AuthorizeAttempt(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	a, err = c.Store.Attempt(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAutoBalanceEmergencyIsGradualAndFencesOnlyProcessorLeases(t *testing.T) {
	c, _ := balanceFixture(t, true, 0)
	ctx := context.Background()
	a := startBalanceAttempt(t, c, "hot", "a")
	b := startBalanceAttempt(t, c, "hot", "b")
	other := startBalanceAttempt(t, c, "cool", "other")
	upload := startBalanceAttempt(t, c, "hot", "upload")
	if err := c.Store.StageCompletion(ctx, upload.ID, `{}`); err != nil {
		t.Fatal(err)
	}
	w := balanceWorker("hot", a.Version, 91)
	check := func(cpu float64) {
		t.Helper()
		w.Resources.CPUPercent = cpu
		w.Resources.SampledAt = time.Now().UnixMilli()
		active, _ := c.Store.ActiveAttempts(ctx)
		if err := c.balanceCPU(ctx, []cluster.Worker{w}, active); err != nil {
			t.Fatal(err)
		}
	}
	check(91)
	counts, _ := c.Store.RunCounts(ctx)
	if counts["interrupted"] != 1 {
		t.Fatalf("expected one interrupted: %v", counts)
	}
	latest, _ := c.Store.Attempt(ctx, b.ID)
	if latest.Phase != "interrupted" || !strings.Contains(latest.Error, "CPU protection") {
		t.Fatalf("wrong victim: %+v", latest)
	}
	if err := c.Store.RenewAttempt(ctx, b.ID, time.Minute); err == nil {
		t.Fatal("interrupted attempt still has an execution lease")
	}
	check(99)
	counts, _ = c.Store.RunCounts(ctx)
	if counts["interrupted"] != 1 {
		t.Fatal("shed again without waiting ten seconds")
	}
	for _, id := range []string{other.ID, upload.ID} {
		r, _ := c.Store.Attempt(ctx, id)
		if r.Phase == "interrupted" {
			t.Fatal("unrelated/staged work was interrupted")
		}
	}
	c.mu.Lock()
	state := c.cpuEmergency["hot"]
	if time.Until(state.NextCheck) < 9*time.Second {
		t.Fatal("cooldown shorter than ten seconds")
	}
	state.NextCheck = time.Now().Add(-time.Second)
	c.cpuEmergency["hot"] = state
	c.mu.Unlock()
	check(87)
	first, _ := c.Store.Attempt(ctx, a.ID)
	if first.Phase != "interrupted" {
		t.Fatal("emergency recovery stopped before 85 percent")
	}
	c.mu.Lock()
	state = c.cpuEmergency["hot"]
	state.NextCheck = time.Now().Add(-time.Second)
	c.cpuEmergency["hot"] = state
	c.mu.Unlock()
	check(85)
	c.mu.Lock()
	_, held := c.cpuEmergency["hot"]
	c.mu.Unlock()
	if held {
		t.Fatal("emergency did not clear at 85 percent")
	}
	w.Resources.CPUPercent = 74.9
	if reason := c.cpuAdmissionReason(w); reason != "" {
		t.Fatal(reason)
	}
	w.Resources.CPUPercent = 75
	if reason := c.cpuAdmissionReason(w); reason == "" {
		t.Fatal("admitted work at the hold threshold")
	}
}

func TestAutoBalanceStaleTelemetryDoesNotKillWork(t *testing.T) {
	c, _ := balanceFixture(t, true, 0)
	a := startBalanceAttempt(t, c, "hot", "one")
	ctx := context.Background()
	w := balanceWorker("hot", a.Version, 99)
	w.Resources.SampledAt = time.Now().Add(-20 * time.Second).UnixMilli()
	active, _ := c.Store.ActiveAttempts(ctx)
	if err := c.balanceCPU(ctx, []cluster.Worker{w}, active); err != nil {
		t.Fatal(err)
	}
	r, _ := c.Store.Attempt(ctx, a.ID)
	if r.Phase != "running" {
		t.Fatal("killed using stale telemetry")
	}
	if c.cpuAdmissionReason(w) == "" {
		t.Fatal("stale telemetry admitted work")
	}
}

func TestAutoBalancePolicyValidation(t *testing.T) {
	p := balancePolicy()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.RecoverCPUPercent = 95
	if p.Validate() == nil {
		t.Fatal("invalid threshold order accepted")
	}
	p = balancePolicy()
	p.CooldownSeconds = 9
	if p.Validate() == nil {
		t.Fatal("short cooldown accepted")
	}
}
