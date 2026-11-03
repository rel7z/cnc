package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDNSRiftRealSchemaAndOutput(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("ip") == "" {
			t.Error("missing IP")
		}
		fmt.Fprint(w, `{"ip":"8.8.8.8","domain_count":6,"domain_list":["EXAMPLE.test","example.test.","www.example.test","8.8.8.8","https://bad.test/","_dmarc.example.test"]}`)
	}))
	defer s.Close()
	input := filepath.Join(t.TempDir(), "ips.txt")
	os.WriteFile(input, []byte("# example\n8.8.8.8\n8.8.8.8\n"), 0600)
	output := filepath.Join(t.TempDir(), "output.txt")
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"-f", input, "-o", output, "-delay", "0s"}, &stdout, &stderr, s.URL)
	if err != nil {
		t.Fatal(err)
	}
	expected := "example.test\nwww.example.test\n_dmarc.example.test\n"
	file, _ := os.ReadFile(output)
	if string(file) != expected || stdout.String() != expected || calls.Load() != 1 {
		t.Fatalf("bad result/calls: %q %d", file, calls.Load())
	}
}

func TestDNSRiftFailuresAreNotEmptySuccess(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		status     int
	}{
		{"invalid JSON", "<html>unavailable</html>", 200}, {"missing list", `{"domain_count":10}`, 200}, {"provider error", `{"error":"denied"}`, 200}, {"HTTP error", `{}`, 403}, {"rate limit", `{}`, 429}, {"wrong IP", `{"ip":"1.1.1.1","domain_list":[]}`, 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tt.status); fmt.Fprint(w, tt.body) }))
			defer s.Close()
			var stdout, stderr bytes.Buffer
			err := run(context.Background(), []string{"-ip", "8.8.8.8", "-retries", "0", "-delay", "0s"}, &stdout, &stderr, s.URL)
			if err == nil || stdout.Len() != 0 || !strings.Contains(stderr.String(), "[error]") {
				t.Fatalf("failure hidden: %v %s", err, stderr.String())
			}
		})
	}
}

func TestDNSRiftRateLimitRetryAndEmptyResult(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{"domain_list":[]}`)
	}))
	defer s.Close()
	domains, err := lookup(context.Background(), s.Client(), s.URL, "8.8.8.8", 1, &pacer{})
	if err != nil || len(domains) != 0 || calls.Load() != 2 {
		t.Fatalf("retry/empty result: %v %d", err, calls.Load())
	}
}

func TestDNSRiftTimeoutAndInputValidation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	client := s.Client()
	client.Timeout = 20 * time.Millisecond
	if _, err := lookup(context.Background(), client, s.URL, "8.8.8.8", 0, &pacer{}); err == nil {
		t.Fatal("timeout hidden")
	}
	if _, err := readIPs(strings.NewReader("8.8.8.8\nnot-an-ip\n")); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatal("invalid input accepted")
	}
}
