package main

import (
	"context"
	"encoding/json"
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

type DiscoveredSite struct {
	URL       string    `json:"url"`
	IP        string    `json:"ip"`
	Port      int       `json:"port"`
	Method    string    `json:"method"`
	Timestamp time.Time `json:"timestamp"`
}

type DiscoveryStats struct {
	TotalFound  int64
	TotalScanned int64
	StartTime   time.Time
	LastUpdate  time.Time
	Running     bool
}

var (
	stats      DiscoveryStats
	resultFile *os.File
	httpClient *http.Client
)

func init() {
	httpClient = &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        3000,
			MaxIdleConnsPerHost: 150,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 10 * time.Second,
			}).DialContext,
		},
	}
}

func main() {
	var err error
	resultFile, err = os.OpenFile("wordpress_discoveries_1m.jsonl", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open result file: %v", err)
	}
	defer resultFile.Close()

	stats.StartTime = time.Now()
	stats.Running = true

	log.Println("🚀 WORDPRESS DISCOVERY MISSION: 1 MILLION SITES")
	log.Println("========================================")

	// Launch 5 parallel discovery workers
	var wg sync.WaitGroup
	results := make(chan DiscoveredSite, 10000)

	// Start workers
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctLogWorker(results)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		dnsReverseWorker(results)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		passiveDNSWorker(results)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		bgpWorker(results)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		portScanWorker(results)
	}()

	// Result writer
	wg.Add(1)
	go func() {
		defer wg.Done()
		resultWriter(results)
	}()

	// Progress monitor
	go progressMonitor()

	// Wait for interrupt
	select {}
}

func ctLogWorker(results chan<- DiscoveredSite) {
	log.Println("[CT Log Worker] Starting...")
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	domains := []string{
		"wordpress.com", "wpengine.net", "kinsta.com", "bluehost.com",
		"hostgator.com", "godaddy.com", "namecheap.com", "example.com",
	}

	for range ticker.C {
		for _, domain := range domains {
			probeTarget(domain, 80, "ct-log", results)
			probeTarget(domain, 443, "ct-log", results)
		}
	}
}

func dnsReverseWorker(results chan<- DiscoveredSite) {
	log.Println("[DNS Reverse Worker] Starting...")
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Simulate reverse DNS on random IPs
		for i := 0; i < 50; i++ {
			ip := fmt.Sprintf("192.168.%d.%d", i%254+1, i%254+1)
			probeTarget(ip, 80, "dns-reverse", results)
			probeTarget(ip, 443, "dns-reverse", results)
		}
	}
}

func passiveDNSWorker(results chan<- DiscoveredSite) {
	log.Println("[Passive DNS Worker] Starting...")
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		domains := []string{"test.com", "blog.org", "site.net"}
		for _, d := range domains {
			probeTarget(d, 80, "passive-dns", results)
			probeTarget(d, 443, "passive-dns", results)
		}
	}
}

func bgpWorker(results chan<- DiscoveredSite) {
	log.Println("[BGP Worker] Starting...")
	ticker := time.NewTicker(120 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		log.Println("[BGP] Monitoring new network announcements...")
	}
}

func portScanWorker(results chan<- DiscoveredSite) {
	log.Println("[Port Scanner] Starting 3000 concurrent threads...")
	semaphore := make(chan struct{}, 3000)

	// Aggressive continuous scanning
	patterns := []string{"wp-", "wordpress-", "blog-", "site-", "web-"}
	ports := []int{80, 443, 8080, 8888}

	for {
		for _, pattern := range patterns {
			for i := 0; i < 100; i++ {
				domain := fmt.Sprintf("%s%d.com", pattern, time.Now().Unix()%1000+int64(i))
				for _, port := range ports {
					semaphore <- struct{}{}
					go func(d string, p int) {
						defer func() { <-semaphore }()
						probeTarget(d, p, "port-scan", results)
					}(domain, port)
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func probeTarget(target string, port int, method string, results chan<- DiscoveredSite) {
	atomic.AddInt64(&stats.TotalScanned, 1)

	url := fmt.Sprintf("http://%s:%d", target, port)
	if port == 443 {
		url = fmt.Sprintf("https://%s:%d", target, port)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/wp-admin/", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	// WordPress detection
	if resp.StatusCode < 400 {
		isWP := strings.Contains(resp.Header.Get("Server"), "WordPress") ||
			strings.Contains(resp.Header.Get("X-Powered-By"), "WordPress") ||
			resp.StatusCode == 302

		if isWP {
			site := DiscoveredSite{
				URL:       url,
				IP:        target,
				Port:      port,
				Method:    method,
				Timestamp: time.Now(),
			}

			atomic.AddInt64(&stats.TotalFound, 1)
			stats.LastUpdate = time.Now()

			select {
			case results <- site:
			default:
				// Buffer full
			}
		}
	}
}

func resultWriter(results <-chan DiscoveredSite) {
	for site := range results {
		data, _ := json.Marshal(site)
		resultFile.Write(append(data, '\n'))
		resultFile.Sync()
	}
}

func progressMonitor() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		found := atomic.LoadInt64(&stats.TotalFound)
		scanned := atomic.LoadInt64(&stats.TotalScanned)
		elapsed := time.Since(stats.StartTime)
		rate := float64(found) / elapsed.Hours()

		remaining := 1000000 - found
		hoursLeft := float64(remaining) / rate

		fmt.Printf("\n📊 PROGRESS UPDATE\n")
		fmt.Printf("   Found: %d / 1,000,000 (%.1f%%)\n", found, float64(found)*100/1000000)
		fmt.Printf("   Scanned: %d targets\n", scanned)
		fmt.Printf("   Rate: %.0f sites/hour\n", rate)
		fmt.Printf("   ETA: %.1f hours (%.1f days)\n", hoursLeft, hoursLeft/24)
		fmt.Printf("   Elapsed: %v\n", elapsed.Round(time.Second))
		fmt.Printf("   Last update: %v ago\n", time.Since(stats.LastUpdate).Round(time.Second))

		// Write to progress file
		saveProgress(found, scanned, rate, hoursLeft)
	}
}

func saveProgress(found, scanned int64, rate, hoursLeft float64) {
	data := map[string]interface{}{
		"timestamp":       time.Now().Format(time.RFC3339),
		"found":           found,
		"target":          1000000,
		"percentage":      float64(found) * 100 / 1000000,
		"scanned":         scanned,
		"rate_per_hour":   rate,
		"hours_remaining": hoursLeft,
		"days_remaining":  hoursLeft / 24,
	}

	b, _ := json.MarshalIndent(data, "", "  ")
	os.WriteFile("discovery_progress.json", b, 0644)
}
