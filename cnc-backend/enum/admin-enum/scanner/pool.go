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

type job struct {
	fullURL  string
	keywords []string
	titles   []string
}

// Run dispatches all probe jobs across concurrency workers and returns a
// channel of results. When noBaseline is false (default), each domain gets a
// baseline probe first:
//
//   - If the baseline itself matches keywords/titles, every job for that
//     domain is skipped entirely (catch-all detected).
//   - If the baseline returns a body, its size is pre-seeded into the
//     per-domain dedup map so any hit with the same body size is suppressed.
//
// Additionally, across all domains, if two CONFIRMED results share the same
// domain AND the same body size, only the first is emitted; subsequent ones
// are downgraded to not-found and dropped by the writer.
func Run(
	domains []string,
	entries []URLEntry,
	keywords []string,
	titles []string,
	allTrue bool,
	concurrency int,
	timeout time.Duration,
	noBaseline bool,
) <-chan Result {

	// Count only relative-path entries; absolute URLs are per-URL, not
	// per-domain, so they don't benefit from baseline checks.
	relativeCount := 0
	for _, e := range entries {
		if !e.IsAbsolute {
			relativeCount++
		}
	}
	total := int64(len(domains)*relativeCount + (len(entries) - relativeCount))
	var completed atomic.Int64

	jobs := make(chan job, concurrency*2)
	rawResults := make(chan Result, concurrency*2)
	resultsCh := make(chan Result, concurrency*2)

	// --- signal handling ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\n[!] Interrupted — flushing results...")
		close(done)
	}()

	// --- HTTP clients ---
	client := newClient(timeout)
	blClient := baselineClient(timeout)

	// --- per-domain seen body-size map ---
	// seenSizes[domain] is the set of body sizes already emitted as CONFIRMED
	// for that domain.  Access is serialised in the filter goroutine below.
	seenSizes := make(map[string]map[int]bool)
	var seenMu sync.Mutex

	seedSize := func(domain string, size int) {
		seenMu.Lock()
		defer seenMu.Unlock()
		if seenSizes[domain] == nil {
			seenSizes[domain] = make(map[int]bool)
		}
		seenSizes[domain][size] = true
	}

	checkAndRecord := func(domain string, size int) (duplicate bool) {
		seenMu.Lock()
		defer seenMu.Unlock()
		if seenSizes[domain] == nil {
			seenSizes[domain] = make(map[int]bool)
		}
		if seenSizes[domain][size] {
			return true
		}
		seenSizes[domain][size] = true
		return false
	}

	// --- workers ---
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
				res := ProbeURL(client, j.fullURL, j.keywords, j.titles, allTrue)
				rawResults <- res

				n := completed.Add(1)
				printProgress(n, total)
			}
		}()
	}

	// --- filter goroutine ---
	// Reads raw results, applies body-size dedup, and forwards survivors.
	var filterWg sync.WaitGroup
	filterWg.Add(1)
	go func() {
		defer filterWg.Done()
		defer close(resultsCh)
		for r := range rawResults {
			if r.Status == StatusConfirmed && r.BodySize > 0 {
				if checkAndRecord(r.Domain, r.BodySize) {
					// Duplicate body size — downgrade and drop.
					r.Status = StatusNotFound
					continue
				}
			}
			resultsCh <- r
		}
	}()

	// --- job producer ---
	go func() {
		defer close(jobs)

		for _, domain := range domains {
			// Run baseline probe (unless disabled) before queuing any jobs.
			if !noBaseline {
				bl := ProbeBaseline(blClient, domain, keywords, titles, allTrue)

				if bl.Matched {
					// Every path on this domain looks like a hit — it's a
					// catch-all. Skip all relative-path jobs for it.
					fmt.Fprintf(os.Stderr,
						"\r[!] Skipping %s — baseline matched (catch-all detected)\n",
						domain)
					// Still probe absolute-URL entries; they're self-contained.
					for _, entry := range entries {
						if !entry.IsAbsolute {
							continue
						}
						select {
						case <-done:
							return
						case jobs <- job{fullURL: entry.FixedURL, keywords: keywords, titles: titles}:
						}
					}
					continue
				}

				// Pre-seed the dedup map with the baseline body size so any
				// hit that returns the exact same body is suppressed.
				if !bl.Unavailable && bl.BodySize > 0 {
					seedSize(domain, bl.BodySize)
				}
			}

			for _, entry := range entries {
				url := BuildURL(domain, entry)
				select {
				case <-done:
					return
				case jobs <- job{fullURL: url, keywords: keywords, titles: titles}:
				}
			}
		}
	}()

	// Close rawResults once all workers finish, then wait for filter to drain.
	go func() {
		wg.Wait()
		close(rawResults)
		filterWg.Wait()
		fmt.Fprintf(os.Stderr, "\r%-80s\r", "")
	}()

	return resultsCh
}

func printProgress(n, total int64) {
	pct := float64(n) / float64(total) * 100
	fmt.Fprintf(os.Stderr, "\r[*] Progress: %d/%d (%.1f%%)", n, total, pct)
}
