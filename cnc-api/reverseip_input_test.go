package cnc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReverseIPNormalizeAllIPv4AddressesAndGlobalDedup(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	lookup := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		mu.Lock()
		calls[host]++
		mu.Unlock()
		switch host {
		case "one.example":
			return []net.IPAddr{{IP: net.ParseIP("2001:db8::2")}, {IP: net.ParseIP("192.0.2.2")}, {IP: net.ParseIP("192.0.2.1")}}, nil
		case "two.example":
			return []net.IPAddr{{IP: net.ParseIP("192.0.2.2")}, {IP: net.ParseIP("2001:db8::1")}}, nil
		case "missing.example":
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		default:
			t.Errorf("unexpected DNS lookup: %s", host)
			return nil, fmt.Errorf("unexpected host")
		}
	}
	var output bytes.Buffer
	stats, err := normalizeReverseIPInput(context.Background(), strings.NewReader("# comment\nONE.example\none.example.\nhttps://two.example/path\n192.0.2.1\n::ffff:192.0.2.1\n2001:0db8::1\nmissing.example\nnot a domain\n"), &output, lookup)
	if err != nil {
		t.Fatal(err)
	}
	want := "192.0.2.1\n192.0.2.2\n"
	if output.String() != want {
		t.Fatalf("got %q, want %q", output.String(), want)
	}
	if stats.UniqueIPs != 2 || stats.Domains != 3 || stats.DuplicateInputs != 2 || stats.DuplicateIPs != 2 || stats.DNSFailures != 1 || stats.InvalidInputs != 1 || stats.SkippedIPv6 != 3 {
		t.Fatalf("incorrect summary: %+v", stats)
	}
	if !reflect.DeepEqual(calls, map[string]int{"one.example": 1, "two.example": 1, "missing.example": 1}) {
		t.Fatal(calls)
	}
}

func TestReverseIPBoundedResolutionAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var active, peak atomic.Int32
	started := make(chan struct{}, 32)
	lookup := func(ctx context.Context, _ string) ([]net.IPAddr, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("DNS lookup has no deadline")
		}
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	var input strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&input, "host%d.example\n", i)
	}
	done := make(chan error, 1)
	go func() {
		_, err := normalizeReverseIPInput(ctx, strings.NewReader(input.String()), &bytes.Buffer{}, lookup)
		done <- err
	}()
	for i := 0; i < 16; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("resolver did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation stalled")
	}
	if peak.Load() > 16 || active.Load() != 0 {
		t.Fatalf("peak=%d active=%d", peak.Load(), active.Load())
	}
}

func TestReverseIPAllFailedDoesNotProduceProviderInput(t *testing.T) {
	var output bytes.Buffer
	stats, err := normalizeReverseIPInput(context.Background(), strings.NewReader("bad input\nmissing.example\n"), &output, func(context.Context, string) ([]net.IPAddr, error) { return nil, context.DeadlineExceeded })
	if err == nil || stats.UniqueIPs != 0 || output.Len() != 0 || stats.InvalidInputs != 1 || stats.DNSFailures != 1 {
		t.Fatalf("stats=%+v err=%v output=%q", stats, err, output.String())
	}
}

