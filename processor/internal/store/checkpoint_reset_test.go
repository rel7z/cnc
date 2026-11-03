package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestCheckpointResetPreservesHistoryAndBacksUpCursors(t *testing.T) {
	ctx := context.Background()
	s, err := NewStore(filepath.Join(t.TempDir(), "processor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	cursor := CaptureCursor{Filename: "fixture.txt", Identity: "inode", Generation: "old", Offset: 8, PrefixHash: "hash"}
	batch := Batch{SourceID: "fixture", BatchID: "old", Filename: "fixture.txt", Generation: "old", PayloadFingerprint: "hash", RecordsJSON: `["fixture\n"]`}
	if err = s.AcceptCaptured(ctx, batch, []string{"dummy"}, cursor); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResetCaptureCheckpoints(ctx); !errors.Is(err, ErrResetBusy) {
		t.Fatalf("queued input must block reset: %v", err)
	}
	runs, _ := s.ListRuns(ctx)
	if err = s.UpdateRunStatus(ctx, runs[0].ID, "completed", "", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResetCaptureCheckpoints(ctx); !errors.Is(err, ErrResetBusy) {
		t.Fatalf("enabled processor must block reset: %v", err)
	}
	s.SetEnabled(ctx, false)
	reset, err := s.ResetCaptureCheckpoints(ctx)
	if err != nil || reset.Count != 1 {
		t.Fatalf("reset=%+v err=%v", reset, err)
	}
	got, err := s.CaptureCursor(ctx, "fixture.txt")
	if err != nil || got.Offset != 0 || got.Generation != "" {
		t.Fatalf("cursor=%+v err=%v", got, err)
	}
	var snapshot string
	if err = s.db.QueryRow("SELECT cursors_json FROM processor_checkpoint_resets WHERE id=?", reset.ID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot == "[]" {
		t.Fatal("checkpoint backup missing")
	}
	counts, _ := s.RunCounts(ctx)
	if counts["completed"] != 1 {
		t.Fatal("history erased", counts)
	}
	list, _ := s.ProcessorTemplates(ctx)
	if len(list) != 2 {
		t.Fatal("templates erased")
	}
	// A later capture gets a new generation; historical dedup does not swallow it.
	batch.Generation = "new"
	batch.BatchID = "new"
	cursor.Generation = "new"
	if err = s.AcceptCaptured(ctx, batch, []string{"dummy"}, cursor); err != nil {
		t.Fatal(err)
	}
	counts, _ = s.RunCounts(ctx)
	if counts["queued"] != 1 || counts["completed"] != 1 {
		t.Fatal("replay not captured independently", counts)
	}
}

func TestCheckpointResetFailureRollsBackCursorAndAudit(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "processor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	s.db.Exec("INSERT INTO capture_cursors VALUES('fixture.txt','i','g',8,'h')")
	s.db.Exec("CREATE TRIGGER reset_failure BEFORE DELETE ON capture_cursors BEGIN SELECT RAISE(FAIL,'fixture disk error'); END")
	if _, err = s.ResetCaptureCheckpoints(context.Background()); err == nil {
		t.Fatal("expected rollback")
	}
	var audits int
	s.db.QueryRow("SELECT COUNT(*) FROM processor_checkpoint_resets").Scan(&audits)
	cursor, _ := s.CaptureCursor(context.Background(), "fixture.txt")
	if audits != 0 || cursor.Offset != 8 {
		t.Fatal("partial reset committed")
	}
}
