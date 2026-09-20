package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	baseURL        = "https://ip.thc.org/"
	defaultWorkers = 3
	defaultLimit   = 100 // max results per IP (API max)
	requestTimeout = 20 * time.Second
	defaultDelay   = 250 * time.Millisecond
	maxRetries     = 3
)

type result struct {
	ip      string
	domains []string
	err     error
}

func main() {
	var (
		inputFile  string
		outputFile string
		workers    int
		limit      int
		delay      time.Duration
		format     string
		retries    int
	)

	flag.StringVar(&inputFile, "f", "", "Path to .txt file with one IP per line")
	flag.StringVar(&inputFile, "file", "", "Path to .txt file with one IP per line")
	flag.StringVar(&inputFile, "input", "", "Path to .txt file with one IP per line")
	flag.StringVar(&outputFile, "o", "", "Output file to write results (default: stdout)")
	flag.StringVar(&outputFile, "out", "", "Output file to write results (default: stdout)")
	flag.IntVar(&workers, "w", defaultWorkers, "Number of concurrent workers (low concurrency recommended)")
	flag.IntVar(&limit, "l", defaultLimit, "Max domains to return per IP (1-100)")
	flag.IntVar(&limit, "limit", defaultLimit, "Max domains to return per IP (1-100)")
	flag.DurationVar(&delay, "delay", defaultDelay, "Pacing delay between requests per worker (e.g. 250ms)")
	flag.StringVar(&format, "format", "domains", "Output format: 'domains' (one per line, deduplicated) or 'ip-domains'")
	flag.IntVar(&retries, "retries", maxRetries, "Max retry attempts on HTTP 429 rate limit")
	flag.Parse()

	// If positional arg provided and inputFile is empty, use first arg
	if inputFile == "" && flag.NArg() > 0 {
		inputFile = flag.Arg(0)
	}

	if inputFile == "" {
		fmt.Fprintln(os.Stderr, "Usage: reverseip-thc -f ips.txt [-o output.txt] [-w 3] [-delay 250ms] [-format domains]")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if workers < 1 {
		workers = 1
	}

	ips, err := readLines(inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input file: %v\n", err)
		os.Exit(1)
	}

	var out io.Writer = os.Stdout
	if outputFile != "" {
		f, err := os.Create(outputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}

	fmt.Fprintf(os.Stderr, "[*] Loaded %d IPs — starting lookup with %d workers (delay=%v, format=%s)\n",
		len(ips), workers, delay, format)

	jobs := make(chan string, len(ips))
	results := make(chan result, len(ips))

	client := &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			MaxIdleConns:        workers,
			MaxIdleConnsPerHost: workers,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	// Spin up workers with delay pacing
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for ip := range jobs {
				domains, err := lookupWithRetry(client, ip, limit, delay, retries)
				results <- result{ip: ip, domains: domains, err: err}
				if delay > 0 {
					time.Sleep(delay)
				}
			}
		}(i)
	}

	// Feed jobs
	for _, ip := range ips {
		jobs <- ip
	}
	close(jobs)

	// Close results once all workers are done
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect and write results
	var done, failed, totalDomains int64
	total := len(ips)
	writer := bufio.NewWriterSize(out, 1<<20) // 1 MB buffer

	seenDomains := make(map[string]struct{})
	var domainsMu sync.Mutex

	for r := range results {
		atomic.AddInt64(&done, 1)
		if r.err != nil {
			atomic.AddInt64(&failed, 1)
			fmt.Fprintf(os.Stderr, "[-] %s: %v\n", r.ip, r.err)
			continue
		}

		if format == "ip-domains" {
			if len(r.domains) == 0 {
				fmt.Fprintf(writer, "%s: (no results)\n", r.ip)
			} else {
				fmt.Fprintf(writer, "%s: %s\n", r.ip, strings.Join(r.domains, ", "))
			}
			atomic.AddInt64(&totalDomains, int64(len(r.domains)))
		} else {
			// Plain domains, deduplicated
			domainsMu.Lock()
			for _, d := range r.domains {
				dClean := strings.ToLower(strings.TrimSpace(d))
				if dClean == "" {
					continue
				}
				if _, seen := seenDomains[dClean]; !seen {
					seenDomains[dClean] = struct{}{}
					fmt.Fprintln(writer, dClean)
					atomic.AddInt64(&totalDomains, 1)
				}
			}
			domainsMu.Unlock()
		}

		// Progress reporting every 25 IPs or on completion
		if d := atomic.LoadInt64(&done); d%25 == 0 || d == int64(total) {
			fmt.Fprintf(os.Stderr, "[*] Progress: %d/%d IPs processed (domains found: %d, failed: %d)\n",
				d, total, atomic.LoadInt64(&totalDomains), atomic.LoadInt64(&failed))
		}
	}

	writer.Flush()
	fmt.Fprintf(os.Stderr, "[+] Done. %d/%d succeeded, %d failed, %d unique domains written.\n",
		int64(total)-failed, total, failed, atomic.LoadInt64(&totalDomains))
}

// lookupWithRetry queries ip.thc.org with exponential backoff on HTTP 429
func lookupWithRetry(client *http.Client, ip string, limit int, delay time.Duration, maxRetries int) ([]string, error) {
	url := fmt.Sprintf("%s%s?l=%d&nocolor=1&noheader=1", baseURL, ip, limit)

	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "reverseip-tool/1.1")

		resp, err := client.Do(req)
		if err != nil {
			if attempt < maxRetries {
				backoff := time.Duration(1<<attempt)*time.Second + time.Duration(rand.Intn(500))*time.Millisecond
				time.Sleep(backoff)
				continue
			}
			return nil, err
		}

		if resp.StatusCode == http.StatusTooManyRequests { // 429
			resp.Body.Close()
			if attempt < maxRetries {
				// Check Retry-After header
				waitDuration := time.Duration(2<<attempt)*time.Second + time.Duration(rand.Intn(1000))*time.Millisecond
				if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
					if secs, parseErr := strconv.Atoi(retryAfter); parseErr == nil && secs > 0 {
						waitDuration = time.Duration(secs)*time.Second + 500*time.Millisecond
					}
				}
				fmt.Fprintf(os.Stderr, "[!] Rate limited (429) on %s (attempt %d/%d) — backing off for %v\n",
					ip, attempt+1, maxRetries, waitDuration)
				time.Sleep(waitDuration)
				continue
			}
			return nil, fmt.Errorf("rate limited (429) after %d retries", maxRetries)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}

		domains := parseResponse(resp.Body)
		resp.Body.Close()
		return domains, nil
	}

	return nil, fmt.Errorf("failed after retries")
}

// parseResponse extracts domain names from the plain-text response body.
// The API (with noheader=1) returns one domain per line; lines starting
// with ";" are metadata/comments and are skipped.
func parseResponse(r io.Reader) []string {
	var domains []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		domains = append(domains, line)
	}
	return domains
}

// readLines reads non-empty, non-comment lines from a file.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return lines, scanner.Err()
}
