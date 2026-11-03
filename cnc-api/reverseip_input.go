package cnc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/idna"
)

type reverseIPLookup func(context.Context, string) ([]net.IPAddr, error)

// ReverseIPInputSummary describes preprocessing, before any provider is called.
type ReverseIPInputSummary struct {
	InputLines      int      `json:"input_lines"`
	Domains         int      `json:"domains"`
	DuplicateInputs int      `json:"duplicate_inputs"`
	InvalidInputs   int      `json:"invalid_inputs"`
	DNSFailures     int      `json:"dns_failures"`
	DuplicateIPs    int      `json:"duplicate_ips"`
	SkippedIPv6     int      `json:"skipped_ipv6"`
	UniqueIPs       int      `json:"unique_ips"`
	Warnings        []string `json:"warnings,omitempty"`
}

func isReverseIPTool(id string) bool {
	return id == "reverseip-domain" || id == "reverseip-thc" || id == "reverseip-dnsrift"
}

func reverseIPTarget(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if ip, err := netip.ParseAddr(value); err == nil && ip.Zone() == "" {
		return ip.Unmap().String(), nil
	}
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Hostname() == "" {
			return "", fmt.Errorf("expected an IP, domain, or HTTP(S) URL without credentials")
		}
		value = u.Hostname()
	} else if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.TrimSuffix(strings.ToLower(value), ".")
	if ip, err := netip.ParseAddr(value); err == nil && ip.Zone() == "" {
		return ip.Unmap().String(), nil
	}
	host, err := idna.Lookup.ToASCII(value)
	if err != nil || len(host) > 253 || !strings.Contains(host, ".") {
		return "", fmt.Errorf("invalid domain or IP address")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid domain label")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", fmt.Errorf("invalid domain character")
			}
		}
	}
	return host, nil
}

// Resolve each unique hostname once, retain IPv4 addresses only, then dedup
// globally. Stable input order and sorted DNS answers make chunks reproducible.
func normalizeReverseIPInput(ctx context.Context, input io.Reader, output io.Writer, lookup reverseIPLookup) (*ReverseIPInputSummary, error) {
	stats := &ReverseIPInputSummary{}
	warn := func(message string) {
		if len(stats.Warnings) < 10 {
			stats.Warnings = append(stats.Warnings, message)
		}
	}
	var targets []string
	seenTargets := map[string]bool{}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		stats.InputLines++
		target, err := reverseIPTarget(raw)
		if err != nil {
			stats.InvalidInputs++
			warn(fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		if seenTargets[target] {
			stats.DuplicateInputs++
			continue
		}
		seenTargets[target] = true
		targets = append(targets, target)
	}
	if err := scanner.Err(); err != nil {
		return stats, err
	}
	type answer struct {
		ips []netip.Addr
		err error
	}
	answers := make([]answer, len(targets))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if err := ctx.Err(); err != nil {
					answers[i].err = err
					continue
				}
				if ip, err := netip.ParseAddr(targets[i]); err == nil {
					answers[i].ips = []netip.Addr{ip}
					continue
				}
				queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				addresses, err := lookup(queryCtx, targets[i])
				cancel()
				answers[i].err = err
				for _, address := range addresses {
					if ip, ok := netip.AddrFromSlice(address.IP); ok && address.Zone == "" {
						answers[i].ips = append(answers[i].ips, ip.Unmap())
					}
				}
				sort.Slice(answers[i].ips, func(a, b int) bool { return answers[i].ips[a].Less(answers[i].ips[b]) })
			}
		}()
	}
dispatch:
	for i := range targets {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return stats, err
	}
	seenIPs := map[netip.Addr]bool{}
	writer := bufio.NewWriter(output)
	for i, result := range answers {
		if _, err := netip.ParseAddr(targets[i]); err != nil {
			stats.Domains++
		}
		if result.err != nil || len(result.ips) == 0 {
			stats.DNSFailures++
			reason := "no A/AAAA addresses"
			if result.err != nil {
				reason = result.err.Error()
			}
			warn(targets[i] + ": " + reason)
			continue
		}
		for _, ip := range result.ips {
			if !ip.Is4() {
				stats.SkippedIPv6++
				continue
			}
			if seenIPs[ip] {
				stats.DuplicateIPs++
				continue
			}
			seenIPs[ip] = true
			if _, err := fmt.Fprintln(writer, ip.String()); err != nil {
				return stats, err
			}
			stats.UniqueIPs++
		}
	}
	if err := writer.Flush(); err != nil {
		return stats, err
	}
	if stats.UniqueIPs == 0 {
		return stats, fmt.Errorf("reverse IP input produced no IPv4 addresses: %d invalid inputs, %d DNS failures, %d IPv6 skipped", stats.InvalidInputs, stats.DNSFailures, stats.SkippedIPv6)
	}
	return stats, nil
}

func (s *Server) prepareReverseIPJob(job *Job) (string, error) {
	if job.ID == "" || filepath.Base(job.ID) != job.ID || job.ID == "." || job.ID == ".." || strings.ContainsAny(job.ID, "\\") {
		return "", fmt.Errorf("invalid job ID")
	}
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.mu.RLock()
				cancelled := job.Status == "cancelled"
				s.mu.RUnlock()
				if cancelled {
					cancel()
					return
				}
			}
		}
	}()
	input, err := os.Open(job.InputFile)
	if err != nil {
		return "", err
	}
	defer input.Close()
	directory := filepath.Join(s.config.DataDir, "reverseip-jobs", job.ID)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	output, err := os.CreateTemp(directory, ".ips-")
	if err != nil {
		return "", err
	}
	defer os.Remove(output.Name())
	lookup := s.reverseIPLookup
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]net.IPAddr, error) {
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
			addresses := make([]net.IPAddr, 0, len(ips))
			for _, ip := range ips {
				addresses = append(addresses, net.IPAddr{IP: ip})
			}
			return addresses, err
		}
	}
	stats, resolveErr := normalizeReverseIPInput(ctx, input, output, lookup)
	closeErr := output.Close()
	s.mu.Lock()
	job.ReverseIP = stats
	snapshot := *job
	s.mu.Unlock()
	s.broadcast(SSEEvent{Type: SSEEventJob, Payload: snapshot})
	summary, _ := json.Marshal(stats)
	log.Printf("[reverseip] Job %s input preparation: %s", job.ID, summary)
	if resolveErr != nil {
		return "", resolveErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	destination := filepath.Join(directory, "ips.txt")
	if err := os.Rename(output.Name(), destination); err != nil {
		return "", err
	}
	return destination, nil
}
