package store

import (
	"context"
	"encoding/json"
	"github.com/user/cnc-processor/cluster"
	"strings"
	"testing"
	"time"
)

func TestPublicationDeduplicationChainingAndCyclePrevention(t *testing.T) {
	s, ids := distributedFixture(t, false, 6)
	ctx := context.Background()
	if err := s.RegisterProgram(ctx, "downstream", ProgramSpec{Version: "v2", Limit: 6}); err != nil {
		t.Fatal(err)
	}
	a, err := s.AllocateAttempt(ctx, ids[0], "w", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s.AuthorizeAttempt(ctx, a.ID)
	p := cluster.Publication{Name: "next.txt", Generation: "g", Sequence: 1, Lines: []string{"one\n", "two\n", "one\n"}}
	routes := []string{"fixture", "downstream"}
	for i := 0; i < 2; i++ {
		if err = s.AcceptPublication(ctx, a, p, routes); err != nil {
			t.Fatal(err)
		}
	}
	p.Sequence = 2
	if err = s.AcceptPublication(ctx, a, p, routes); err != nil {
		t.Fatal(err)
	}
	runs, err := s.QueuedRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	next := ""
	count := 0
	for _, r := range runs {
		if r.ProgramID == "downstream" {
			next = r.ID
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicate/chained runs: %+v", runs)
	}
	ancestry, err := s.Ancestry(ctx, next)
	if err != nil || len(ancestry) != 1 || ancestry[0] != "fixture" {
		t.Fatalf("ancestry: %v %v", ancestry, err)
	}
	b, err := s.AllocateAttempt(ctx, next, "w", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s.AuthorizeAttempt(ctx, b.ID)
	p.Lines = []string{"three\n"}
	p.Sequence = 1
	if err = s.AcceptPublication(ctx, b, p, routes); err != nil {
		t.Fatal(err)
	}
	after, _ := s.QueuedRuns(ctx)
	if len(after) != 1 {
		t.Fatalf("cycle enqueued: %+v", after)
	}
	pending, err := s.PendingOutput(ctx)
	if err != nil || len(pending) != 3 {
		t.Fatalf("unique output: %v %v", pending, err)
	}
	filtered, err := s.FilterCaptured(ctx, "next.txt", []string{"one\n", "three\n", "original\n"})
	if err != nil || len(filtered) != 1 || filtered[0] != "original\n" {
		t.Fatalf("capture loop: %v %v", filtered, err)
	}
	p.Lines = []string{"different\n"}
	if err = s.AcceptPublication(ctx, b, p, routes); err == nil {
		t.Fatal("identity conflict accepted")
	}
}
func TestQueueFairnessAcrossToolsAndSources(t *testing.T) {
	s, _ := distributedFixture(t, false, 100)
	ctx := context.Background()
	s.RegisterProgram(ctx, "other", ProgramSpec{Version: "v", Limit: 6})
	for i := 0; i < 120; i++ {
		s.AcceptBatch(ctx, Batch{SourceID: "large", BatchID: strings.Repeat("x", i+1), Filename: "large.txt", RecordsJSON: `["x\n"]`}, []string{"fixture"})
	}
	_, other, err := s.AcceptBatch(ctx, Batch{SourceID: "small", BatchID: "small", Filename: "small.txt", RecordsJSON: `["y\n"]`}, []string{"other"})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := s.QueuedRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range queued[:3] {
		if r.ID == other[0] {
			found = true
		}
	}
	if !found {
		t.Fatal("large input starved another tool")
	}
	spec, _ := s.PinRun(ctx, queued[0].ID)
	_ = spec
	attempt, err := s.AllocateAttempt(ctx, queued[0].ID, "w", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CommitPriorityTurn(ctx, attempt.ID); err != nil {
		t.Fatal(err)
	}
	queued, err = s.QueuedRuns(ctx)
	if err != nil || queued[0].ProgramID != "other" {
		t.Fatalf("tool round robin: %+v %v", queued[0], err)
	}
}
func TestCancelFencesExecutionButPreservesPublishedOutput(t *testing.T) {
	s, ids := distributedFixture(t, false, 1)
	ctx := context.Background()
	a, err := s.AllocateAttempt(ctx, ids[0], "w", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s.AuthorizeAttempt(ctx, a.ID)
	p := cluster.Publication{Name: "partial.txt", Generation: "g", Sequence: 1, Lines: []string{"confirmed\n"}}
	if err = s.AcceptPublication(ctx, a, p, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.CancelRun(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err = s.RenewAttempt(ctx, a.ID, time.Minute); err != ErrFenced {
		t.Fatalf("cancel did not fence lease: %v", err)
	}
	pending, _ := s.PendingOutput(ctx)
	if len(pending) != 1 {
		t.Fatal("cancel removed partial output")
	}
}

func TestPendingLegacyMigrationSplitsAndPinsNewVersionsOnce(t *testing.T) {
	s, ids := distributedFixture(t, false, 6)
	ctx := context.Background()
	records, _ := json.Marshal([]string{strings.Repeat("a", 600000) + "\n", strings.Repeat("b", 600000) + "\n"})
	if _, err := s.db.Exec(`UPDATE batches SET records_json=? WHERE acceptance_id=(SELECT batch_acceptance_id FROM runs WHERE id=?)`, string(records), ids[0]); err != nil {
		t.Fatal(err)
	}
	s.RegisterProgram(ctx, "fixture", ProgramSpec{Version: "new-wrapper", Limit: 6})
	if err := s.MigrateQueue(ctx); err != nil {
		t.Fatal(err)
	}
	queued, err := s.QueuedRuns(ctx)
	if err != nil || len(queued) != 3 {
		t.Fatalf("migration: %v %v", queued, err)
	}
	for _, run := range queued {
		spec, err := s.PinRun(ctx, run.ID)
		if err != nil || spec.Version != "new-wrapper" {
			t.Fatalf("old algorithm pinned: %+v %v", spec, err)
		}
		data, _ := s.GetBatchRecords(ctx, run.BatchAcceptanceID)
		var lines []string
		json.Unmarshal([]byte(data), &lines)
		total := 0
		for _, line := range lines {
			total += len(line)
		}
		if total > 1<<20 {
			t.Fatal("migration produced oversized chunk")
		}
	}
	s.RegisterProgram(ctx, "fixture", ProgramSpec{Version: "later-change", Limit: 6})
	if err := s.MigrateQueue(ctx); err != nil {
		t.Fatal(err)
	}
	spec, _ := s.PinRun(ctx, ids[0])
	if spec.Version != "new-wrapper" {
		t.Fatal("migration changed a pinned v2 run")
	}
}

func TestInternalPublicationPinsChainAndNeverRenders(t *testing.T) {
	s, ids := distributedFixture(t, false, 6)
	ctx := context.Background()
	s.RegisterProgram(ctx, "checker", ProgramSpec{Version: "new-target", Limit: 6})
	a, err := s.AllocateAttempt(ctx, ids[0], "w", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s.AuthorizeAttempt(ctx, a.ID)
	settings := cluster.Program{Script: &cluster.ScriptExecution{Runtime: "python", Input: "input.txt", Outputs: []string{"results.txt"}, Policies: []cluster.OutputPolicy{{Path: "results.txt", Drive: false, SendTo: "checker", Target: &cluster.PinnedTarget{ProgramID: "checker", Version: "old-target", Limit: 6}}}}}
	if err := s.RegisterSettings(ctx, a.Version, settings); err != nil {
		t.Fatal(err)
	}
	p := cluster.Publication{Name: "fixture__results.txt", Generation: "g", Sequence: 1, Lines: []string{"owned\n", "owned\n"}}
	for i := 0; i < 2; i++ {
		if err := s.AcceptPublication(ctx, a, p, []string{"wrong-current-route"}); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := s.PendingOutput(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("internal rendered: %v %v", pending, err)
	}
	runs, err := s.QueuedRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, run := range runs {
		if run.ProgramID == "checker" {
			found++
			spec, err := s.PinRun(ctx, run.ID)
			if err != nil || spec.Version != "old-target" {
				t.Fatalf("lost pin: %+v %v", spec, err)
			}
		}
	}
	if found != 1 {
		t.Fatal("duplicate checker jobs", runs)
	}
	p.Name = "fixture__undeclared.txt"
	if err := s.AcceptPublication(ctx, a, p, nil); err == nil {
		t.Fatal("undeclared output accepted")
	}
	// Internal output remains durable across a database reopen and duplicate transfer.
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM processor_output_lines WHERE name='fixture__results.txt'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestPublicationRenamesMergedOutputAndKeepsReceiptIdempotent(t *testing.T) {
	s, ids := distributedFixture(t, false, 6)
	ctx := context.Background()
	a, err := s.AllocateAttempt(ctx, ids[0], "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeAttempt(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	settings := cluster.Program{Script: &cluster.ScriptExecution{Input: "input.txt", Outputs: []string{"x.txt"}, Policies: []cluster.OutputPolicy{{Path: "x.txt", MergedName: "apalah.txt", Drive: true}}}}
	if err = s.RegisterSettings(ctx, a.Version, settings); err != nil {
		t.Fatal(err)
	}
	p := cluster.Publication{Name: "fixture__x.txt", Generation: "g", Sequence: 1, Lines: []string{"one\n"}}
	for i := 0; i < 2; i++ {
		if err = s.AcceptPublication(ctx, a, p, nil); err != nil {
			t.Fatal(err)
		}
	}
	lines, err := s.PendingOutput(ctx)
	if err != nil || len(lines) != 1 || lines[0].Name != "apalah.txt" || lines[0].Line != "one\n" {
		t.Fatalf("mapped output: %+v %v", lines, err)
	}
	p.Name = "fixture__undeclared.txt"
	if err = s.AcceptPublication(ctx, a, p, nil); err == nil {
		t.Fatal("mapping accepted an undeclared worker file")
	}
	settings.Script.Policies[0].MergedName = "../escape.txt"
	if err = (cluster.Program{Entrypoint: "run.py", Script: settings.Script}).ValidateScript(); err == nil {
		t.Fatal("accepted escaping merged path")
	}
}
