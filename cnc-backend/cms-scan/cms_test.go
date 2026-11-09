package main

import (
	"bytes"
	"cnc-enum/version"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Invalid input must not terminate the concurrent batch before healthy targets.
func TestWorkerPoolContinuesAfterInvalidURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readme.html" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`<html><script src="/wp-content/a.js"></script><script src="/wp-includes/b.js"></script></html>`))
	}))
	defer server.Close()
	dir := t.TempDir()
	files := map[string]*os.File{}
	for _, name := range []string{"unknown", "wordpress"} {
		f, err := os.Create(filepath.Join(dir, name+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = f
		defer f.Close()
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var processed int64
	jobs := make(chan string, 100)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go worker(jobs, files, &mu, &wg, &processed)
	}
	bad := []string{"http://%zz", "http://[broken", "bad host", "https://example.invalid/invalid%"}
	for i := 0; i < 10; i++ {
		for _, target := range bad {
			jobs <- target
		}
		jobs <- server.URL
	}
	close(jobs)
	wg.Wait()
	if processed != 50 {
		t.Fatalf("processed %d, want 50", processed)
	}
	for name, want := range map[string]int{"unknown": 40, "wordpress": 10} {
		data, err := os.ReadFile(filepath.Join(dir, name+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		got := len(strings.Split(strings.TrimSpace(string(data)), "\n"))
		if got != want {
			t.Fatalf("%s: %d records, want %d", name, got, want)
		}
	}
}

func TestRunReportsBlockedPartialAndBadSettings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/blocked" {
			w.WriteHeader(403)
			return
		}
		w.Write([]byte(`<meta name="generator" content="WordPress">`))
	}))
	defer server.Close()
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	os.WriteFile(input, []byte(server.URL+"\n"+server.URL+"/blocked\n"), 0600)
	var stdout, stderr bytes.Buffer
	o := options{Input: input, Output: filepath.Join(dir, "out"), Max: 16, Automatic: true, Timeout: time.Second}
	if code := run(context.Background(), o, &stdout, &stderr); code != 2 {
		t.Fatalf("code=%d: %s", code, &stderr)
	}
	if !strings.Contains(stdout.String(), "[FILE:wordpress.txt]") || !strings.Contains(stdout.String(), "[FILE:cms-blocked.txt]") {
		t.Fatal(&stdout)
	}
	o.Max = 0
	if code := run(context.Background(), o, &stdout, &stderr); code != 1 {
		t.Fatal(code)
	}
}

func TestCMSUsesSharedWorkerBudgetAndForwardedHeaders(t *testing.T) {
	var active, peak atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for n > peak.Load() {
			old := peak.Load()
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		w.Header().Set("X-Shopify-Store", "owned-fixture")
		w.Write([]byte(""))
	}))
	defer fixture.Close()
	gateway, e := version.StartGateway(1)
	if e != nil {
		t.Fatal(e)
	}
	defer gateway.Close()
	token, remove, e := gateway.Register(context.Background(), version.ArtifactV1{SchemaVersion: 1, JobID: "cms-gateway-fixture"})
	if e != nil {
		t.Fatal(e)
	}
	defer remove()
	t.Setenv("ENUM_EVIDENCE_GATEWAY", gateway.URL)
	t.Setenv("ENUM_EVIDENCE_TOKEN", token)
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	var lines strings.Builder
	for n := 0; n < 8; n++ {
		fmt.Fprintf(&lines, "%s/%d\n", fixture.URL, n)
	}
	os.WriteFile(input, []byte(lines.String()), 0600)
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), options{Input: input, Output: filepath.Join(dir, "output"), Max: 32, Automatic: true, Timeout: time.Second}, &stdout, &stderr); code != 0 {
		t.Fatalf("%d: %s", code, &stderr)
	}
	if peak.Load() != 1 || strings.Count(stdout.String(), "[FILE:shopify.txt]") != 8 {
		t.Fatalf("budget/header regression: peak=%d output=%s", peak.Load(), &stdout)
	}
}

