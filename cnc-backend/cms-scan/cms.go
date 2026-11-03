package main

import (
	"bufio"
	"cnc-enum/detection"
	"cnc-enum/version"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const maxBodyBytes = detection.BodyLimit

var cmsSignatures = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range detection.Names {
		m[n] = true
	}
	return m
}()

func detectCMS(body string, headers http.Header, _ *http.Client, _ string) string {
	cms, _ := detection.Analyze([]byte(body), headers)
	return cms
}
func resultName(result detection.Result) string {
	if result.Outcome == "detected" {
		return result.CMS
	}
	return map[string]string{"unknown": "cms-unknown", "blocked": "cms-blocked", "error": "cms-errors"}[result.Outcome]
}
func scanURL(inputURL string, files map[string]*os.File, mu *sync.Mutex, processed *int64, client *http.Client) {
	r := detection.Classify(context.Background(), client, inputURL)
	name := resultName(r)
	mu.Lock()
	defer mu.Unlock()
	f := files[name]
	if f == nil {
		f = files["unknown"]
	}
	if f != nil {
		f.WriteString(r.URL + "\n")
	}
	if r.Outcome == "detected" {
		fmt.Printf("[FILE:%s.txt] %s\n", name, r.URL)
	}
	atomic.AddInt64(processed, 1)
}
func worker(jobs <-chan string, files map[string]*os.File, mu *sync.Mutex, wg *sync.WaitGroup, processed *int64) {
	defer wg.Done()
	client := newClient(10 * time.Second)
	for raw := range jobs {
		scanURL(raw, files, mu, processed, client)
	}
}
func newClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}}
}

type options struct {
	Input, Output string
	Max           int
	Automatic     bool
	Timeout       time.Duration
}

