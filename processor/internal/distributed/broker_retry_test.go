package distributed

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBrokerRefusalRetainsOnlyKnownControlMessages(t *testing.T) {
	for _, body := range []string{"waiting for available memory\n", "proxy failure password=PRIVATE_TOKEN\n", strings.Repeat("x", 4096)} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, body, 409) }))
			defer s.Close()
			c := &Controller{BrokerURL: s.URL}
			code, err := c.broker(context.Background(), "GET", "workers", nil, nil)
			if code != 409 || err == nil {
				t.Fatalf("%d %v", code, err)
			}
			if strings.Contains(err.Error(), "PRIVATE_TOKEN") || len(err.Error()) > 160 {
				t.Fatal("arbitrary response leaked")
			}
			if strings.HasPrefix(body, "waiting for available memory") && !strings.Contains(err.Error(), "waiting for available memory") {
				t.Fatal("known reason lost")
			}
		})
	}
}

func TestWorkerRejectionCooldownExpires(t *testing.T) {
	c := &Controller{}
	c.deferWorker("dummy-worker", fmt.Errorf("HTTP 409"))
	if c.workerRetryReason("dummy-worker") == "" {
		t.Fatal("missing cooldown")
	}
	if c.workerRetryReason("other-worker") != "" {
		t.Fatal("cooldown affected unrelated worker")
	}
	c.mu.Lock()
	r := c.workerRetry["dummy-worker"]
	r.until = time.Now().Add(-time.Second)
	c.workerRetry["dummy-worker"] = r
	c.mu.Unlock()
	if c.workerRetryReason("dummy-worker") != "" {
		t.Fatal("worker never becomes eligible again")
	}
}
