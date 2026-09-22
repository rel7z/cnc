package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	probing "github.com/prometheus-community/pro-bing"
)

type ProgressUI struct {
	totalDomains int64
	resolvedIPs  int64
	fetchedCIDRs int64
	pingedIPs    int64
	validIPs     int64
	startTime    time.Time
	done         chan struct{}
}

func NewProgressUI() *ProgressUI {
	return &ProgressUI{
		startTime: time.Now(),
		done:      make(chan struct{}),
	}
}

func (p *ProgressUI) Start() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.render()
		case <-p.done:
			p.renderFinal()
			return
		}
	}
}

func (p *ProgressUI) Stop() {
	close(p.done)
}

func (p *ProgressUI) render() {
	dom := atomic.LoadInt64(&p.totalDomains)
	res := atomic.LoadInt64(&p.resolvedIPs)
	cidrs := atomic.LoadInt64(&p.fetchedCIDRs)
	pinged := atomic.LoadInt64(&p.pingedIPs)
	valid := atomic.LoadInt64(&p.validIPs)
	elapsed := time.Since(p.startTime).Round(time.Second)

	fmt.Fprintf(os.Stderr, "\r[*] Domains: %d/%d | CIDRs: %d | Pinged: %d | Alive: %d | Elapsed: %v   ",
		res, dom, cidrs, pinged, valid, elapsed)
}

func (p *ProgressUI) renderFinal() {
	valid := atomic.LoadInt64(&p.validIPs)
	elapsed := time.Since(p.startTime).Round(time.Second)
	fmt.Fprintf(os.Stderr, "\n[+] Done. Found %d valid alive IPs in %v\n", valid, elapsed)
}

// RipeStatResponse models the JSON from stat.ripe.net/data/network-info/data.json
type RipeStatResponse struct {
	Data struct {
		Prefix string `json:"prefix"`
	} `json:"data"`
}

func fetchCIDR(ip string, client *http.Client) (string, error) {
	url := fmt.Sprintf("https://stat.ripe.net/data/network-info/data.json?resource=%s", ip)
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status code %d", resp.StatusCode)
	}

	var data RipeStatResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}

	if data.Data.Prefix == "" {
		return "", fmt.Errorf("no prefix found")
	}

	return data.Data.Prefix, nil
}

func expandCIDR(cidr string) ([]string, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	ones, bits := ipnet.Mask.Size()
	// Restrict to /20 (4096 IPs) max to prevent huge cloud block expansions
	if bits == 32 && ones < 20 {
		return nil, fmt.Errorf("subnet %s too large (/%d), skipping", cidr, ones)
	}

	var ips []string
	for ip := ip.Mask(ipnet.Mask); ipnet.Contains(ip); inc(ip) {
		ips = append(ips, ip.String())
	}

	// Remove network and broadcast addresses for typical subnets
	if len(ips) > 2 {
		ips = ips[1 : len(ips)-1]
	}

	return ips, nil
}

func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func isAlive(ip string, timeout time.Duration) bool {
	pinger, err := probing.NewPinger(ip)
	if err != nil {
		return false
	}
	pinger.SetPrivileged(true)
	pinger.Count = 1
	pinger.Timeout = timeout

	err = pinger.Run() // Blocks until finished
	if err != nil {
		// Fallback to unprivileged datagram ICMP if raw sockets are restricted
		pinger, err = probing.NewPinger(ip)
		if err != nil {
			return false
		}
		pinger.SetPrivileged(false)
		pinger.Count = 1
		pinger.Timeout = timeout
		if err := pinger.Run(); err != nil {
			return false
		}
	}
	stats := pinger.Statistics()
	return stats.PacketsRecv > 0
}

