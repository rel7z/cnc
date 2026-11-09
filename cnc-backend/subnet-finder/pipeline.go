package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	probing "github.com/prometheus-community/pro-bing"
)

type pipelineOptions struct {
	Concurrency, MaxAddresses int
	Timeout                   time.Duration
	StateDir                  string
}
type pipelineDependencies struct {
	Resolve func(context.Context, string) ([]netip.Addr, error)
	Fetch   func(context.Context, netip.Addr) (netip.Prefix, error)
	Probe   func(context.Context, netip.Addr) bool
}

func defaultDependencies(timeout time.Duration) pipelineDependencies {
	transport := &http.Transport{MaxIdleConns: 8, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 4, IdleConnTimeout: 30 * time.Second}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	return pipelineDependencies{
		Resolve: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
		},
		Fetch: func(ctx context.Context, ip netip.Addr) (netip.Prefix, error) {
			request, err := http.NewRequestWithContext(ctx, "GET", "https://stat.ripe.net/data/network-info/data.json?resource="+ip.String(), nil)
			if err != nil {
				return netip.Prefix{}, err
			}
			response, err := client.Do(request)
			if err != nil {
				return netip.Prefix{}, err
			}
			defer response.Body.Close()
			if response.StatusCode != 200 {
				return netip.Prefix{}, fmt.Errorf("RIPE status %d", response.StatusCode)
			}
			var result struct {
				Data struct {
					Prefix string `json:"prefix"`
				} `json:"data"`
			}
			if err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
				return netip.Prefix{}, err
			}
			prefix, err := netip.ParsePrefix(result.Data.Prefix)
			return prefix.Masked(), err
		},
		Probe: func(ctx context.Context, ip netip.Addr) bool {
			for _, privileged := range []bool{true, false} {
				if ctx.Err() != nil {
					return false
				}
				pinger, err := probing.NewPinger(ip.String())
				if err != nil {
					return false
				}
				pinger.SetPrivileged(privileged)
				pinger.Count = 1
				pinger.Timeout = timeout
				pinger.RecordRtts = false
				if err = pinger.RunWithContext(ctx); err == nil {
					return pinger.Statistics().PacketsRecv > 0
				}
			}
			return false
		},
	}
}

// Only IPv4 is supported. Reject large prefixes before walking. Emit one
// compact address at a time; a slow consumer applies channel backpressure.
func walkPrefix(ctx context.Context, prefix netip.Prefix, limit int, emit func(netip.Addr) error) error {
	if !prefix.IsValid() || !prefix.Addr().Is4() {
		return fmt.Errorf("IPv4 subnet required")
	}
	prefix = prefix.Masked()
	bits := prefix.Addr().BitLen() - prefix.Bits()
	if bits >= 63 || uint64(1)<<uint(bits) > uint64(limit) {
		return fmt.Errorf("subnet exceeds %d addresses", limit)
	}
	count := uint64(1) << uint(bits)
	ip := prefix.Addr()
	for i := uint64(0); i < count; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Preserve IPv4 /31 and /32 hosts.
		if count <= 2 || (i > 0 && i < count-1) {
			if err := emit(ip); err != nil {
				return err
			}
		}
		ip = ip.Next()
	}
	return nil
}

