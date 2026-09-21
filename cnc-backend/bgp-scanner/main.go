package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	probing "github.com/prometheus-community/pro-bing"
)

type ProgressUI struct {
	totalDomains int64
	resolvedIPs  int64
	fetchedCIDRs int64
	totalIPs     int64
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
	ticker := time.NewTicker(200 * time.Millisecond)
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
	ips := atomic.LoadInt64(&p.totalIPs)
	pinged := atomic.LoadInt64(&p.pingedIPs)
	valid := atomic.LoadInt64(&p.validIPs)
	elapsed := time.Since(p.startTime).Round(time.Second)

	fmt.Fprintf(os.Stderr, "\033[2J\033[HDomains: %d/%d | CIDRs: %d | Total IPs: %d | Pinged: %d | Alive: %d | Elapsed: %v",
		res, dom, cidrs, ips, pinged, valid, elapsed)
}

func (p *ProgressUI) renderFinal() {
	valid := atomic.LoadInt64(&p.validIPs)
	elapsed := time.Since(p.startTime).Round(time.Second)
	fmt.Fprintf(os.Stderr, "\033[2J\033[H[DONE] Found %d valid IPs in %v\n", valid, elapsed)
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

// expandCIDR generates all IP addresses in the given CIDR (excluding network and broadcast if > /31)
func expandCIDR(cidr string) ([]string, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
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
	// Important for linux: use unprivileged datagram ICMP if raw sockets fail.
	// But pro-bing handles it nicely if we set SetPrivileged(true) and we run as root.
	// We'll set Privileged = true for accurate raw ICMP, which is standard for CNC tools running as root.
	pinger.SetPrivileged(true)
	pinger.Count = 1
	pinger.Timeout = timeout

	err = pinger.Run() // Blocks until finished
	if err != nil {
		return false
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
		fmt.Fprintln(os.Stderr, "Usage: bgp-scanner -l domains.txt [-c 100] [-timeout 2s] [-out valid.txt]")
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
		fmt.Fprintln(os.Stderr, "Usage: bgp-scanner -l domains.txt [-c 100] [-timeout 2s] [-out valid.txt]")
		os.Exit(1)
	}

	outF, err := os.Create(outputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output file: %v\n", err)
		os.Exit(1)
	}
	defer outF.Close()

	ui := NewProgressUI()
	atomic.StoreInt64(&ui.totalDomains, int64(len(domains)))
	go ui.Start()

	// 1. Resolve Domains to IPs
	var resolvedIPs []string
	var mu sync.Mutex

	var wg sync.WaitGroup
	domainCh := make(chan string, len(domains))
	for _, d := range domains {
		domainCh <- d
	}
	close(domainCh)

	// Small concurrency for DNS resolution
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range domainCh {
				ips, _ := net.LookupHost(d)
				if len(ips) > 0 {
					mu.Lock()
					resolvedIPs = append(resolvedIPs, ips[0]) // just take the first IP
					mu.Unlock()
				}
				atomic.AddInt64(&ui.resolvedIPs, 1)
			}
		}()
	}
	wg.Wait()

	// Unique IPs
	ipSet := make(map[string]bool)
	for _, ip := range resolvedIPs {
		ipSet[ip] = true
	}
	var uniqueIPs []string
	for ip := range ipSet {
		uniqueIPs = append(uniqueIPs, ip)
	}

	// 2. Fetch CIDRs
	client := &http.Client{Timeout: 10 * time.Second}
	var cidrs []string
	ipCh := make(chan string, len(uniqueIPs))
	for _, ip := range uniqueIPs {
		ipCh <- ip
	}
	close(ipCh)

	// Small concurrency for API so we don't spam RIPE Stat
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range ipCh {
				cidr, err := fetchCIDR(ip, client)
				if err == nil && cidr != "" {
					mu.Lock()
					cidrs = append(cidrs, cidr)
					mu.Unlock()
					atomic.AddInt64(&ui.fetchedCIDRs, 1)
				}
			}
		}()
	}
	wg.Wait()

	// Unique CIDRs
	cidrSet := make(map[string]bool)
	for _, c := range cidrs {
		cidrSet[c] = true
	}

	// 3. Expand CIDRs
	var allIPs []string
	for c := range cidrSet {
		expanded, _ := expandCIDR(c)
		allIPs = append(allIPs, expanded...)
	}

	// Unique expanded IPs
	expandedSet := make(map[string]bool)
	for _, ip := range allIPs {
		expandedSet[ip] = true
	}
	var finalIPs []string
	for ip := range expandedSet {
		finalIPs = append(finalIPs, ip)
	}

	atomic.StoreInt64(&ui.totalIPs, int64(len(finalIPs)))

	// 4. Ping Sweep
	pingCh := make(chan string, len(finalIPs))
	for _, ip := range finalIPs {
		pingCh <- ip
	}
	close(pingCh)

	outMu := sync.Mutex{}
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range pingCh {
				if isAlive(ip, timeout) {
					atomic.AddInt64(&ui.validIPs, 1)
					outMu.Lock()
					fmt.Fprintln(outF, ip)
					outMu.Unlock()
				}
				atomic.AddInt64(&ui.pingedIPs, 1)
			}
		}()
	}
	wg.Wait()

	ui.Stop()
}
