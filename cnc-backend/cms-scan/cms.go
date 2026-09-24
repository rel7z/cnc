package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// maxBodyBytes caps how much of each HTTP response body is read into memory.
// 512KB is enough to cover all CMS fingerprint signals while bounding
// worst-case memory to ~150MB at 300 concurrency.
const maxBodyBytes = 512 * 1024

// cmsIndicatorRaw holds the raw string definitions for each CMS.
// These are compiled into cmsIndicatorCompiled once at init time.
type cmsIndicatorRaw struct {
	patterns     []string
	minMatches   int
	headerChecks map[string]string
	metaChecks   []string
	cookieChecks []string
}

// cmsIndicator holds precompiled regex patterns for efficient matching.
type cmsIndicator struct {
	compiledPatterns []*regexp.Regexp
	minMatches       int
	headerChecks     map[string]string
	compiledMeta     []*regexp.Regexp
	cookieChecks     []string
}

// cmsSignaturesRaw contains the raw string definitions, compiled once in init().
var cmsSignaturesRaw = map[string]cmsIndicatorRaw{
	"wordpress": {
		patterns:   []string{`wp-content`, `wp-includes`, `wp-json`, `xmlrpc\.php`, `wp-login\.php`, `generator.*wordpress`, `readme\.html.*wordpress`},
		minMatches: 2,
		headerChecks: map[string]string{
			"Link":         `rel="https://api.w.org/"`,
			"X-Powered-By": `wordpress`,
		},
		metaChecks:   []string{`name="generator".*wordpress`, `property="og:site_name".*wordpress`},
		cookieChecks: []string{`wordpress_`, `wp-settings-`},
	},
	"joomla": {
		patterns:   []string{`joomla`, `com_content`, `com_users`, `com_config`, `powered.*joomla`, `joomla\.css`, `joomla\.js`},
		minMatches: 2,
		headerChecks: map[string]string{
			"X-Powered-By": `joomla`,
		},
		metaChecks:   []string{`name="generator".*joomla`, `name="joomla"`},
		cookieChecks: []string{`joomla_`, `jfcookie`},
	},
	"drupal": {
		patterns:   []string{`drupal`, `sites/default`, `misc/drupal`, `core/modules`, `powered.*drupal`, `drupal\.js`, `drupal\.css`},
		minMatches: 2,
		headerChecks: map[string]string{
			"X-Powered-By":   `drupal`,
			"X-Drupal-Cache": ``,
			"X-Generator":    `drupal`,
		},
		metaChecks:   []string{`name="generator".*drupal`, `drupal\.settings`},
		cookieChecks: []string{`sess`, `drupal_`, `has_js`},
	},
	"shopify": {
		patterns:   []string{`checkout\.shopify`, `cdn\.shopify`, `shopify\.theme`, `shopify_checkout`, `myshopify\.com`},
		minMatches: 1,
		headerChecks: map[string]string{
			"X-Shopify-Store": ``,
			"X-Shopify-Stage": ``,
			"Server":          `shopify`,
		},
		metaChecks:   []string{`shopify`, `content_for_header`},
		cookieChecks: []string{`_shopify_`, `cart_sig`, `secure_customer_sig`},
	},
	"ghost": {
		patterns:   []string{`ghost\.org`, `ghost\.min\.js`, `ghost\.min\.css`, `/ghost/`, `content/api`},
		minMatches: 1,
		headerChecks: map[string]string{
			"X-Powered-By": `ghost`,
		},
		metaChecks:   []string{`name="generator".*ghost`, `ghost-head`},
		cookieChecks: []string{`ghost`},
	},
	"magento": {
		patterns:   []string{`magento`, `mage/cookies`, `varien`, `magento\.js`, `magento\.css`, `skin/frontend`},
		minMatches: 2,
		headerChecks: map[string]string{
			"X-Magento-Tags":          ``,
			"X-Magento-Cache-Control": ``,
		},
		metaChecks:   []string{`name="generator".*magento`},
		cookieChecks: []string{`magento`, `frontend`, `adminhtml`},
	},
	"wix": {
		patterns:   []string{`wix\.com`, `wixstatic`, `_wix`, `wixcode`, `wix-site`},
		minMatches: 1,
		headerChecks: map[string]string{
			"X-Wix-Request-Id": ``,
			"Server":           `wix`,
		},
		metaChecks:   []string{`wix`},
		cookieChecks: []string{`wix`},
	},
	"squarespace": {
		patterns:   []string{`squarespace`, `static1\.squarespace`, `squarespace\.com`, `sqsp`},
		minMatches: 1,
		headerChecks: map[string]string{
			"X-Squarespace-Request-Id": ``,
		},
		metaChecks:   []string{`squarespace`},
		cookieChecks: []string{`squarespace`, `ss_cid`, `ss_cvisit`},
	},
}

