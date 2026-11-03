package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func distributedFixture(t *testing.T, retry bool, limit int) (*Store, []string) {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "processor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s.SetEnabled(ctx, true)
	if err = s.RegisterProgram(ctx, "fixture", ProgramSpec{Version: "version-one", Limit: limit, RetrySafe: retry}); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for i := 0; i < 2; i++ {
		_, runs, err := s.AcceptBatch(ctx, Batch{SourceID: "fixture", BatchID: fmt.Sprint(i), Filename: "fixture.log", Generation: "g", PayloadFingerprint: fmt.Sprint(i), RecordsJSON: `["input"]`}, []string{"fixture"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, runs[0])
	}
	return s, ids
}
func TestDistributedVersionPinConcurrencyPauseAndRecovery(t *testing.T) {
	s, ids := distributedFixture(t, false, 1)
	ctx := context.Background()
	s.RegisterProgram(ctx, "fixture", ProgramSpec{Version: "version-two", Limit: 6, RetrySafe: true})
	spec, err := s.PinRun(ctx, ids[0])
	if err != nil || spec.Version != "version-one" || spec.Limit != 1 || spec.RetrySafe {
		t.Fatalf("run version changed: %+v %v", spec, err)
	}
	a, err := s.AllocateAttempt(ctx, ids[0], "worker-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AllocateAttempt(ctx, ids[1], "worker-2", time.Minute); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("program limit bypassed: %v", err)
	}
	s.SetEnabled(ctx, false)
	if err = s.AuthorizeAttempt(ctx, a.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("pause authorization bypassed: %v", err)
	}
	s.SetEnabled(ctx, true)
	if err = s.AuthorizeAttempt(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	run, err := s.Run(ctx, ids[0])
	if err != nil || run.Status != "running" {
		t.Fatalf("active remote attempt discarded on restart: %+v %v", run, err)
	}
	if _, err = s.AuthenticateAttempt(ctx, a.ID, "wrong-token"); !errors.Is(err, ErrFenced) {
		t.Fatalf("credential check bypassed: %v", err)
	}
	s.db.Exec(`UPDATE processor_attempts SET lease_until=0 WHERE id=?`, a.ID)
	if err = s.RenewAttempt(ctx, a.ID, time.Minute); !errors.Is(err, ErrFenced) {
		t.Fatalf("expired lease resurrected: %v", err)
	}
	if err = s.StageCompletion(ctx, a.ID, `{"exit_code":0}`); !errors.Is(err, ErrFenced) {
		t.Fatalf("expired result accepted: %v", err)
	}
	if err = s.ExpireAttempts(ctx); err != nil {
		t.Fatal(err)
	}
	run, _ = s.Run(ctx, ids[0])
	if run.Status != "interrupted" {
		t.Fatalf("unsafe run automatically retried: %s", run.Status)
	}
}
func TestRetryBudgetExcludesRejectedAndManualRetryResetsBudget(t *testing.T) {
	s, ids := distributedFixture(t, true, 6)
	ctx := context.Background()
	id := ids[0]
	for i := 0; i < 4; i++ {
		a, err := s.AllocateAttempt(ctx, id, "worker", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.RejectAttempt(ctx, a.ID, "capacity changed"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		a, err := s.AllocateAttempt(ctx, id, "worker", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		s.db.Exec(`UPDATE processor_attempts SET lease_until=0 WHERE id=?`, a.ID)
		if err = s.ExpireAttempts(ctx); err != nil {
			t.Fatal(err)
		}
		run, _ := s.Run(ctx, id)
		expected := "queued"
		if i == 2 {
			expected = "interrupted"
		}
		if run.Status != expected {
			t.Fatalf("retry %d status %s expected %s", i+1, run.Status, expected)
		}
	}
	if err := s.RetryRun(ctx, id); err != nil {
		t.Fatal(err)
	}
	a, err := s.AllocateAttempt(ctx, id, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Exec(`UPDATE processor_attempts SET lease_until=0 WHERE id=?`, a.ID)
	if err = s.ExpireAttempts(ctx); err != nil {
		t.Fatal(err)
	}
	run, _ := s.Run(ctx, id)
	if run.Status != "queued" {
		t.Fatal("manual retry retained exhausted budget")
	}
}
