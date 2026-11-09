package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type WordPressResult struct {
	URL       string    `json:"url"`
	IP        string    `json:"ip"`
	Port      int       `json:"port"`
	Version   string    `json:"version,omitempty"`
	Plugins   []string  `json:"plugins,omitempty"`
	Method    string    `json:"method"`
	Timestamp time.Time `json:"timestamp"`
}

type Discoverer struct {
	client           *http.Client
	results          chan WordPressResult
	wg               sync.WaitGroup
	threadsLimit     int
	timeout          time.Duration
	wpSignatures     []string
	foundCount       int64
	scannedCount     int64
}

var (
	inputFile = flag.String("input", "", "Input file: domains, IPs, or CIDRs")
	outputFile = flag.String("out", "wordpress_sites.json", "Output file")
	threads = flag.Int("t", 2000, "Concurrent threads (aggressive: 1000-5000)")
	timeout = flag.Int("timeout", 5, "HTTP timeout in seconds")
	ports = flag.String("ports", "80,443,8080,8888,8081", "Ports to scan")
	aggressiveness = flag.String("level", "max", "Aggressiveness: low/medium/high/max")
	ctLogs = flag.Bool("ct", true, "Enable Certificate Transparency log scanning")
	cidrScan = flag.Bool("cidr", false, "Enable aggressive CIDR/IP range scanning")
	dnsRevLookup = flag.Bool("dns-rev", true, "Enable aggressive reverse DNS lookup")
)

func main() {
	flag.Parse()

	if *inputFile == "" {
		log.Fatal("Usage: wp-discovery -input <file> -t <threads> [options]")
	}

	d := &Discoverer{
		client: &http.Client{
			Timeout: time.Duration(*timeout) * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        *threads,
				MaxIdleConnsPerHost: 100,
				MaxConnsPerHost:     100,
				DialContext: (&net.Dialer{
					Timeout:   time.Duration(*timeout) * time.Second,
					KeepAlive: 10 * time.Second,
				}).DialContext,
			},
		},
		results:       make(chan WordPressResult, 10000),
		threadsLimit:  *threads,
		timeout:       time.Duration(*timeout) * time.Second,
	}

	// Aggressive WordPress signatures to detect
	d.wpSignatures = []string{
		"wp-content",
		"wp-includes",
		"wp-admin",
		"wordpress",
		"wp-json",
		"xmlrpc.php",
		"wp-login.php",
	}

	fmt.Printf("[*] Starting WordPress Discovery (Aggressive Mode)\n")
	fmt.Printf("[*] Threads: %d | Timeout: %ds | Input: %s\n", *threads, *timeout, *inputFile)
	fmt.Printf("[*] CT Logs: %v | CIDR Scan: %v | Reverse DNS: %v\n", *ctLogs, *cidrScan, *dnsRevLookup)

	// Start result writer
	outputChan := make(chan WordPressResult, 10000)
	go d.writeResults(outputChan)

	// Launch discovery methods concurrently
	var scanWg sync.WaitGroup

	// Method 1: Direct scanning from input file
	scanWg.Add(1)
	go func() {
		defer scanWg.Done()
		d.scanFromFile(*inputFile, outputChan)
	}()

	// Method 2: Reverse DNS on discovered IPs
	if *dnsRevLookup {
		scanWg.Add(1)
		go func() {
			defer scanWg.Done()
			d.reverseIpLookup(*inputFile, outputChan)
		}()
	}

	// Method 3: CIDR range scanning (aggressive)
	if *cidrScan {
		scanWg.Add(1)
		go func() {
			defer scanWg.Done()
			d.cidrRangeScan(*inputFile, outputChan)
		}()
	}

	// Method 4: Certificate Transparency logs
	if *ctLogs {
		scanWg.Add(1)
		go func() {
			defer scanWg.Done()
			d.ctLogDiscovery(*inputFile, outputChan)
		}()
	}

	// Wait for all discovery methods to finish
	go func() {
		scanWg.Wait()
		close(d.results)
	}()

	// Aggregate results
	for result := range d.results {
		outputChan <- result
		atomic.AddInt64(&d.foundCount, 1)
	}

	close(outputChan)

	fmt.Printf("\n[+] Discovery Complete!\n")
	fmt.Printf("[+] Found: %d WordPress sites | Scanned: %d targets\n", atomic.LoadInt64(&d.foundCount), atomic.LoadInt64(&d.scannedCount))
	fmt.Printf("[+] Results saved to: %s\n", *outputFile)
}

// scanFromFile - direct aggressive scanning of targets
func (d *Discoverer) scanFromFile(filename string, out chan WordPressResult) {
	file, err := os.Open(filename)
	if err != nil {
		log.Printf("[-] Error opening file: %v\n", err)
		return
	}
	defer file.Close()

	portList := parsePortList(*ports)
	semaphore := make(chan struct{}, d.threadsLimit)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		target := strings.TrimSpace(scanner.Text())
		if target == "" {
			continue
		}

		// For each target, probe multiple ports concurrently
		for _, port := range portList {
			d.wg.Add(1)
			go func(t string, p int) {
				defer d.wg.Done()
				semaphore <- struct{}{}        // Acquire
				defer func() { <-semaphore }() // Release

				d.probeTarget(t, p, "direct")
			}(target, port)
		}
	}

	d.wg.Wait()
}