func TestReverseIPIPv6OnlyIsSkippedWithoutDNS(t *testing.T) {
	var output bytes.Buffer
	stats, err := normalizeReverseIPInput(context.Background(), strings.NewReader("2001:db8::1\n"), &output, func(context.Context, string) ([]net.IPAddr, error) {
		t.Fatal("literal IP triggered DNS")
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "no IPv4 addresses") || stats.SkippedIPv6 != 1 || output.Len() != 0 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
}

func TestReverseIPEveryProviderReceivesOnlyGloballyUniqueIPs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, provider := range []string{"reverseip-domain", "reverseip-thc", "reverseip-dnsrift"} {
		t.Run(provider, func(t *testing.T) {
			s := processorTestServer(t)
			s.config.DataDir = t.TempDir()
			var calls atomic.Int32
			s.reverseIPLookup = func(context.Context, string) ([]net.IPAddr, error) {
				calls.Add(1)
				return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}, {IP: net.ParseIP("192.0.2.2")}, {IP: net.ParseIP("2001:db8::1")}}, nil
			}
			for _, id := range []string{"one", "two"} {
				s.workers[id] = &Worker{ID: id, Status: WorkerStatusOnline, MaxTasks: 2, Resources: memoryFixtureSnapshot(), SendCh: make(chan *Message, 4)}
			}
			input := filepath.Join(t.TempDir(), "domains.txt")
			original := []byte("ONE.example\none.example.\ntwo.example\n192.0.2.1\n")
			if err := os.WriteFile(input, original, 0600); err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(map[string]any{"tool_id": provider, "input_file": input})
			w := httptest.NewRecorder()
			s.handleToolsLaunchAPI(w, httptest.NewRequest("POST", "/api/tools/launch", bytes.NewReader(payload)))
			if w.Code != 201 {
				t.Fatal(w.Body.String())
			}
			var response struct {
				JobID string `json:"job_id"`
			}
			json.Unmarshal(w.Body.Bytes(), &response)
			deadline := time.Now().Add(2 * time.Second)
			for {
				s.mu.RLock()
				ready := s.jobs[response.JobID].TotalTasks == 2
				s.mu.RUnlock()
				if ready {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("no prepared chunks")
				}
				time.Sleep(time.Millisecond)
			}
			seen := map[string]bool{}
			s.mu.RLock()
			for _, task := range s.tasks {
				data, err := os.ReadFile(task.Payload["chunk_path"].(string))
				if err != nil {
					t.Fatal(err)
				}
				for _, line := range strings.Fields(string(data)) {
					if ip, err := netip.ParseAddr(line); err != nil || !ip.Is4() || seen[line] {
						t.Fatalf("invalid/duplicate provider input %q", line)
					}
					seen[line] = true
				}
				if !strings.Contains(task.Payload["download_url"].(string), response.JobID+"-") {
					t.Fatal("chunk URL is not job-specific")
				}
				download := httptest.NewRecorder()
				s.handleFilesAPI(download, httptest.NewRequest("GET", task.Payload["download_url"].(string), nil))
				if download.Code != 200 || !bytes.Equal(download.Body.Bytes(), data) {
					t.Fatalf("worker download differs from normalized chunk: status=%d", download.Code)
				}
			}
			stats := s.jobs[response.JobID].ReverseIP
			s.mu.RUnlock()
			if len(seen) != 2 || stats.UniqueIPs != 2 || calls.Load() != 2 {
				t.Fatalf("ips=%v stats=%+v lookups=%d", seen, stats, calls.Load())
			}
			source, _ := os.ReadFile(input)
			if !bytes.Equal(source, original) {
				t.Fatal("original source was changed")
			}
		})
	}
}

func TestReverseIPPreparationFailureAndCancellationNeverDispatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, cancelJob := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelJob), func(t *testing.T) {
			s := processorTestServer(t)
			s.config.DataDir = t.TempDir()
			input := filepath.Join(t.TempDir(), "domains.txt")
			os.WriteFile(input, []byte("missing.example\n"), 0600)
			started := make(chan struct{})
			s.reverseIPLookup = func(ctx context.Context, _ string) ([]net.IPAddr, error) {
				close(started)
				if cancelJob {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return nil, fmt.Errorf("DNS fixture failure")
			}
			job := &Job{ID: "reverse-fixture", Name: "Tool: reverseip-thc", InputFile: input, Workers: 2, Status: "pending"}
			s.jobs[job.ID] = job
			done := make(chan struct{})
			go func() { s.processSpreadJob(job); close(done) }()
			<-started
			if cancelJob {
				s.mu.Lock()
				job.Status = "cancelled"
				s.mu.Unlock()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("preparation did not stop")
			}
			if len(s.tasks) != 0 || len(s.taskQueue) != 0 {
				t.Fatal("failed/cancelled preparation dispatched provider work")
			}
			if cancelJob {
				if job.Status != "cancelled" {
					t.Fatal(job.Status)
				}
			} else if job.Status != "failed" || !strings.Contains(job.Error, "DNS failures") {
				t.Fatalf("status=%s error=%s", job.Status, job.Error)
			}
		})
	}
}

func TestReverseIPTargetValidation(t *testing.T) {
	for raw, want := range map[string]string{"EXAMPLE.com.": "example.com", "https://example.com:443/path": "example.com", "example.com:443": "example.com", "::ffff:192.0.2.1": "192.0.2.1", "https://[2001:db8::1]/": "2001:db8::1", "bücher.example": "xn--bcher-kva.example"} {
		got, err := reverseIPTarget(raw)
		if err != nil || got != want {
			t.Fatalf("%q got %q err=%v", raw, got, err)
		}
	}
	for _, raw := range []string{"bad input", "192.0.2.0/24", "https://user:secret@example.com/", "bad_.example", "-bad.example", "fe80::1%eth0"} {
		if _, err := reverseIPTarget(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestReverseIPRejectsModeBypass(t *testing.T) {
	for _, id := range []string{"reverseip-domain", "reverseip-thc", "reverseip-dnsrift"} {
		for _, mode := range []string{"broadcast", "server"} {
			s := processorTestServer(t)
			payload, _ := json.Marshal(map[string]any{"tool_id": id, "mode": mode, "input_file": "unused.txt"})
			w := httptest.NewRecorder()
			s.handleToolsLaunchAPI(w, httptest.NewRequest("POST", "/api/tools/launch", bytes.NewReader(payload)))
			if w.Code != 400 || len(s.jobs) != 0 {
				t.Fatalf("%s %s bypass accepted: %d", id, mode, w.Code)
			}
		}
	}
}