func runPipeline(parent context.Context, input io.Reader, output io.Writer, options pipelineOptions, deps pipelineDependencies, ui *ProgressUI) error {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	store, err := newDiskSet(options.StateDir)
	if err != nil {
		return err
	}
	defer store.Close()
	size := min(256, max(1, options.Concurrency*2))
	domains := make(chan string, size)
	ips := make(chan netip.Addr, size)
	prefixes := make(chan netip.Prefix, size)
	probes := make(chan netip.Addr, size)
	var all, dns, fetch, ping sync.WaitGroup
	var outputMu sync.Mutex
	fail := func(err error) {
		if err != nil {
			cancel(err)
		}
	}
	enqueuePrefix := func(prefix netip.Prefix) bool {
		if !prefix.IsValid() || !prefix.Addr().Is4() {
			return true
		}
		fresh, err := store.Add(ctx, 1, prefix.Masked().String())
		if err != nil {
			fail(err)
			return false
		}
		if fresh {
			select {
			case prefixes <- prefix:
				atomic.AddInt64(&ui.fetchedCIDRs, 1)
			case <-ctx.Done():
				return false
			}
		}
		return true
	}
	all.Add(1)
	go func() {
		defer all.Done()
		defer close(domains)
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			atomic.AddInt64(&ui.totalDomains, 1)
			select {
			case domains <- line:
			case <-ctx.Done():
				return
			}
		}
		fail(scanner.Err())
	}()
	for i := 0; i < min(options.Concurrency, 16); i++ {
		dns.Add(1)
		go func() {
			defer dns.Done()
			for host := range domains {
				if ctx.Err() != nil {
					return
				}
				if prefix, err := netip.ParsePrefix(host); err == nil {
					if !enqueuePrefix(prefix) {
						return
					}
					atomic.AddInt64(&ui.resolvedIPs, 1)
					continue
				}
				lookupCtx, stop := context.WithTimeout(ctx, options.Timeout)
				resolved, err := deps.Resolve(lookupCtx, host)
				stop()
				if err == nil {
					for _, ip := range resolved {
						ip = ip.Unmap()
						if !ip.Is4() {
							continue
						}
						fresh, err := store.Add(ctx, 0, ip.String())
						if err != nil {
							fail(err)
							return
						}
						if fresh {
							select {
							case ips <- ip:
							case <-ctx.Done():
								return
							}
						}
					}
				}
				atomic.AddInt64(&ui.resolvedIPs, 1)
			}
		}()
	}
	all.Add(1)
	go func() { defer all.Done(); dns.Wait(); close(ips) }()
	for i := 0; i < min(options.Concurrency, 4); i++ {
		fetch.Add(1)
		go func() {
			defer fetch.Done()
			for ip := range ips {
				if ctx.Err() != nil {
					return
				}
				prefix, err := deps.Fetch(ctx, ip)
				if err == nil && prefix.IsValid() {
					if !enqueuePrefix(prefix) {
						return
					}
				}
				timer := time.NewTimer(100 * time.Millisecond)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					return
				}
			}
		}()
	}
	// Direct prefixes are produced by DNS workers too; close only after both
	// producer groups have exited. This also prevents cancellation send panics.
	all.Add(1)
	go func() { defer all.Done(); dns.Wait(); fetch.Wait(); close(prefixes) }()
	all.Add(1)
	go func() {
		defer all.Done()
		defer close(probes)
		for prefix := range prefixes {
			err := walkPrefix(ctx, prefix, options.MaxAddresses, func(ip netip.Addr) error {
				fresh, err := store.Add(ctx, 2, ip.String())
				if err != nil {
					return err
				}
				if !fresh {
					return nil
				}
				select {
				case probes <- ip:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				if strings.Contains(err.Error(), "exceeds") {
					fmt.Fprintln(os.Stderr, "[!] Skipping subnet:", err)
					continue
				}
				fail(err)
				return
			}
		}
	}()
	for i := 0; i < options.Concurrency; i++ {
		ping.Add(1)
		go func() {
			defer ping.Done()
			for ip := range probes {
				if ctx.Err() != nil {
					return
				}
				if deps.Probe(ctx, ip) {
					outputMu.Lock()
					_, err := fmt.Fprintln(output, ip.String())
					outputMu.Unlock()
					if err != nil {
						fail(err)
						return
					}
					atomic.AddInt64(&ui.validIPs, 1)
				}
				atomic.AddInt64(&ui.pingedIPs, 1)
			}
		}()
	}
	ping.Wait()
	all.Wait()
	return context.Cause(ctx)
}