// reverseIpLookup - reverse DNS on IPs to find subdomains
func (d *Discoverer) reverseIpLookup(filename string, out chan WordPressResult) {
	file, err := os.Open(filename)
	if err != nil {
		return
	}
	defer file.Close()

	semaphore := make(chan struct{}, d.threadsLimit)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		target := strings.TrimSpace(scanner.Text())
		if target == "" {
			continue
		}

		d.wg.Add(1)
		go func(ip string) {
			defer d.wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Aggressive reverse DNS
			names, err := net.LookupAddr(ip)
			if err != nil {
				return
			}

			for _, name := range names {
				d.probeTarget(strings.TrimSuffix(name, "."), 80, "reverse-dns")
				d.probeTarget(strings.TrimSuffix(name, "."), 443, "reverse-dns")
			}
		}(target)
	}

	d.wg.Wait()
}

// cidrRangeScan - aggressive CIDR range scanning
func (d *Discoverer) cidrRangeScan(filename string, out chan WordPressResult) {
	file, err := os.Open(filename)
	if err != nil {
		return
	}
	defer file.Close()

	semaphore := make(chan struct{}, d.threadsLimit)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		cidr := strings.TrimSpace(scanner.Text())
		if cidr == "" {
			continue
		}

		// Parse CIDR notation (e.g., "192.168.1.0/24")
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			// If not CIDR, try as single IP with /32
			ip := net.ParseIP(cidr)
			if ip == nil {
				continue
			}
			ipNet = &net.IPNet{IP: ip, Mask: net.CIDRMask(32, 32)}
		}

		// Aggressive scanning of CIDR range
		for ip := ipNet.IP.Mask(ipNet.Mask); ipNet.Contains(ip); incrementIP(ip) {
			d.wg.Add(1)
			go func(addr string) {
				defer d.wg.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				d.probeTarget(addr, 80, "cidr-scan")
				d.probeTarget(addr, 443, "cidr-scan")
			}(ip.String())
		}
	}

	d.wg.Wait()
}

// ctLogDiscovery - real-time Certificate Transparency log scanning
func (d *Discoverer) ctLogDiscovery(filename string, out chan WordPressResult) {
	file, err := os.Open(filename)
	if err != nil {
		return
	}
	defer file.Close()

	semaphore := make(chan struct{}, d.threadsLimit)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		domain := strings.TrimSpace(scanner.Text())
		if domain == "" {
			continue
		}

		d.wg.Add(1)
		go func(d_ string) {
			defer d.wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Query CT logs for subdomains (simplified - would use real CT API)
			variations := []string{
				d_,
				"www." + d_,
				"wordpress." + d_,
				"blog." + d_,
				"admin." + d_,
				"site." + d_,
				"web." + d_,
				"mail." + d_,
				"ftp." + d_,
				"api." + d_,
			}

			for _, v := range variations {
				d.probeTarget(v, 80, "ct-log")
				d.probeTarget(v, 443, "ct-log")
			}
		}(domain)
	}

	d.wg.Wait()
}

// probeTarget - aggressive WordPress detection on single target
func (d *Discoverer) probeTarget(target string, port int, method string) {
	atomic.AddInt64(&d.scannedCount, 1)

	url := fmt.Sprintf("http://%s:%d", target, port)
	if port == 443 {
		url = fmt.Sprintf("https://%s:%d", target, port)
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/wp-admin/", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := d.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	// WordPress signatures detection
	if resp.StatusCode < 400 {
		// Check for WordPress signatures
		isWP := strings.Contains(resp.Header.Get("Server"), "WordPress") ||
			strings.Contains(resp.Header.Get("X-Powered-By"), "WordPress") ||
			resp.StatusCode == 302 // WordPress redirects

		if isWP || resp.StatusCode == 200 {
			result := WordPressResult{
				URL:       url,
				IP:        getIPFromURL(target),
				Port:      port,
				Method:    method,
				Timestamp: time.Now(),
			}

			select {
			case d.results <- result:
			default:
				// Buffer full, drop result
			}
		}
	}
}

// Helper functions
func parsePortList(portStr string) []int {
	var ports []int
	for _, p := range strings.Split(portStr, ",") {
		var port int
		fmt.Sscanf(strings.TrimSpace(p), "%d", &port)
		if port > 0 && port < 65536 {
			ports = append(ports, port)
		}
	}
	return ports
}

func incrementIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] > 0 {
			break
		}
	}
}

func getIPFromURL(target string) string {
	// Try to resolve target to IP
	ips, err := net.LookupIP(target)
	if err == nil && len(ips) > 0 {
		return ips[0].String()
	}
	return target
}

func (d *Discoverer) writeResults(out chan WordPressResult) {
	f, err := os.Create(*outputFile)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	f.WriteString("[\n")
	first := true

	for result := range out {
		if !first {
			f.WriteString(",\n")
		}
		first = false

		// JSON output
		fmt.Fprintf(f, `  {"url":"%s","ip":"%s","port":%d,"method":"%s","timestamp":"%s"}`,
			result.URL, result.IP, result.Port, result.Method, result.Timestamp.Format(time.RFC3339))
	}

	f.WriteString("\n]\n")
}
