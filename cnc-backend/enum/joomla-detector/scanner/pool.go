package scanner

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// job represents a single (domain, extension) probe task.
type job struct {
	domain string
	entry  PluginEntry
}

// Run launches a concurrent worker pool to scan all (domain, extension) pairs.
// Results stream into the returned channel; the channel is closed when all
// workers finish or a SIGINT/SIGTERM is received.
func Run(domains []string, entries []PluginEntry, concurrency int, timeout time.Duration) <-chan Result {
	total := int64(len(domains) * len(entries))
	var completed atomic.Int64

	jobs := make(chan job, concurrency*2)
	resultsCh := make(chan Result, concurrency*2)

	// Graceful Ctrl+C handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\n[!] Interrupted — flushing results...")
		close(done)
	}()

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
				res := ProbeExtension(client, j.domain, j.entry)
				resultsCh <- res

				n := completed.Add(1)
				printProgress(n, total)
			}
		}()
	}

	// Feed jobs channel
	go func() {
		defer close(jobs)
		for _, domain := range domains {
			for _, entry := range entries {
				select {
				case <-done:
					return
				case jobs <- job{domain: domain, entry: entry}:
				}
			}
		}
	}()

	// Close results channel once all workers finish
	go func() {
		wg.Wait()
		close(resultsCh)
		fmt.Fprintf(os.Stderr, "\r%-80s\r", "") // clear progress line
	}()

	return resultsCh
}

func printProgress(n, total int64) {
	pct := float64(n) / float64(total) * 100
	fmt.Fprintf(os.Stderr, "\r[*] Progress: %d/%d (%.1f%%)", n, total, pct)
}