func TestRunPreservesInputAndExistingResultsOnStartupFailure(t *testing.T) {
	for _, kind := range []string{"input-alias", "hardlink", "symlink", "gateway", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			out := filepath.Join(dir, "output")
			os.Mkdir(out, 0700)
			input := filepath.Join(dir, "input.txt")
			original := []byte("fixture.example.invalid\n")
			if kind == "input-alias" {
				input = filepath.Join(out, "unknown.txt")
			}
			os.WriteFile(input, original, 0600)
			keep := filepath.Join(out, "wordpress.txt")
			os.WriteFile(keep, []byte("keep previous results\n"), 0600)
			if kind == "hardlink" {
				if err := os.Link(input, filepath.Join(out, "unknown.txt")); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "symlink" {
				if err := os.Symlink(input, filepath.Join(out, "unknown.txt")); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "gateway" {
				t.Setenv("ENUM_EVIDENCE_GATEWAY", "http://external.invalid/fetch")
				t.Setenv("ENUM_EVIDENCE_TOKEN", "fixture")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			var stdout, stderr bytes.Buffer
			if code := run(ctx, options{Input: input, Output: out, Max: 1, Timeout: time.Second}, &stdout, &stderr); code != 1 {
				t.Fatalf("code %d: %s", code, &stderr)
			}
			data, _ := os.ReadFile(input)
			if !bytes.Equal(data, original) {
				t.Fatal("Input was destroyed during startup")
			}
			data, _ = os.ReadFile(keep)
			if string(data) != "keep previous results\n" {
				t.Fatal("Existing results were truncated before startup validation")
			}
		})
	}
}

func TestRunCancellationDoesNotClassifyUnscannedQueuedTargets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var requests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		time.Sleep(50 * time.Millisecond)
		cancel()
		fmt.Fprint(w, `<meta name="generator" content="WordPress">`)
	}))
	defer fixture.Close()
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	var targets strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&targets, "%s/%d\n", fixture.URL, i)
	}
	os.WriteFile(input, []byte(targets.String()), 0600)
	var stdout, stderr bytes.Buffer
	if code := run(ctx, options{Input: input, Output: filepath.Join(dir, "out"), Max: 1, Timeout: time.Second}, &stdout, &stderr); code != 1 {
		t.Fatalf("code %d: %s", code, &stderr)
	}
	if requests.Load() != 1 || strings.Count(stdout.String(), "[FILE:") != 1 || !strings.Contains(stderr.String(), "processed=1 ") {
		t.Fatalf("Queued targets were misreported: requests=%d stdout=%s stderr=%s", requests.Load(), &stdout, &stderr)
	}
}

func TestRunAllCMSAndNonCMSOutcomes(t *testing.T) {
	names := []string{"wordpress", "joomla", "drupal", "shopify", "ghost", "magento", "wix", "squarespace"}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.Trim(r.URL.Path, "/")
		for _, cms := range names {
			if name == cms {
				fmt.Fprintf(w, `<meta name="generator" content="%s">`, cms)
				return
			}
		}
		switch name {
		case "blocked":
			w.WriteHeader(403)
		case "error":
			w.WriteHeader(500)
		default:
			fmt.Fprint(w, "Plain non-CMS page")
		}
	}))
	defer fixture.Close()
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	var targets strings.Builder
	for _, name := range append(names, "unknown", "blocked", "error") {
		fmt.Fprintf(&targets, "%s/%s\n", fixture.URL, name)
	}
	fmt.Fprintf(&targets, "# comment\n\n%s/wordpress\nhttp://%%zz\n", fixture.URL)
	os.WriteFile(input, []byte(targets.String()), 0600)
	var stdout, stderr bytes.Buffer
	output := filepath.Join(dir, "out")
	if code := run(context.Background(), options{Input: input, Output: output, Max: 8, Automatic: true, Timeout: time.Second}, &stdout, &stderr); code != 2 {
		t.Fatalf("code %d: %s", code, &stderr)
	}
	for _, name := range append(names, "cms-unknown", "cms-blocked", "cms-errors") {
		data, err := os.ReadFile(filepath.Join(output, name+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if name == "cms-errors" {
			want = 2
		}
		if count := strings.Count(string(data), "\n"); count != want {
			t.Fatalf("%s: %d results, want %d", name, count, want)
		}
	}
	if !strings.Contains(stderr.String(), "processed=12 detected=8 unknown=1 blocked=1 errors=2") {
		t.Fatal(&stderr)
	}
}
