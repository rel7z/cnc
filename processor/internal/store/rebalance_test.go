package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestRebalancePendingSharesPreservesActiveInputVersionAndCursor(t *testing.T) {
	s, _ := distributedFixture(t, false, 10)
	ctx := context.Background()
	var ids []string
	for i := 0; i < 24; i++ {
		raw, _ := json.Marshal([]string{fmt.Sprintf("dummy-%d\n", i)})
		_, runs, e := s.AcceptBatch(ctx, Batch{SourceID: "dummy", BatchID: fmt.Sprint(i), Filename: "dummy.txt", Generation: "g", PayloadFingerprint: fmt.Sprint(i), RecordsJSON: string(raw)}, []string{"fixture"})
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, runs[0])
	}
	active, e := s.AllocateAttempt(ctx, ids[0], "a", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.AuthorizeAttempt(ctx, active.ID); e != nil {
		t.Fatal(e)
	}
	s.RegisterProgram(ctx, "fixture", ProgramSpec{Version: "new-version", Limit: 3, RetrySafe: true})
	if _, e = s.RebalanceQueuedFile(ctx, "dummy.txt", []string{"a", "b", "c"}); e == nil {
		t.Fatal("rebalanced while dispatch enabled")
	}
	s.SetEnabled(ctx, false)
	r, e := s.RebalanceQueuedFile(ctx, "dummy.txt", []string{"a", "b", "c"})
	if e != nil {
		t.Fatal(e)
	}
	if r.ReplacedRuns != 23 || r.NewRuns != 3 || r.Records != 23 {
		t.Fatalf("result=%+v", r)
	}
	a, _ := s.Attempt(ctx, active.ID)
	if a.Phase != "running" {
		t.Fatal("active attempt was changed")
	}
	runs, _ := s.ListRuns(ctx)
	counts := map[string]int{}
	seen := map[string]bool{}
	for _, run := range runs {
		name, w, n, e := s.AssignmentInfo(ctx, run.ID)
		if e != nil {
			t.Fatal(e)
		}
		if name != "dummy.txt" || w == "" {
			continue
		}
		counts[w] += n
		spec, e := s.PinRun(ctx, run.ID)
		if e != nil || spec.Version != "version-one" || spec.Limit != 10 || spec.RetrySafe {
			t.Fatalf("pinned settings changed: %+v %v", spec, e)
		}
		raw, _ := s.GetBatchRecords(ctx, run.BatchAcceptanceID)
		var records []string
		json.Unmarshal([]byte(raw), &records)
		for _, line := range records {
			if seen[line] {
				t.Fatal("record duplicated")
			}
			seen[line] = true
		}
	}
	if fmt.Sprint(counts) != fmt.Sprint(map[string]int{"a": 8, "b": 8, "c": 7}) || len(seen) != 23 || seen["dummy-0\n"] {
		t.Fatalf("shares=%v records=%d", counts, len(seen))
	}
	again, e := s.RebalanceQueuedFile(ctx, "dummy.txt", []string{"a", "b", "c"})
	if e != nil || again.NewRuns != 0 || again.ReplacedRuns != 0 {
		t.Fatalf("not idempotent: %+v %v", again, e)
	}
}
