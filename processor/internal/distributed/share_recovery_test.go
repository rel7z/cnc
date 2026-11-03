package distributed

import (
	"context"
	"testing"
	"time"

	"github.com/user/cnc-processor/cluster"
	"github.com/user/cnc-processor/internal/store"
)

func TestUnavailableWorkerSharesRecoverAfterGrace(t *testing.T) {
	c, _ := newFixture(t, true, false)
	ctx := context.Background()
	_, runs, err := c.Store.AcceptBatch(ctx, store.Batch{SourceID: "recovery", BatchID: "one", Filename: "fixture.log", Generation: "g", PayloadFingerprint: "one", RecordsJSON: `["kept\n"]`, PreferredWorker: "gone"}, []string{"fixture"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	w := []cluster.Worker{balanceWorker("ready", "", 10)}
	if err = c.recoverWorkerShares(ctx, w, now); err != nil {
		t.Fatal(err)
	}
	_, worker, _, _ := c.Store.AssignmentInfo(ctx, runs[0])
	if worker != "gone" {
		t.Fatal("temporary outage lost affinity")
	}
	if err = c.recoverWorkerShares(ctx, w, now.Add(shareRecoveryGrace)); err != nil {
		t.Fatal(err)
	}
	_, worker, records, _ := c.Store.AssignmentInfo(ctx, runs[0])
	if worker != "ready" || records != 1 {
		t.Fatalf("share not recovered: worker=%s records=%d", worker, records)
	}
}
