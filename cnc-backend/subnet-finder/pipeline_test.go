package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIPv4ExpansionBounds(t *testing.T) {
	for _, tc := range []struct {
		prefix string
		count  int
		reject bool
	}{
		{"127.0.0.0/30", 2, false}, {"127.0.0.0/31", 2, false}, {"127.0.0.1/32", 1, false}, {"127.0.0.0/20", 4094, false}, {"0.0.0.0/0", 0, true}, {"::/0", 0, true}, {"2001:db8::/64", 0, true}, {"::1/128", 0, true},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			count := 0
			err := walkPrefix(context.Background(), netip.MustParsePrefix(tc.prefix), 4096, func(ip netip.Addr) error {
				if !ip.Is4() {
					t.Fatal("IPv6 emitted")
				}
				count++
				return nil
			})
			if (err != nil) != tc.reject || count != tc.count {
				t.Fatalf("count=%d error=%v", count, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(walkPrefix(ctx, netip.MustParsePrefix("127.0.0.0/20"), 4096, func(netip.Addr) error { return nil }), context.Canceled) {
		t.Fatal("expansion ignores cancellation")
	}
}

func fixtureDependencies() pipelineDependencies {
	return pipelineDependencies{
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")}, nil
		},
		Fetch: func(context.Context, netip.Addr) (netip.Prefix, error) {
			return netip.MustParsePrefix("127.0.0.0/30"), nil
		},
		Probe: func(context.Context, netip.Addr) bool { return true },
	}
}
func fixtureOptions(t *testing.T) pipelineOptions {
	return pipelineOptions{Concurrency: 3, MaxAddresses: 4096, Timeout: time.Second, StateDir: t.TempDir()}
}

func TestIPv6PrefixFromLookupIsIgnored(t *testing.T) {
	deps := fixtureDependencies()
	deps.Fetch = func(context.Context, netip.Addr) (netip.Prefix, error) { return netip.MustParsePrefix("::/0"), nil }
	var probes atomic.Int32
	deps.Probe = func(context.Context, netip.Addr) bool { probes.Add(1); return true }
	var output bytes.Buffer
	if err := runPipeline(context.Background(), strings.NewReader("owned.fixture\n"), &output, fixtureOptions(t), deps, NewProgressUI()); err != nil {
		t.Fatal(err)
	}
	if probes.Load() != 0 || output.Len() != 0 {
		t.Fatal("IPv6 lookup reached expansion/probing")
	}
}

func TestPipelineExactDedupAndConcurrency(t *testing.T) {
	deps := fixtureDependencies()
	var active, peak, fetches atomic.Int32
	deps.Fetch = func(context.Context, netip.Addr) (netip.Prefix, error) {
		fetches.Add(1)
		return netip.MustParsePrefix("127.0.0.0/30"), nil
	}
	deps.Probe = func(ctx context.Context, ip netip.Addr) bool {
		if !ip.Is4() {
			t.Error("IPv6 reached probe")
		}
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		return true
	}
	var output bytes.Buffer
	err := runPipeline(context.Background(), strings.NewReader("owned.test\nowned.test\n127.0.0.0/30\n127.0.0.0/29\n::/0\n"), &output, fixtureOptions(t), deps, NewProgressUI())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(output.String())
	seen := map[string]bool{}
	for _, line := range lines {
		if seen[line] {
			t.Fatal("duplicate output")
		}
		seen[line] = true
	}
	if len(lines) != 6 || fetches.Load() != 1 || peak.Load() > 3 {
		t.Fatalf("lines=%d fetches=%d peak=%d", len(lines), fetches.Load(), peak.Load())
	}
}

type notifyingWriter struct {
	once  sync.Once
	first chan struct{}
	data  bytes.Buffer
}

func (w *notifyingWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.first) })
	return w.data.Write(data)
}
func TestLiveOutputBeforeInputEOF(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	output := &notifyingWriter{first: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- runPipeline(context.Background(), reader, output, fixtureOptions(t), fixtureDependencies(), NewProgressUI())
	}()
	if _, err := io.WriteString(writer, "127.0.0.1/32\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-output.first:
	case <-time.After(2 * time.Second):
		t.Fatal("input was accumulated before output")
	}
	writer.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if output.data.String() != "127.0.0.1\n" {
		t.Fatal("live result missing")
	}
}

func TestCancellationStopsBlockedPipeline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deps := fixtureDependencies()
	started := make(chan struct{})
	var once sync.Once
	deps.Probe = func(ctx context.Context, ip netip.Addr) bool {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return false
	}
	done := make(chan error, 1)
	go func() {
		done <- runPipeline(ctx, strings.NewReader("127.0.0.0/20\n"), io.Discard, fixtureOptions(t), deps, NewProgressUI())
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation leaked blocked workers")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("fixture output failure") }
func TestOutputFailureIsVisible(t *testing.T) {
	err := runPipeline(context.Background(), strings.NewReader("127.0.0.0/30\n"), failingWriter{}, fixtureOptions(t), fixtureDependencies(), NewProgressUI())
	if err == nil || !strings.Contains(err.Error(), "fixture output failure") {
		t.Fatal(err)
	}
}
