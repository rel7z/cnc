package distributed

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user/cnc-processor/cluster"
)

func TestCompletionRequiresSavedMatchingSummary(t *testing.T) {
	for _, tt := range []struct {
		name, log, want string
		exit            int
	}{
		{"missing", "", "failed", 0}, {"wrong-size", "processed = 1/99\n", "failed", 0}, {"overflow", "processed = 3/2\n", "failed", 0}, {"valid-filtered", "processed = 1/2\n", "completed", 0}, {"nonzero-exit", "processed = 2/2\n", "failed", 7},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newFixture(t, true, false)
			ctx := context.Background()
			id := acceptFixture(t, c, "summary")
			c.config.FileProcessing = map[string]cluster.FileProcessingPolicy{"fixture.log": {RequireSummary: true}}
			a, e := c.Store.AllocateAttempt(ctx, id, "worker", time.Minute)
			if e != nil {
				t.Fatal(e)
			}
			if e = c.Store.AuthorizeAttempt(ctx, a.ID); e != nil {
				t.Fatal(e)
			}
			if e = writeAtomic(filepath.Join(c.root(), "attempts", a.ID, "log"), []byte(tt.log)); e != nil {
				t.Fatal(e)
			}
			body, _ := json.Marshal(cluster.Completion{ExitCode: tt.exit})
			if e = c.Store.StageCompletion(ctx, a.ID, string(body)); e != nil {
				t.Fatal(e)
			}
			a, _ = c.Store.Attempt(ctx, a.ID)
			if e = c.publish(ctx, a); e != nil {
				t.Fatal(e)
			}
			run, _ := c.Store.Run(ctx, id)
			if run.Status != tt.want {
				t.Fatalf("status=%s want=%s", run.Status, tt.want)
			}
			attempt, _ := c.Store.Attempt(ctx, a.ID)
			if tt.want == "failed" && tt.exit == 0 && !strings.Contains(attempt.Error, "summary") {
				t.Fatalf("reason missing: %q", attempt.Error)
			}
		})
	}
}

func TestCPUHoldDoesNotInterruptBackgroundWork(t *testing.T) {
	c, _ := balanceFixture(t, true, 0)
	no := false
	c.config.AutoBalance.InterruptRunning = &no
	a := startBalanceAttempt(t, c, "hot", "background")
	ctx := context.Background()
	w := balanceWorker("hot", a.Version, 99)
	active, _ := c.Store.ActiveAttempts(ctx)
	for i := 0; i < 3; i++ {
		if e := c.balanceCPU(ctx, []cluster.Worker{w}, active); e != nil {
			t.Fatal(e)
		}
	}
	got, _ := c.Store.Attempt(ctx, a.ID)
	if got.Phase != "running" {
		t.Fatal("CPU hold interrupted background attempt")
	}
	if e := c.Store.RenewAttempt(ctx, a.ID, time.Minute); e != nil {
		t.Fatal(e)
	}
	if c.cpuAdmissionReason(w) == "" {
		t.Fatal("high CPU admitted new work")
	}
}