func run(ctx context.Context, o options, stdout, stderr io.Writer) int {
	if o.Max < 1 || o.Max > 256 || o.Timeout < time.Second || o.Timeout > 120*time.Second {
		fmt.Fprintln(stderr, "[ERROR] threads must be 1–256; timeout must be 1s–120s")
		return 1
	}
	input, e := os.Open(o.Input)
	if e != nil {
		fmt.Fprintln(stderr, "[ERROR]", e)
		return 1
	}
	defer input.Close()
	base := newClient(o.Timeout)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 256
	transport.MaxIdleConnsPerHost = 4
	transport.MaxConnsPerHost = 4
	transport.IdleConnTimeout = 30 * time.Second
	transport.MaxResponseHeaderBytes = 64 * 1024
	defer transport.CloseIdleConnections()
	var requestTransport http.RoundTripper = transport
	if gateway := os.Getenv("ENUM_EVIDENCE_GATEWAY"); gateway != "" {
		u, e := url.Parse(gateway)
		valid := e == nil && u.Scheme == "http" && u.Path == "/fetch" && u.User == nil
		if valid {
			ip, e := netip.ParseAddr(u.Hostname())
			valid = e == nil && ip.IsLoopback()
		}
		if !valid || os.Getenv("ENUM_EVIDENCE_TOKEN") == "" {
			fmt.Fprintln(stderr, "[ERROR] invalid worker gateway")
			return 1
		}
		requestTransport = &version.GatewayTransport{URL: gateway, Token: os.Getenv("ENUM_EVIDENCE_TOKEN")}
	}
	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "[ERROR] scan cancelled before starting")
		return 1
	}
	inputInfo, e := input.Stat()
	if e != nil || !inputInfo.Mode().IsRegular() {
		fmt.Fprintln(stderr, "[ERROR] input must be a readable regular file")
		return 1
	}
	files, e := openResultFiles(o.Output, inputInfo)
	if e != nil {
		fmt.Fprintln(stderr, "[ERROR]", e)
		return 1
	}
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	scheduler := detection.NewScheduler(requestTransport, o.Max, o.Automatic)
	jobs := make(chan string, 256)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failed, success, processed atomic.Int64
	var outputError atomic.Bool
	counts := map[string]int{}
	workers := o.Max
	if workers > 64 {
		workers = 64
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for raw := range jobs {
				// Queued targets have not been scanned; cancellation must not
				// report them as network errors or issue more HTTP requests.
				if ctx.Err() != nil {
					return
				}
				client := detection.NewSession(base, scheduler)
				r := detection.Classify(detection.TargetContext(ctx, raw), client, raw)
				name := resultName(r)
				if r.Outcome == "error" || r.Outcome == "blocked" {
					failed.Add(1)
				} else {
					success.Add(1)
				}
				processed.Add(1)
				mu.Lock()
				counts[name]++
				if _, e := fmt.Fprintln(files[name], r.URL); e != nil {
					outputError.Store(true)
				}
				if name == "cms-unknown" {
					if _, e := fmt.Fprintf(stdout, "[FILE:unknown.txt] %s\n", r.URL); e != nil {
						outputError.Store(true)
					}
					if _, e := fmt.Fprintln(files["unknown"], r.URL); e != nil {
						outputError.Store(true)
					}
				}
				if _, e := fmt.Fprintf(stdout, "[FILE:%s.txt] %s\n", name, r.URL); e != nil {
					outputError.Store(true)
				}
				if r.Err != nil {
					fmt.Fprintf(stderr, "[WARN] %s: %v\n", r.URL, r.Err)
				}
				mu.Unlock()
			}
		}()
	}
	start := time.Now()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	// Bounded recent deduplication; CNC's merge layer remains authoritative.
	seen := map[string]bool{}
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") || seen[raw] {
			continue
		}
		if len(seen) >= 65536 {
			clear(seen)
		}
		seen[raw] = true
		select {
		case jobs <- raw:
		case <-ctx.Done():
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	for _, f := range files {
		if e := f.Sync(); e != nil {
			outputError.Store(true)
		}
	}
	m := scheduler.Snapshot()
	fmt.Fprintf(stderr, "[*] processed=%d detected=%d unknown=%d blocked=%d errors=%d concurrency=%d requests=%d cache_hits=%d elapsed=%s\n", processed.Load(), success.Load()-int64(counts["cms-unknown"]), counts["cms-unknown"], counts["cms-blocked"], counts["cms-errors"], m.Concurrency, m.Requests, m.CacheHits, time.Since(start).Round(time.Millisecond))
	if scanner.Err() != nil || outputError.Load() {
		fmt.Fprintln(stderr, "[ERROR] incomplete input or output")
		return 1
	}
	if failed.Load() > 0 || ctx.Err() != nil {
		if success.Load() == 0 {
			return 1
		}
		return 2
	}
	return 0
}
func main() {
	var o options
	flag.StringVar(&o.Input, "l", "", "Input filename")
	flag.StringVar(&o.Input, "f", "", "Input filename (alias)")
	flag.StringVar(&o.Output, "o", "./results", "Output directory")
	flag.IntVar(&o.Max, "t", 256, "Maximum request concurrency")
	flag.IntVar(&o.Max, "c", 256, "Maximum request concurrency (alias)")
	flag.BoolVar(&o.Automatic, "auto-concurrency", true, "Automatically tune concurrency")
	flag.DurationVar(&o.Timeout, "timeout", 10*time.Second, "Request timeout")
	help := flag.Bool("h", false, "Show help")
	flag.Parse()
	if *help {
		flag.PrintDefaults()
		return
	}
	if o.Input == "" {
		fmt.Fprintln(os.Stderr, "[ERROR] -l input is required")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, o, os.Stdout, os.Stderr))
}

// Open every result before truncating any, so an input/output alias or an
// unwritable destination cannot destroy existing data during startup.
func openResultFiles(directory string, input os.FileInfo) (map[string]*os.File, error) {
	if err := os.MkdirAll(directory, 0755); err != nil {
		return nil, err
	}
	files := map[string]*os.File{}
	complete := false
	defer func() {
		if !complete {
			for _, f := range files {
				f.Close()
			}
		}
	}()
	names := append(append([]string{}, detection.Names...), "cms-unknown", "cms-blocked", "cms-errors", "unknown")
	var infos []os.FileInfo
	for _, name := range names {
		path := filepath.Join(directory, name+".txt")
		if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
			return nil, fmt.Errorf("result %s is not a regular file", name)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return nil, fmt.Errorf("open %s result: %w", name, err)
		}
		files[name] = file
		info, err := file.Stat()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || os.SameFile(input, info) {
			return nil, fmt.Errorf("result %s aliases the input or is not a regular file", name)
		}
		for _, previous := range infos {
			if os.SameFile(previous, info) {
				return nil, fmt.Errorf("result files must not alias each other")
			}
		}
		infos = append(infos, info)
	}
	for name, file := range files {
		if err := file.Truncate(0); err != nil {
			return nil, fmt.Errorf("initialize %s result: %w", name, err)
		}
	}
	complete = true
	return files, nil
}
