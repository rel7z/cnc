package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func priorityFixture(t *testing.T, weights map[string]int, count int) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "priority.db")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s.SetEnabled(ctx, true)
	for id := range weights {
		s.RegisterProgram(ctx, id, ProgramSpec{Version: "v", Limit: 1000})
		for i := 0; i < count; i++ {
			_, _, err = s.AcceptBatch(ctx, Batch{SourceID: id, BatchID: fmt.Sprint(i), Filename: fmt.Sprintf("source-%d.txt", i%2), PayloadFingerprint: fmt.Sprint(i), RecordsJSON: `["x\n"]`}, []string{id})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = s.SetConfiguredRanks(ctx, weights); err != nil {
		t.Fatal(err)
	}
	return s, path
}
func dispatchPriority(t *testing.T, s *Store, skip string, accepted bool) string {
	t.Helper()
	ctx := context.Background()
	runs, err := s.QueuedRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if run.ProgramID == skip {
			continue
		}
		if _, err = s.PinRun(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
		a, err := s.AllocateAttempt(ctx, run.ID, "worker", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if accepted {
			if err = s.CommitPriorityTurn(ctx, a.ID); err != nil {
				t.Fatal(err)
			}
			if err = s.CommitPriorityTurn(ctx, a.ID); err != nil {
				t.Fatal(err)
			}
		} else {
			if err = s.RejectAttempt(ctx, a.ID, "capacity changed"); err != nil {
				t.Fatal(err)
			}
		}
		return run.ProgramID
	}
	t.Fatal("no candidate")
	return ""
}
func TestPriorityWeightedTurnsPersistAndDoNotChargeRejections(t *testing.T) {
	s, path := priorityFixture(t, map[string]int{"high": 5, "low": 1}, 140)
	for i := 0; i < 3; i++ {
		if got := dispatchPriority(t, s, "", true); got != "high" {
			t.Fatal(got)
		}
	}
	if got := dispatchPriority(t, s, "", false); got != "high" {
		t.Fatal(got)
	}
	s.Close()
	var err error
	s, err = NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"high", "high", "low"} {
		if got := dispatchPriority(t, s, "", true); got != want {
			t.Fatalf("restart/rejection got %s want %s", got, want)
		}
	}
	counts := map[string]int{}
	for i := 0; i < 60; i++ {
		counts[dispatchPriority(t, s, "", true)]++
	}
	if counts["high"] != 50 || counts["low"] != 10 {
		t.Fatalf("not 5:1: %v", counts)
	}
}
func TestPriorityDefaultsSourcesBlockedToolsAndBacklogs(t *testing.T) {
	s, _ := priorityFixture(t, map[string]int{"a": 1, "b": 1}, 125)
	for _, want := range []string{"a", "b", "a", "b"} {
		if got := dispatchPriority(t, s, "", true); got != want {
			t.Fatalf("equal sharing: %s", got)
		}
	}
	ctx := context.Background()
	runs, _ := s.QueuedRuns(ctx)
	if len(runs) != 200 {
		t.Fatalf("per-tool cap: %d", len(runs))
	}
	_, fresh, err := s.AcceptBatch(ctx, Batch{SourceID: "fresh", BatchID: "fresh", Filename: "fresh.txt", RecordsJSON: `["y\n"]`}, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	runs, _ = s.QueuedRuns(ctx)
	if runs[0].ID != fresh[0] {
		t.Fatalf("source fairness: %+v", runs[0])
	}
	p, _ := s.ProgramPriorities(ctx)
	five := 5
	_, err = s.UpdateProgramPriority(ctx, "a", &five, p[0].Revision)
	if err != nil {
		t.Fatal(err)
	}
	if got := dispatchPriority(t, s, "a", true); got != "b" {
		t.Fatal("blocked tool starved eligible tool")
	}
	if got := dispatchPriority(t, s, "", true); got != "a" {
		t.Fatal("blocked tool did not rejoin")
	}
	for i := 0; i < 4; i++ {
		if got := dispatchPriority(t, s, "", true); got != "a" {
			t.Fatal(got)
		}
	}
	if got := dispatchPriority(t, s, "", true); got != "b" {
		t.Fatal("blocked turns accumulated")
	}
}
func TestPriorityOverridesConfigurationRevisionsAndPinnedVersions(t *testing.T) {
	s, path := priorityFixture(t, map[string]int{"a": 2, "b": 1}, 3)
	ctx := context.Background()
	p, _ := s.ProgramPriorities(ctx)
	five := 5
	updated, err := s.UpdateProgramPriority(ctx, "a", &five, p[0].Revision)
	if err != nil || updated.EffectiveRank != 5 || updated.ConfiguredRank != 2 {
		t.Fatal(updated, err)
	}
	if _, err = s.UpdateProgramPriority(ctx, "a", nil, p[0].Revision); !errors.Is(err, ErrPriorityConflict) {
		t.Fatal(err)
	}
	if err = s.SetConfiguredRanks(ctx, map[string]int{"a": 3}); err != nil {
		t.Fatal(err)
	}
	p, _ = s.ProgramPriorities(ctx)
	if p[0].EffectiveRank != 5 || p[0].ConfiguredRank != 3 {
		t.Fatal(p)
	}
	s.Close()
	s, err = NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.InitSchema()
	p, _ = s.ProgramPriorities(ctx)
	if p[0].OverrideRank == nil || *p[0].OverrideRank != 5 {
		t.Fatal("override lost")
	}
	reset, err := s.UpdateProgramPriority(ctx, "a", nil, p[0].Revision)
	if err != nil || reset.EffectiveRank != 3 || reset.OverrideRank != nil {
		t.Fatal(reset, err)
	}
	zero := 0
	if _, err = s.UpdateProgramPriority(ctx, "a", &zero, reset.Revision); err == nil {
		t.Fatal("zero accepted")
	}
	if err = s.SetConfiguredRanks(ctx, map[string]int{"a": 6}); err == nil {
		t.Fatal("invalid config")
	}
	if _, err = s.UpdateProgramPriority(ctx, "unknown", nil, 1); !errors.Is(err, ErrPriorityNotFound) {
		t.Fatal(err)
	}
	runs, _ := s.QueuedRuns(ctx)
	before, _ := s.PinRun(ctx, runs[0].ID)
	s.UpdateProgramPriority(ctx, runs[0].ProgramID, &five, reset.Revision)
	after, _ := s.PinRun(ctx, runs[0].ID)
	if before != after {
		t.Fatal("priority changed pinned version")
	}
}
