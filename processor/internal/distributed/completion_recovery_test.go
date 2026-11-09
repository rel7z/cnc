package distributed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrokenStagedCompletionDoesNotBlockWorkerRefresh(t *testing.T) {
	c, _ := newFixture(t, true, false)
	ctx := context.Background()
	run := acceptFixture(t, c, "broken-staged")
	a, err := c.Store.AllocateAttempt(ctx, run, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Store.AuthorizeAttempt(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(c.root(), "attempts", a.ID, "log")
	if err = writeAtomic(log, []byte("complete\n")); err != nil {
		t.Fatal(err)
	}
	if err = c.Store.StageCompletion(ctx, a.ID, `{"exit_code":0}`); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(log); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write([]byte("[]")) }))
	defer broker.Close()
	c.BrokerURL = broker.URL
	err = c.step(ctx)
	if err != nil {
		t.Fatal(err)
	}
	recovered, _ := c.Store.Attempt(ctx, a.ID)
	if recovered.Phase != "failed" {
		t.Fatalf("damaged completion was not isolated: %+v", recovered)
	}
	if calls.Load() == 0 {
		t.Fatalf("one unavailable staged log blocks worker refresh and dispatch; step error: %v", err)
	}
}