func main() {
	var (
		fileInput   string
		concurrency int
		timeout     time.Duration
		outputFile  string
	)

	flag.StringVar(&fileInput, "file", "", "File containing domains")
	flag.StringVar(&fileInput, "l", "", "File containing domains")
	flag.IntVar(&concurrency, "c", runtime.NumCPU()*10, "Number of concurrent workers")
	flag.DurationVar(&timeout, "timeout", 2*time.Second, "Ping timeout per IP")
	flag.StringVar(&outputFile, "out", "valid.txt", "Output file for valid IPs")
	flag.Parse()

	if fileInput == "" {
		fmt.Fprintln(os.Stderr, "Usage: subnet-finder -l domains.txt [-c 100] [-timeout 2s] [-out valid.txt]")
		os.Exit(1)
	}

	data, err := os.ReadFile(fileInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	var domains []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		domains = append(domains, line)
	}

	if len(domains) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: subnet-finder -l domains.txt [-c 100] [-timeout 2s] [-out valid.txt]")
		os.Exit(1)
	}

	outF, err := os.Create(outputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output file: %v\n", err)
		os.Exit(1)
	}
	defer outF.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\n[!] Received interrupt, shutting down gracefully...")
		cancel()
	}()

	ui := NewProgressUI()
	atomic.StoreInt64(&ui.totalDomains, int64(len(domains)))
	go ui.Start()

	// Pipeline channels
	domainCh := make(chan string, len(domains))
	ipToCidrCh := make(chan string, 1000)
	cidrToExpandCh := make(chan string, 1000)
	ipToPingCh := make(chan string, 10000)

	var dnsWg, ripeWg, expandWg, pingWg sync.WaitGroup

	var seenIPs sync.Map
	var seenCIDRs sync.Map
	var seenExpandedIPs sync.Map
	outMu := sync.Mutex{}

	// Stage 1: DNS Resolution
	for i := 0; i < 50; i++ {
		dnsWg.Add(1)
		go func() {
			defer dnsWg.Done()
			for d := range domainCh {
				select {
				case <-ctx.Done():
					return
				default:
				}
				ips, _ := net.LookupHost(d)
				for _, ip := range ips {
					if _, loaded := seenIPs.LoadOrStore(ip, true); !loaded {
						select {
						case ipToCidrCh <- ip:
						case <-ctx.Done():
							return
						}
					}
				}
				atomic.AddInt64(&ui.resolvedIPs, 1)
			}
		}()
	}

	// Stage 2: Fetch CIDRs from RIPE Stat
	client := &http.Client{Timeout: 10 * time.Second}
	for i := 0; i < 10; i++ {
		ripeWg.Add(1)
		go func() {
			defer ripeWg.Done()
			for ip := range ipToCidrCh {
				select {
				case <-ctx.Done():
					return
				default:
				}
				cidr, err := fetchCIDR(ip, client)
				if err == nil && cidr != "" {
					if _, loaded := seenCIDRs.LoadOrStore(cidr, true); !loaded {
						select {
						case cidrToExpandCh <- cidr:
						case <-ctx.Done():
							return
						}
						atomic.AddInt64(&ui.fetchedCIDRs, 1)
					}
				}
				time.Sleep(100 * time.Millisecond) // Rate limit
			}
		}()
	}

	// Stage 3: Expand CIDRs
	for i := 0; i < 2; i++ {
		expandWg.Add(1)
		go func() {
			defer expandWg.Done()
			for cidr := range cidrToExpandCh {
				select {
				case <-ctx.Done():
					return
				default:
				}
				expanded, err := expandCIDR(cidr)
				if err == nil {
					for _, ip := range expanded {
						if _, loaded := seenExpandedIPs.LoadOrStore(ip, true); !loaded {
							select {
							case ipToPingCh <- ip:
							case <-ctx.Done():
								return
							}
						}
					}
				}
			}
		}()
	}

	// Stage 4: Ping Sweep
	for i := 0; i < concurrency; i++ {
		pingWg.Add(1)
		go func() {
			defer pingWg.Done()
			for ip := range ipToPingCh {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if isAlive(ip, timeout) {
					atomic.AddInt64(&ui.validIPs, 1)
					outMu.Lock()
					fmt.Fprintln(outF, ip)
					fmt.Println(ip) // Streams directly to stdout (worker picks this up and streams to server)
					outMu.Unlock()
				}
				atomic.AddInt64(&ui.pingedIPs, 1)
			}
		}()
	}

	// Coordinator
	go func() {
		// Feed domains
		for _, d := range domains {
			select {
			case domainCh <- d:
			case <-ctx.Done():
				break
			}
		}
		close(domainCh)

		// Wait for stages to finish sequentially closing downstream channels
		dnsWg.Wait()
		close(ipToCidrCh)

		ripeWg.Wait()
		close(cidrToExpandCh)

		expandWg.Wait()
		close(ipToPingCh)
	}()

	// Wait for pings to finish
	pingWg.Wait()
	ui.Stop()
}
