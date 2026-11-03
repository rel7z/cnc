package store

import (
	"context"
	"testing"
	"time"
)

func TestShareRecoveryPreservesPinsAndFencesUncertainWork(t *testing.T) {
	s, _ := distributedFixture(t, false, 10)
	ctx := context.Background()
	makeRun := func(id string) string {
		_, runs, err := s.AcceptBatch(ctx, Batch{SourceID: "recovery", BatchID: id, Filename: "input.txt", Generation: "g", PayloadFingerprint: id, RecordsJSON: `["kept\n"]`, PreferredWorker: "gone"}, []string{"fixture"})
		if err != nil {
			t.Fatal(err)
		}
		return runs[0]
	}
	untouched, active, uncertain := makeRun("untouched"), makeRun("active"), makeRun("uncertain")
	for _, run := range []string{active, uncertain} {
		a, err := s.AllocateAttempt(ctx, run, "gone", time.Minute)
		// Affinity admits only one active share per worker at a time.
		if run == uncertain && err == ErrUnavailable {
			for _, attempt := range mustActive(t, s, ctx) {
				if err = s.FinishAttempt(ctx, attempt.ID, "completed", "", "", ""); err != nil {
					t.Fatal(err)
				}
			}
			a, err = s.AllocateAttempt(ctx, run, "gone", time.Minute)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = s.AuthorizeAttempt(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
		if run == uncertain {
			if err = s.FinishAttempt(ctx, a.ID, "interrupted", "", "", "lost"); err != nil {
				t.Fatal(err)
			}
			if err = s.RetryRun(ctx, run); err != nil {
				t.Fatal(err)
			}
		}
	}
	before, _ := s.PinRun(ctx, untouched)
	n, err := s.ReassignWaitingShares(ctx, "gone", []string{"a", "b"})
	if err != nil || n != 1 {
		t.Fatalf("recovered=%d err=%v", n, err)
	}
	_, worker, _, _ := s.AssignmentInfo(ctx, untouched)
	after, _ := s.PinRun(ctx, untouched)
	if worker != "a" || before != after {
		t.Fatal("input affinity or version pin changed incorrectly")
	}
	_, worker, _, _ = s.AssignmentInfo(ctx, uncertain)
	if worker != "gone" {
		t.Fatal("uncertain non-retry-safe execution was moved")
	}
}

func mustActive(t *testing.T, s *Store, ctx context.Context) []Attempt {
	t.Helper()
	attempts, err := s.ActiveAttempts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return attempts
}

func TestShareRecoveryWaitsForLeaseAndMovesRetrySafeExpiredWork(t *testing.T) {
	s, _ := distributedFixture(t, true, 10)
	ctx := context.Background()
	_, runs, err := s.AcceptBatch(ctx, Batch{SourceID: "recovery", BatchID: "retry-safe", Filename: "input.txt", Generation: "g", PayloadFingerprint: "retry-safe", RecordsJSON: `["kept\n"]`, PreferredWorker: "gone"}, []string{"fixture"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.AllocateAttempt(ctx, runs[0], "gone", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeAttempt(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if n, e := s.ReassignWaitingShares(ctx, "gone", []string{"healthy"}); e != nil || n != 0 {
		t.Fatalf("active lease moved: %d %v", n, e)
	}
	if _, err = s.db.Exec(`UPDATE processor_attempts SET lease_until=0 WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.ExpireAttempts(ctx); err != nil {
		t.Fatal(err)
	}
	if n, e := s.ReassignWaitingShares(ctx, "gone", []string{"healthy"}); e != nil || n != 1 {
		t.Fatalf("retry-safe share not recovered: %d %v", n, e)
	}
}
