package watcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/cnc-processor/internal/store"
)

func TestSpreadCaptureEvenRestartAppendAndOffline(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := store.NewStore(filepath.Join(t.TempDir(), "dummy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	s.SetEnabled(ctx, true)
	workers := []string{"c", "a", "b"}
	makeCapturer := func() *Capturer {
		return &Capturer{Store: s, Dir: dir, Routes: map[string][]string{"dummy.txt": {"fixture"}}, SpreadWorkers: func(string) ([]string, bool) { return workers, true }}
	}
	var input strings.Builder
	for i := 0; i < 23; i++ {
		fmt.Fprintf(&input, "record-%02d\n", i)
	}
	path := filepath.Join(dir, "dummy.txt")
	os.WriteFile(path, []byte(input.String()), 0600)
	workers = nil
	if err = makeCapturer().Capture(ctx, "dummy.txt"); !errors.Is(err, ErrWorkersUnavailable) {
		t.Fatal(err)
	}
	cur, _ := s.CaptureCursor(ctx, path)
	if cur.Offset != 0 {
		t.Fatal("input consumed without workers")
	}
	workers = []string{"c", "a", "b"}
	if err = makeCapturer().Capture(ctx, "dummy.txt"); err != nil {
		t.Fatal(err)
	}
	check := func(want map[string]int, wantRecords int) {
		t.Helper()
		runs, e := s.ListRuns(ctx)
		if e != nil {
			t.Fatal(e)
		}
		counts := map[string]int{}
		seen := map[string]bool{}
		for _, r := range runs {
			_, worker, n, e := s.AssignmentInfo(ctx, r.ID)
			if e != nil {
				t.Fatal(e)
			}
			counts[worker] += n
			raw, _ := s.GetBatchRecords(ctx, r.BatchAcceptanceID)
			var lines []string
			json.Unmarshal([]byte(raw), &lines)
			for _, line := range lines {
				if seen[line] {
					t.Fatal("record duplicated")
				}
				seen[line] = true
			}
		}
		if fmt.Sprint(counts) != fmt.Sprint(want) || len(seen) != wantRecords {
			t.Fatalf("shares=%v records=%d", counts, len(seen))
		}
	}
	check(map[string]int{"a": 8, "b": 8, "c": 7}, 23)
	if err = makeCapturer().Capture(ctx, "dummy.txt"); err != nil {
		t.Fatal(err)
	}
	check(map[string]int{"a": 8, "b": 8, "c": 7}, 23)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString("record-23\npartial")
	f.Close()
	if err = makeCapturer().Capture(ctx, "dummy.txt"); !errors.Is(err, ErrPartial) {
		t.Fatal(err)
	}
	check(map[string]int{"a": 8, "b": 8, "c": 8}, 24)
	if err = makeCapturer().Capture(ctx, "dummy.txt"); !errors.Is(err, ErrPartial) {
		t.Fatal(err)
	}
	check(map[string]int{"a": 8, "b": 8, "c": 8}, 24)
}

func TestSpreadCaptureConsumesWholeSnapshotBeforeDeletionBarrier(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := store.NewStore(filepath.Join(t.TempDir(), "dummy.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.InitSchema(); e != nil {
		t.Fatal(e)
	}
	s.SetEnabled(ctx, true)
	content := strings.Repeat(strings.Repeat("x", (1<<20)-1)+"\n", 9)
	path := filepath.Join(dir, "dummy.txt")
	if e = os.WriteFile(path, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
	c := Capturer{Store: s, Dir: dir, Routes: map[string][]string{"dummy.txt": {"fixture"}}, SpreadWorkers: func(string) ([]string, bool) { return []string{"a", "b"}, true }}
	if e = c.Capture(ctx, "dummy.txt"); e != nil {
		t.Fatal(e)
	}
	cur, _ := s.CaptureCursor(ctx, path)
	if cur.Offset != int64(len(content)) {
		t.Fatalf("deletion barrier left %d bytes uncaptured", int64(len(content))-cur.Offset)
	}
	n, e := s.SpreadRecordCount(ctx, "dummy.txt", cur.Generation)
	if e != nil || n != 9 {
		t.Fatalf("records=%d err=%v", n, e)
	}
}
