package watcher

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user/cnc-processor/internal/store"
)

func TestUnchangedSnapshotCacheDetectsOverwriteWithRestoredModificationTime(t *testing.T) {
	dir := t.TempDir()
	s, err := store.NewStore(filepath.Join(dir, "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = s.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "input.txt")
	if err = os.WriteFile(path, []byte("first\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &Capturer{Store: s, Dir: dir, Routes: map[string][]string{"input.txt": {"fixture"}}}
	for i := 0; i < 3; i++ {
		if err = c.Capture(ctx, "input.txt"); err != nil {
			t.Fatal(err)
		}
	}
	if c.verified[path] == "" {
		t.Fatal("unchanged snapshot was not cached")
	}
	info, _ := os.Stat(path)
	// Use an older mtime so timestamp restoration is independent of clock precision.
	stamp := info.ModTime().Add(-time.Hour)
	if err = os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err = c.Capture(ctx, "input.txt"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("other\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err = c.Capture(ctx, "input.txt"); err != nil {
		t.Fatal(err)
	}
	counts, err := s.RunCounts(ctx)
	if err != nil || counts["queued"] != 2 {
		t.Fatalf("overwrite not captured: %v %v", counts, err)
	}
}

func TestCaptureRestartReplacementAndDeletionBarrier(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CNC_APP_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"routes":{"input.txt":["fixture-a","fixture-b"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.NewStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.InitSchema(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c := &Capturer{Store: db, Dir: dir}
	path := filepath.Join(dir, "input.txt")
	write := func(data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want int) {
		t.Helper()
		counts, err := db.RunCounts(ctx)
		if err != nil || counts["queued"] != want {
			t.Fatalf("counts %v err %v want %d", counts, err, want)
		}
	}
	write("first\npartial")
	if err = c.Capture(ctx, "input.txt"); !errors.Is(err, ErrPaused) {
		t.Fatal(err)
	}
	if err = db.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err = c.Capture(ctx, "input.txt"); !errors.Is(err, ErrPartial) {
		t.Fatal(err)
	}
	check(2)
	// New capturer simulates process restart; committed input must not duplicate.
	c = &Capturer{Store: db, Dir: dir}
	if err = c.Capture(ctx, "input.txt"); !errors.Is(err, ErrPartial) {
		t.Fatal(err)
	}
	check(2)
	write("first\npartial\n")
	if err = c.Capture(ctx, "input.txt"); err != nil {
		t.Fatal(err)
	}
	check(4)
	// Replacement with a longer file must not inherit the previous byte offset.
	replacement := filepath.Join(dir, "replacement")
	if err = os.WriteFile(replacement, []byte(strings.Repeat("new\n", 1100)), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if err = c.Capture(ctx, "input.txt"); err != nil {
		t.Fatal(err)
	}
	check(8)
	if err = c.Capture(ctx, "input.txt"); err != nil {
		t.Fatal(err)
	}
	check(8)
	// Same-inode overwrite whose size grows is detected by the consumed prefix.
	write(strings.Repeat("changed\n", 1200))
	if err = c.Capture(ctx, "input.txt"); err != nil {
		t.Fatal(err)
	}
	check(12)
	if err = c.Capture(ctx, "../input.txt"); err == nil {
		t.Fatal("accepted traversal")
	}
}

func TestCaptureSplitsLargeInputByBytes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CNC_APP_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"routes":{"large.txt":["fixture"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.NewStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.InitSchema(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = db.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat(strings.Repeat("x", (1<<20)-1)+"\n", 9)
	if err = os.WriteFile(filepath.Join(dir, "large.txt"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	c := &Capturer{Store: db, Dir: dir}
	if err = c.Capture(ctx, "large.txt"); err != nil {
		t.Fatal(err)
	}
	runs, err := db.ListRuns(ctx)
	if err != nil || len(runs) != 9 {
		t.Fatalf("runs=%d err=%v", len(runs), err)
	}
	total := 0
	for _, run := range runs {
		raw, err := db.GetBatchRecords(ctx, run.BatchAcceptanceID)
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		if err := json.Unmarshal([]byte(raw), &lines); err != nil {
			t.Fatal(err)
		}
		size := 0
		for _, line := range lines {
			size += len(line)
		}
		if size > 4<<20 {
			t.Fatalf("batch too large: %d", size)
		}
		total += size
	}
	if total != len(content) {
		t.Fatalf("captured %d bytes want %d", total, len(content))
	}
	if err = c.Capture(ctx, "large.txt"); err != nil {
		t.Fatal(err)
	}
	counts, err := db.RunCounts(ctx)
	if err != nil || counts["queued"] != 9 {
		t.Fatalf("duplicate capture: %v %v", counts, err)
	}
}
