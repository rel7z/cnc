package version

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGatewaySharesOneBudgetAcrossScannerRegistrations(t *testing.T) {
	var active, peak atomic.Int32
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	entered := make(chan struct{}, 10)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			p := peak.Load()
			if p >= n || peak.CompareAndSwap(p, n) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
			fmt.Fprint(w, "ok")
		case <-r.Context().Done():
		}
	}))
	defer fixture.Close()
	defer unblock()
	_, rawPort, _ := net.SplitHostPort(strings.TrimPrefix(fixture.URL, "http://"))
	port, _ := strconv.Atoi(rawPort)
	gateway, err := StartGateway(2)
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	artifact := ArtifactV1{SchemaVersion: 1, JobID: "job-a", TargetScope: TargetScope{CIDRs: []string{"127.0.0.0/8"}, Schemes: []string{"http"}, Ports: []int{port}}}
	first, remove, err := gateway.Register(context.Background(), artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	artifact.JobID = "job-b"
	second, removeB, err := gateway.Register(context.Background(), artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer removeB()
	finished := make(chan error, 6)
	for i := 0; i < 6; i++ {
		token := first
		if i%2 == 1 {
			token = second
		}
		go func(token string) {
			transport := &GatewayTransport{URL: gateway.URL, Token: token}
			req, _ := http.NewRequest("GET", fixture.URL, nil)
			response, err := transport.RoundTrip(req)
			if err == nil {
				_, err = io.ReadAll(response.Body)
				response.Body.Close()
			}
			finished <- err
		}(token)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("requests not admitted")
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		metrics := gateway.Budget.Snapshot()
		queued := 0
		for _, m := range metrics {
			queued += m.Queued
		}
		if queued == 4 {
			break
		}
		if time.Now().After(deadline) {
			unblock()
			t.Fatal(metrics)
		}
		time.Sleep(time.Millisecond)
	}
	if peak.Load() != 2 {
		unblock()
		t.Fatal("separate registrations escaped the worker budget")
	}
	unblock()
	for i := 0; i < 6; i++ {
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range gateway.Budget.Snapshot() {
		if m.Active != 0 || m.Queued != 0 {
			t.Fatal(m)
		}
	}
}
func TestGatewayCancelsQueuedTaskAndRejectsForeignToken(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	entered := make(chan struct{}, 1)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
			fmt.Fprint(w, "ok")
		case <-r.Context().Done():
		}
	}))
	defer fixture.Close()
	defer unblock()
	_, rawPort, _ := net.SplitHostPort(strings.TrimPrefix(fixture.URL, "http://"))
	port, _ := strconv.Atoi(rawPort)
	gateway, _ := StartGateway(1)
	defer gateway.Close()
	a := ArtifactV1{SchemaVersion: 1, JobID: "hold", TargetScope: TargetScope{CIDRs: []string{"127.0.0.0/8"}, Schemes: []string{"http"}, Ports: []int{port}}}
	hold, remove, _ := gateway.Register(context.Background(), a)
	defer remove()
	do := func(token string) error {
		transport := &GatewayTransport{URL: gateway.URL, Token: token}
		req, _ := http.NewRequest("GET", fixture.URL, nil)
		response, err := transport.RoundTrip(req)
		if err == nil {
			io.ReadAll(response.Body)
			response.Body.Close()
		}
		return err
	}
	if do("foreign-token") == nil {
		t.Fatal("foreign registration accepted")
	}
	done := make(chan error, 1)
	go func() { done <- do(hold) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	a.JobID = "cancel"
	token, removeCancel, _ := gateway.Register(ctx, a)
	defer removeCancel()
	cancelled := make(chan error, 1)
	go func() { cancelled <- do(token) }()
	waitQueued(t, gateway.Budget, "cancel", 1)
	cancel()
	if err := <-cancelled; err == nil {
		unblock()
		t.Fatal("queued task was not canceled")
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if m := gateway.Budget.Snapshot()["cancel"]; m.Active != 0 || m.Queued != 0 || m.Cancelled != 1 {
		t.Fatal(m)
	}
}

func TestGatewayTransportReusesLoopbackConnections(t *testing.T) {
	var connections atomic.Int32
	fixture := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	fixture.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	fixture.Start()
	defer fixture.Close()
	transport := &GatewayTransport{URL: fixture.URL, Token: "owned"}
	for i := 0; i < 100; i++ {
		req, _ := http.NewRequest(http.MethodGet, "http://owned.example/", nil)
		response, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, response.Body); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	if connections.Load() != 1 {
		t.Fatalf("100 sequential probes opened %d connections", connections.Load())
	}
}
