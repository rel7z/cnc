package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSpreadAtomicCursorAndWorkerAffinity(t *testing.T) {
	s, _ := distributedFixture(t, false, 10)
	ctx := context.Background()
	batches := []Batch{{SourceID: "capture-spread", BatchID: "a", Filename: "dummy.txt", Generation: "g", PayloadFingerprint: "a", RecordsJSON: `["a\n"]`, PreferredWorker: "a"}, {SourceID: "capture-spread", BatchID: "b", Filename: "dummy.txt", Generation: "g", PayloadFingerprint: "b", RecordsJSON: `["b\n"]`, PreferredWorker: "b"}}
	cursor := CaptureCursor{Filename: "dummy.txt", Identity: "i", Generation: "g", Offset: 4, PrefixHash: "h"}
	if _, e := s.db.Exec(`CREATE TRIGGER reject_spread_cursor BEFORE INSERT ON capture_cursors BEGIN SELECT RAISE(FAIL,'fixture disk failure'); END`); e != nil {
		t.Fatal(e)
	}
	if e := s.AcceptSpreadCaptured(ctx, batches, []string{"fixture"}, cursor); e == nil {
		t.Fatal("expected rollback")
	}
	count, _ := s.SpreadRecordCount(ctx, "dummy.txt", "g")
	if count != 0 {
		t.Fatal("partial shares committed")
	}
	s.db.Exec(`DROP TRIGGER reject_spread_cursor`)
	if e := s.AcceptSpreadCaptured(ctx, batches, []string{"fixture"}, cursor); e != nil {
		t.Fatal(e)
	}
	runs, _ := s.ListRuns(ctx)
	for _, r := range runs {
		name, w, _, e := s.AssignmentInfo(ctx, r.ID)
		if e != nil {
			t.Fatal(e)
		}
		if name != "dummy.txt" {
			continue
		}
		if _, e = s.AllocateAttempt(ctx, r.ID, "wrong", time.Minute); !errors.Is(e, ErrUnavailable) {
			t.Fatal("wrong worker admitted")
		}
		if _, e = s.AllocateAttempt(ctx, r.ID, w, time.Minute); e != nil {
			t.Fatal(e)
		}
	}
	cur, _ := s.CaptureCursor(ctx, "dummy.txt")
	if cur.Offset != 4 {
		t.Fatal("cursor not committed")
	}
}
