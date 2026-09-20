package scanner

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// job represents a single (domain, plugin) probe task.
type job struct {
	domain string
	slug   string
}

// Run launches a worker pool to scan all (domain, plugin) combinations.
// Results are sent to the returned channel as they complete — the channel
// is closed when all workers are done or a SIGINT is received.
func Run(domains, plugins []string, concurrency int, timeout time.Duration) <-chan Result {
	total := int64(len(domains) * len(plugins))
	var completed atomic.Int64

	jobs := make(chan job, concurrency*2)
	resultsCh := make(chan Result, concurrency*2)

	// Handle Ctrl+C gracefully
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\n[!] Interrupted — flushing results...")
		close(done)
	}()

	// Start workers
	client := newClient(timeout)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				select {
				case <-done:
					return
				default:
				}
				res := ProbePlugin(client, j.domain, j.slug)
				resultsCh <- res

				n := completed.Add(1)
				printProgress(n, total)
			}
		}()
	}

	// Feed jobs; close jobs channel when done or interrupted
	go func() {
		defer close(jobs)
		for _, domain := range domains {
			for _, slug := range plugins {
				select {
				case <-done:
					return
				case jobs <- job{domain: domain, slug: slug}:
				}
			}
		}
	}()

	// Close results channel once all workers finish
	go func() {
		wg.Wait()
		close(resultsCh)
		// Clear the progress line
		fmt.Fprintf(os.Stderr, "\r%-70s\r", "")
	}()

	return resultsCh
}

// newHTTPClient builds a reusable client (kept for potential external use).
func newHTTPClient(timeout time.Duration) *http.Client {
	return newClient(timeout)
}

// printProgress writes a compact progress indicator to stderr.
func printProgress(n, total int64) {
	pct := float64(n) / float64(total) * 100
	fmt.Fprintf(os.Stderr, "\r[*] Progress: %d/%d (%.1f%%)", n, total, pct)
}
