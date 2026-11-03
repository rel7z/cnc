package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

const endpoint = "https://api.dnsrift.net/reverse-ip-lookup"
const maxResponseBytes = 32 << 20

type settings struct {
	input, singleIP, output string
	workers, retries        int
	delay, timeout          time.Duration
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr, endpoint); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "[error]", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, apiURL string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var c settings
	flags := flag.NewFlagSet("reverseip-dnsrift", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&c.input, "f", "", "File with one IPv4/IPv6 address per line")
	flags.StringVar(&c.input, "file", "", "Alias for -f")
	flags.StringVar(&c.singleIP, "ip", "", "Look up a single IPv4/IPv6 address")
	flags.StringVar(&c.output, "o", "", "Output .txt file (also streams domains to stdout)")
	flags.StringVar(&c.output, "out", "", "Alias for -o")
	flags.IntVar(&c.workers, "w", 2, "Concurrent requests (1-32)")
	flags.IntVar(&c.retries, "retries", 2, "Retries for rate limits or transient failures (0-5)")
	flags.DurationVar(&c.delay, "delay", 500*time.Millisecond, "Minimum interval between requests per process")
	flags.DurationVar(&c.timeout, "timeout", 20*time.Second, "HTTP request timeout (up to 2m)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if c.input == "" && flags.NArg() == 1 {
		c.input = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if (c.input == "") == (c.singleIP == "") {
		return fmt.Errorf("provide either -f ips.txt or -ip 8.8.8.8")
	}
	if c.workers < 1 || c.workers > 32 || c.retries < 0 || c.retries > 5 || c.delay < 0 || c.timeout <= 0 || c.timeout > 2*time.Minute {
		return fmt.Errorf("invalid worker, retry, delay or timeout setting")
	}
	if c.input != "" && c.output != "" {
		in, _ := filepath.Abs(c.input)
		out, _ := filepath.Abs(c.output)
		if in == out {
			return fmt.Errorf("input and output files must differ")
		}
	}
	var input io.Reader = strings.NewReader(c.singleIP)
	if c.input != "" {
		f, err := os.Open(c.input)
		if err != nil {
			return fmt.Errorf("open input: %w", err)
		}
		defer f.Close()
		input = f
	}
	ips, err := readIPs(input)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return fmt.Errorf("input contains no IP addresses")
	}
	var out io.Writer = stdout
	if c.output != "" {
		f, err := os.Create(c.output)
		if err != nil {
			return fmt.Errorf("create output: %w", err)
		}
		defer f.Close()
		out = io.MultiWriter(f, stdout)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = c.workers
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: c.timeout, Transport: transport}
	pace := &pacer{delay: c.delay}
	type result struct {
		ip      string
		domains []string
		err     error
	}
	jobs := make(chan string)
	results := make(chan result)
	var wg sync.WaitGroup
	for i := 0; i < c.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range jobs {
				domains, err := lookup(ctx, client, apiURL, ip, c.retries, pace)
				select {
				case results <- result{ip, domains, err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, ip := range ips {
			select {
			case jobs <- ip:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	writer := bufio.NewWriter(out)
	seen := map[string]bool{}
	failed, done := 0, 0
	for r := range results {
		done++
		if r.err != nil {
			failed++
			fmt.Fprintf(stderr, "[error] %s: %v\n", r.ip, r.err)
			continue
		}
		for _, domain := range r.domains {
			if !seen[domain] {
				if _, err := fmt.Fprintln(writer, domain); err != nil {
					return fmt.Errorf("write results: %w", err)
				}
				seen[domain] = true
			}
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("flush results: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "[done] %d/%d IPs processed; %d failed; %d unique domains written\n", done, len(ips), failed, len(seen))
	if failed > 0 {
		return fmt.Errorf("%d IP lookup(s) failed; successful partial output was retained", failed)
	}
	return nil
}

func readIPs(r io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(r)
	seen := map[string]bool{}
	var ips []string
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		ip, err := netip.ParseAddr(text)
		if err != nil {
			return nil, fmt.Errorf("invalid IP address at input line %d", line)
		}
		key := ip.String()
		if !seen[key] {
			seen[key] = true
			ips = append(ips, key)
		}
	}
	return ips, scanner.Err()
}

type pacer struct {
	mu    sync.Mutex
	next  time.Time
	delay time.Duration
}

func (p *pacer) wait(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := pause(ctx, time.Until(p.next)); err != nil {
		return err
	}
	p.next = time.Now().Add(p.delay)
	return nil
}
func pause(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func lookup(ctx context.Context, client *http.Client, base, ip string, retries int, pace *pacer) ([]string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("ip", ip)
	u.RawQuery = query.Encode()
	for attempt := 0; attempt <= retries; attempt++ {
		if err := pace.wait(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "CNC-ReverseIP-DNSRift/1.0")
		resp, err := client.Do(req)
		if err != nil {
			if attempt == retries {
				return nil, fmt.Errorf("DNSRift request failed: %w", err)
			}
			if err := pause(ctx, time.Second*time.Duration(1<<attempt)); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			resp.Body.Close()
			if attempt == retries {
				return nil, fmt.Errorf("DNSRift HTTP %d after %d retries", resp.StatusCode, retries)
			}
			wait := time.Second * time.Duration(1<<attempt)
			if value := resp.Header.Get("Retry-After"); value != "" {
				if seconds, e := strconv.Atoi(value); e == nil && seconds >= 0 {
					wait = time.Duration(seconds) * time.Second
				} else if deadline, e := http.ParseTime(value); e == nil {
					wait = time.Until(deadline)
				}
			}
			if wait > 2*time.Minute {
				return nil, fmt.Errorf("DNSRift HTTP %d: Retry-After exceeds the two-minute retry window", resp.StatusCode)
			}
			if err := pause(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("DNSRift HTTP %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read DNSRift response: %w", err)
		}
		if len(data) > maxResponseBytes {
			return nil, fmt.Errorf("DNSRift response exceeds 32 MiB")
		}
		var payload struct {
			IP         string          `json:"ip"`
			DomainList *[]string       `json:"domain_list"`
			Error      json.RawMessage `json:"error"`
			Success    *bool           `json:"success"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, fmt.Errorf("DNSRift returned invalid JSON")
		}
		if (len(payload.Error) > 0 && string(payload.Error) != "null" && string(payload.Error) != "false" && string(payload.Error) != "\"\"") || (payload.Success != nil && !*payload.Success) {
			return nil, fmt.Errorf("DNSRift reported an API error")
		}
		if payload.IP != "" && payload.IP != ip {
			return nil, fmt.Errorf("DNSRift returned a response for a different IP")
		}
		if payload.DomainList == nil {
			return nil, fmt.Errorf("DNSRift response missing domain_list")
		}
		var domains []string
		seen := map[string]bool{}
		for _, raw := range *payload.DomainList {
			d := normalizeDomain(raw)
			if d != "" && !seen[d] {
				seen[d] = true
				domains = append(domains, d)
			}
		}
		if len(*payload.DomainList) > 0 && len(domains) == 0 {
			return nil, fmt.Errorf("DNSRift returned no valid domain records")
		}
		return domains, nil
	}
	return nil, fmt.Errorf("DNSRift retry budget exhausted")
}

func normalizeDomain(raw string) string {
	d := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if len(d) > 253 || !strings.Contains(d, ".") {
		return ""
	}
	if _, err := netip.ParseAddr(d); err == nil {
		return ""
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 {
			return ""
		}
		for _, r := range label {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
				return ""
			}
		}
	}
	return d
}
