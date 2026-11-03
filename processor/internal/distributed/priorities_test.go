package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/user/cnc-processor/cluster"
	"github.com/user/cnc-processor/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRankedAdmissionRejectsOverloadAndLetsOtherToolsProgress(t *testing.T) {
	c, _ := newFixture(t, true, true)
	ctx := context.Background()
	first := acceptFixture(t, c, "seed")
	spec, _ := c.Store.PinRun(ctx, first)
	if err := c.Store.RegisterProgram(ctx, "lower", store.ProgramSpec{Version: spec.Version, Limit: 6}); err != nil {
		t.Fatal(err)
	}
	if err := c.Store.SetConfiguredRanks(ctx, map[string]int{"fixture": 5, "lower": 1}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 120; i++ {
		acceptFixture(t, c, fmt.Sprint(i))
		_, _, err := c.Store.AcceptBatch(ctx, store.Batch{SourceID: "lower", BatchID: fmt.Sprint(i), Filename: "other.txt", RecordsJSON: `["x\n"]`}, []string{"lower"})
		if err != nil {
			t.Fatal(err)
		}
	}
	cpu := 90.0
	reject := false
	starts := []string{}
	workers := []cluster.Worker{{ID: "worker", Status: "online", MaxTasks: 2, Protocol: cluster.ProtocolVersion, Versions: []string{spec.Version}, Resources: cluster.ResourceSnapshot{SampledAt: time.Now().UnixMilli(), CPUCores: 4, TotalMemoryMB: 4096, AvailableMemoryMB: 4096}}}
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			workers[0].Resources.CPUPercent = cpu
			workers[0].Resources.SampledAt = time.Now().UnixMilli()
			json.NewEncoder(w).Encode(workers)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/publish") {
			w.Write([]byte(`{}`))
			return
		}
		if reject {
			w.WriteHeader(409)
			return
		}
		var req struct {
			Assignment cluster.Assignment `json:"assignment"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		starts = append(starts, req.Assignment.ProgramID)
		w.Write([]byte(`{"accepted":true}`))
	}))
	defer broker.Close()
	c.BrokerURL = broker.URL
	if err := c.step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(starts) != 0 {
		t.Fatal("overload admitted a job")
	}
	cpu = 0
	reject = true
	if err := c.step(ctx); err != nil {
		t.Fatal(err)
	}
	reject = false
	// This test covers ranking after capacity recovers; simulate the end of
	// the rejection cooldown without sleeping or changing production timing.
	c.mu.Lock()
	delete(c.workerRetry, "worker")
	c.mu.Unlock()
	for i := 0; i < 12; i++ {
		if err := c.step(ctx); err != nil {
			t.Fatal(err)
		}
		active, _ := c.Store.ActiveAttempts(ctx)
		for _, a := range active {
			if err := c.Store.FinishAttempt(ctx, a.ID, "completed", "", "", ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	want := []string{"fixture", "fixture", "fixture", "fixture", "fixture", "lower", "fixture", "fixture", "fixture", "fixture", "fixture", "lower"}
	if fmt.Sprint(starts) != fmt.Sprint(want) {
		t.Fatalf("weighted dispatch after overload/rejection: %v", starts)
	}
	// High rank at its concurrency limit must not block the lower tool.
	for i := 0; i < 6; i++ {
		runs, _ := c.Store.QueuedRuns(ctx)
		for _, run := range runs {
			if run.ProgramID == "fixture" {
				c.Store.PinRun(ctx, run.ID)
				if _, err := c.Store.AllocateAttempt(ctx, run.ID, "absent-worker", time.Minute); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
	}
	if err := c.step(ctx); err != nil {
		t.Fatal(err)
	}
	if starts[len(starts)-1] != "lower" {
		t.Fatal("concurrency-blocked high rank starved lower rank")
	}
}