// cmsSignatures holds the compiled version of cmsSignaturesRaw, populated by init().
var cmsSignatures map[string]cmsIndicator

func init() {
	cmsSignatures = make(map[string]cmsIndicator, len(cmsSignaturesRaw))
	for name, raw := range cmsSignaturesRaw {
		compiled := cmsIndicator{
			minMatches:   raw.minMatches,
			headerChecks: raw.headerChecks,
			cookieChecks: raw.cookieChecks,
		}

		compiled.compiledPatterns = make([]*regexp.Regexp, len(raw.patterns))
		for i, p := range raw.patterns {
			compiled.compiledPatterns[i] = regexp.MustCompile(p)
		}

		compiled.compiledMeta = make([]*regexp.Regexp, len(raw.metaChecks))
		for i, m := range raw.metaChecks {
			compiled.compiledMeta[i] = regexp.MustCompile(m)
		}

		cmsSignatures[name] = compiled
	}
}

func detectCMS(body string, headers http.Header, client *http.Client, url string) string {
	bodyLower := strings.ToLower(body)

	type cmsScore struct {
		name  string
		score int
	}

	var scores []cmsScore

	for cms, indicator := range cmsSignatures {
		score := 0

		// Check body patterns using precompiled regexes
		patternMatches := 0
		for _, re := range indicator.compiledPatterns {
			if re.MatchString(bodyLower) {
				patternMatches++
			}
		}

		if patternMatches < indicator.minMatches {
			continue
		}
		score += patternMatches * 10

		// Check headers
		for header, expected := range indicator.headerChecks {
			headerVal := strings.ToLower(headers.Get(header))
			if headerVal != "" {
				if expected == "" || strings.Contains(headerVal, strings.ToLower(expected)) {
					score += 20
				}
			}
		}

		// Check meta tags using precompiled regexes
		for _, re := range indicator.compiledMeta {
			if re.MatchString(bodyLower) {
				score += 15
			}
		}

		// Check cookies
		cookies := strings.ToLower(headers.Get("Set-Cookie"))
		for _, cookiePattern := range indicator.cookieChecks {
			if strings.Contains(cookies, strings.ToLower(cookiePattern)) {
				score += 10
			}
		}

		if score > 0 {
			scores = append(scores, cmsScore{name: cms, score: score})
		}
	}

	// WordPress-specific: check readme.html
	if wpScore := checkWordPressReadme(client, url); wpScore > 0 {
		scores = append(scores, cmsScore{name: "wordpress", score: wpScore})
	}

	if len(scores) == 0 {
		return "unknown"
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	if scores[0].score < 25 {
		return "unknown"
	}

	return scores[0].name
}

func checkWordPressReadme(client *http.Client, baseURL string) int {
	readmeURL := strings.TrimSuffix(baseURL, "/") + "/readme.html"
	req, err := http.NewRequest("GET", readmeURL, nil)
	if err != nil {
		return 0
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	bodyLower := strings.ToLower(string(body))

	if strings.Contains(bodyLower, "wordpress") {
		return 50
	}
	return 0
}

func scanURL(inputURL string, files map[string]*os.File, fileMutex *sync.Mutex, processed *int64, client *http.Client) {
	// Auto-convert domain to URL if needed
	url := inputURL
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	resp, err := client.Do(req)

	var cmsType string

	if err != nil {
		cmsType = "unknown"
	} else {
		defer resp.Body.Close()
		// Limit body read to maxBodyBytes to prevent unbounded memory use
		// at high concurrency. Truncation is safe: CMS signals are matched
		// on whatever bytes are available.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		cmsType = detectCMS(string(body), resp.Header, client, url)
	}

	// Write to file and stdout
	fileMutex.Lock()
	file := files[cmsType]
	if file == nil {
		file = files["unknown"]
	}
	file.WriteString(url + "\n")
	if cmsType != "unknown" {
		fmt.Printf("%s|%s\n", url, cmsType)
	}
	fileMutex.Unlock()

	// Update counter
	count := atomic.AddInt64(processed, 1)
	if count%100 == 0 {
		fmt.Fprintf(os.Stderr, "[*] Processed: %d targets\n", count)
	}
}

// worker pulls URLs from the jobs channel and scans each one.
// Using a fixed worker pool means only `concurrency` goroutines ever exist,
// so memory usage stays flat regardless of how large the input file is.
// Each worker gets its own HTTP client to enable connection reuse within that worker.
func worker(jobs <-chan string, files map[string]*os.File, fileMutex *sync.Mutex, wg *sync.WaitGroup, processed *int64) {
	defer wg.Done()

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	for inputURL := range jobs {
		scanURL(inputURL, files, fileMutex, processed, client)
	}
}

func main() {
	var inputFile string
	flag.StringVar(&inputFile, "f", "", "Input file with URLs (one per line)")
	flag.StringVar(&inputFile, "l", "", "Input file with URLs (alias for -f)")
	outputDir := flag.String("o", "./results", "Output directory")
	var concurrency int
	flag.IntVar(&concurrency, "c", 50, "Concurrent requests")
	flag.IntVar(&concurrency, "t", 50, "Concurrent requests / threads (alias for -c)")
	help := flag.Bool("h", false, "Show help")
	flag.Parse()

	if *help || inputFile == "" {
		fmt.Fprintln(os.Stderr, "Usage: cms-scan -l <input_file> -o <output_dir> -t <threads>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		fmt.Fprintln(os.Stderr, "  -l, -f string   Input file with URLs (required)")
		fmt.Fprintln(os.Stderr, "  -o string       Output directory (default: ./results)")
		fmt.Fprintln(os.Stderr, "  -t, -c int      Concurrent requests / threads (default: 50)")
		fmt.Fprintln(os.Stderr, "  -h              Show this help message")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Example:")
		fmt.Fprintln(os.Stderr, "  cms-scan -l urls.txt -o ./results -t 50")
		os.Exit(0)
	}

	// Validate input file
	if _, err := os.Stat(inputFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error: input file not found: %s\n", inputFile)
		os.Exit(1)
	}

	// Create output directory
	os.MkdirAll(*outputDir, 0755)

	// Create output files
	files := make(map[string]*os.File)
	for cms := range cmsSignatures {
		fpath := filepath.Join(*outputDir, cms+".txt")
		f, err := os.Create(fpath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Cannot create file %s: %v\n", fpath, err)
			os.Exit(1)
		}
		files[cms] = f
		defer f.Close()
	}

	unknownPath := filepath.Join(*outputDir, "unknown.txt")
	unknownFile, err := os.Create(unknownPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Cannot create file %s: %v\n", unknownPath, err)
		os.Exit(1)
	}
	files["unknown"] = unknownFile
	defer unknownFile.Close()

	// Open input file
	file, err := os.Open(inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot open input file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	fmt.Fprintf(os.Stderr, "[*] Starting CMS detection with %d concurrent threads...\n", concurrency)
	fmt.Fprintf(os.Stderr, "[*] Input: %s\n", inputFile)
	fmt.Fprintf(os.Stderr, "[*] Output: %s\n", *outputDir)
	fmt.Fprintln(os.Stderr, strings.Repeat("=", 60))

	// Worker pool: start exactly `concurrency` goroutines.
	// URLs are fed through the jobs channel one at a time as workers become free,
	// so the total number of goroutines (and their memory) stays constant.
	jobs := make(chan string, concurrency)
	var wg sync.WaitGroup
	var fileMutex sync.Mutex
	var processed int64

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go worker(jobs, files, &fileMutex, &wg, &processed)
	}

	// Feed URLs into the jobs channel; blocks when all workers are busy.
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		url := strings.TrimSpace(scanner.Text())
		if url != "" && !strings.HasPrefix(url, "#") {
			jobs <- url
		}
	}
	close(jobs) // signal workers that no more jobs are coming

	wg.Wait()

	// Print summary
	fmt.Fprintln(os.Stderr, "\n"+strings.Repeat("=", 60))
	fmt.Fprintln(os.Stderr, "✅ Scan complete!")
	fmt.Fprintln(os.Stderr, "")

	// Count results for each CMS
	for cms := range cmsSignatures {
		fpath := filepath.Join(*outputDir, cms+".txt")
		data, _ := os.ReadFile(fpath)
		trimmed := strings.TrimSpace(string(data))
		count := 0
		if trimmed != "" {
			count = len(strings.Split(trimmed, "\n"))
		}
		if count > 0 {
			fmt.Fprintf(os.Stderr, "📊 %s: %d sites\n", strings.ToUpper(cms), count)
		}
	}

	unknownData, _ := os.ReadFile(unknownPath)
	trimmed := strings.TrimSpace(string(unknownData))
	unknownCount := 0
	if trimmed != "" {
		unknownCount = len(strings.Split(trimmed, "\n"))
	}
	if unknownCount > 0 {
		fmt.Fprintf(os.Stderr, "📊 UNKNOWN: %d sites\n", unknownCount)
	}

	fmt.Fprintln(os.Stderr, strings.Repeat("=", 60))
	fmt.Fprintf(os.Stderr, "📁 Results saved to: %s\n", *outputDir)
}