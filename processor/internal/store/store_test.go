package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRecoveryPreservesCompletedSibling(t *testing.T) {
	ctx := context.Background()
	s, err := NewStore(filepath.Join(t.TempDir(), "processor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	batch := Batch{SourceID: "fixture", BatchID: "one", Filename: "test.log", Generation: "1", PayloadFingerprint: "fixture", RecordsJSON: `["test\n"]`}
	id, runs, err := s.AcceptBatch(ctx, batch, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateRunStatus(ctx, runs[0], "completed", "", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateRunStatus(ctx, runs[1], "running", "", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	counts, err := s.RunCounts(ctx)
	if err != nil || counts["completed"] != 1 || counts["interrupted"] != 1 {
		t.Fatalf("counts=%v err=%v", counts, err)
	}
	duplicate, again, err := s.AcceptBatch(ctx, batch, []string{"a", "b"})
	if err != nil || duplicate != id || len(again) != 2 {
		t.Fatalf("duplicate=%s runs=%v err=%v", duplicate, again, err)
	}
}

func TestCapturedBatchAndCursorRollbackTogether(t *testing.T) {
	ctx := context.Background()
	s, err := NewStore(filepath.Join(t.TempDir(), "processor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("CREATE TRIGGER reject_cursor BEFORE INSERT ON capture_cursors BEGIN SELECT RAISE(FAIL,'fixture disk failure'); END"); err != nil {
		t.Fatal(err)
	}
	err = s.AcceptCaptured(ctx, Batch{SourceID: "fixture", BatchID: "one", Filename: "input.txt", Generation: "g", PayloadFingerprint: "h", RecordsJSON: `["line\n"]`}, []string{"fixture"}, CaptureCursor{Filename: "input.txt", Identity: "i", Generation: "g", Offset: 5, PrefixHash: "h"})
	if err == nil {
		t.Fatal("expected transaction failure")
	}
	counts, err := s.RunCounts(ctx)
	if err != nil || counts["queued"] != 0 {
		t.Fatalf("partial jobs committed: %v %v", counts, err)
	}
	var batches int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM batches").Scan(&batches); err != nil || batches != 0 {
		t.Fatalf("partial batch committed: %d %v", batches, err)
	}
	cursor, err := s.CaptureCursor(ctx, "input.txt")
	if err != nil || cursor.Offset != 0 {
		t.Fatalf("cursor advanced: %+v %v", cursor, err)
	}
}
