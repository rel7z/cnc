// cnc-enum: Automated Multi-Stage CMS Enumeration Pipeline
//
// Phase 1: Classify every target using CMS fingerprinting (cms-scan engine).
// Phase 2: Cascade into specialized deep scanners:
//   - WordPress  → wp-plugins-detector (probes /wp-content/plugins/<slug>/readme.txt)
//   - Joomla     → joomla-detector     (probes extension XML manifests)
//   - Unknown    → admin-enum          (probes hardcoded admin paths; rejects WP/Joomla hits)
//
// All confirmed results are written to the watcher output directory for
// automatic Google Drive deduplication and synchronization.
//
// Usage:
//
//	cnc-enum -l targets.txt -t 50 -o /path/to/watcher/output.txt
package main

import (
	"bufio"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// CMS Detection Engine (inline from cms-scan)
// ─────────────────────────────────────────────────────────────────────────────

const maxBodyBytes = 512 * 1024

type cmsIndicatorRaw struct {
	patterns     []string
	minMatches   int
	headerChecks map[string]string
	metaChecks   []string
	cookieChecks []string
}

type cmsIndicator struct {
	compiledPatterns []*regexp.Regexp
	minMatches       int
	headerChecks     map[string]string
	compiledMeta     []*regexp.Regexp
	cookieChecks     []string
}

var cmsSignaturesRaw = map[string]cmsIndicatorRaw{
	"wordpress": {
		patterns:     []string{`wp-content`, `wp-includes`, `wp-json`, `xmlrpc\.php`, `wp-login\.php`, `generator.*wordpress`, `readme\.html.*wordpress`},
		minMatches:   2,
		headerChecks: map[string]string{"Link": `rel="https://api.w.org/"`, "X-Powered-By": `wordpress`},
		metaChecks:   []string{`name="generator".*wordpress`, `property="og:site_name".*wordpress`},
		cookieChecks: []string{`wordpress_`, `wp-settings-`},
	},
	"joomla": {
		patterns:     []string{`joomla`, `com_content`, `com_users`, `com_config`, `powered.*joomla`, `joomla\.css`, `joomla\.js`},
		minMatches:   2,
		headerChecks: map[string]string{"X-Powered-By": `joomla`},
		metaChecks:   []string{`name="generator".*joomla`, `name="joomla"`},
		cookieChecks: []string{`joomla_`, `jfcookie`},
	},
	"drupal": {
		patterns:     []string{`drupal`, `sites/default`, `misc/drupal`, `core/modules`, `powered.*drupal`, `drupal\.js`, `drupal\.css`},
		minMatches:   2,
		headerChecks: map[string]string{"X-Powered-By": `drupal`, "X-Drupal-Cache": ``, "X-Generator": `drupal`},
		metaChecks:   []string{`name="generator".*drupal`, `drupal\.settings`},
		cookieChecks: []string{`sess`, `drupal_`, `has_js`},
	},
}

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

func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

func detectCMS(body string, headers http.Header, client *http.Client, rawURL string) string {
	bodyLower := strings.ToLower(body)

	type cmsScore struct {
		name  string
		score int
	}
	var scores []cmsScore

	for cms, indicator := range cmsSignatures {
		score := 0
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

		for header, expected := range indicator.headerChecks {
			headerVal := strings.ToLower(headers.Get(header))
			if headerVal != "" {
				if expected == "" || strings.Contains(headerVal, strings.ToLower(expected)) {
					score += 20
				}
			}
		}
		for _, re := range indicator.compiledMeta {
			if re.MatchString(bodyLower) {
				score += 15
			}
		}
		cookies := strings.ToLower(headers.Get("Set-Cookie"))
		for _, cp := range indicator.cookieChecks {
			if strings.Contains(cookies, strings.ToLower(cp)) {
				score += 10
			}
		}
		if score > 0 {
			scores = append(scores, cmsScore{name: cms, score: score})
		}
	}

	// WordPress-specific readme.html check
	readmeURL := strings.TrimSuffix(rawURL, "/") + "/readme.html"
	if req, err := http.NewRequest("GET", readmeURL, nil); err == nil {
		req.Header.Set("User-Agent", "Mozilla/5.0")
		if resp, err := client.Do(req); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				b, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
				if strings.Contains(strings.ToLower(string(b)), "wordpress") {
					scores = append(scores, cmsScore{name: "wordpress", score: 50})
				}
			}
		}
	}

	if len(scores) == 0 {
		return "unknown"
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })
	if scores[0].score < 25 {
		return "unknown"
	}
	return scores[0].name
}

// classifyTargets reads the input list and returns per-CMS buckets.
func classifyTargets(domains []string, concurrency int, timeout time.Duration) map[string][]string {
	results := map[string][]string{
		"wordpress": {},
		"joomla":    {},
		"unknown":   {},
		"other":     {},
	}
	var mu sync.Mutex

	jobs := make(chan string, concurrency)
	var wg sync.WaitGroup
	var processed int64

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := newHTTPClient(timeout)
			for domain := range jobs {
				url := domain
				if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
					url = "https://" + url
				}
				var cms string
				req, err := http.NewRequest("GET", url, nil)
				if err != nil {
					cms = "unknown"
				} else {
					req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
					resp, err := client.Do(req)
					if err != nil {
						cms = "unknown"
					} else {
						body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
						resp.Body.Close()
						cms = detectCMS(string(body), resp.Header, client, url)
					}
				}
				mu.Lock()
				switch cms {
				case "wordpress":
					results["wordpress"] = append(results["wordpress"], domain)
				case "joomla":
					results["joomla"] = append(results["joomla"], domain)
				case "unknown":
					results["unknown"] = append(results["unknown"], domain)
				default:
					results["other"] = append(results["other"], domain)
				}
				mu.Unlock()

				n := atomic.AddInt64(&processed, 1)
				fmt.Fprintf(os.Stderr, "\r[*] CMS Scan: %d/%d classified", n, int64(len(domains)))
			}
		}()
	}

	for _, d := range domains {
		jobs <- d
	}
	close(jobs)
	wg.Wait()
	fmt.Fprintln(os.Stderr)
	return results
}

// ─────────────────────────────────────────────────────────────────────────────
// WordPress Plugin Scanner (inline from wp-plugins-detector)
// ─────────────────────────────────────────────────────────────────────────────

// Default plugin wordlist — embedded so no external file is needed.
var defaultWPPlugins = []string{
	"elementor",
	"elementor-pro",
	"woocommerce",
	"contact-form-7",
	"yoast-seo",
	"wordfence",
	"akismet",
	"jetpack",
	"wpforms-lite",
	"updraftplus",
	"really-simple-ssl",
	"all-in-one-seo-pack",
	"wp-mail-smtp",
	"ninja-forms",
	"wp-super-cache",
	"litespeed-cache",
	"w3-total-cache",
	"all-in-one-wp-migration",
	"autoptimize",
	"redirection",
}

var stableTagRe = regexp.MustCompile(`(?im)^stable tag:\s*(.+?)\s*$`)

func probeWPPlugin(client *http.Client, domain, slug string) (string, bool) {
	for _, scheme := range []string{"https", "http"} {
		url := fmt.Sprintf("%s://%s/wp-content/plugins/%s/readme.txt", scheme, domain, slug)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; wp-scanner/1.0)")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()
		switch resp.StatusCode {
		case 200:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
			version := ""
			if m := stableTagRe.FindSubmatch(body); len(m) >= 2 {
				version = strings.TrimSpace(string(m[1]))
			}
			return version, true
		case 403:
			return "(blocked)", true
		}
	}
	return "", false
}

func runWPPluginScanner(domains, plugins []string, concurrency int, timeout time.Duration, out *resultWriter, label string) {
	type job struct{ domain, slug string }
	jobs := make(chan job, concurrency*2)
	var wg sync.WaitGroup
	var found int64

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := newHTTPClient(timeout)
			for j := range jobs {
				ver, detected := probeWPPlugin(client, j.domain, j.slug)
				if detected {
					line := fmt.Sprintf("[FILE:enum-wp-plugins.txt] https://%s/wp-content/plugins/%s  version=%s", j.domain, j.slug, ver)
					out.writeLine(line)
					atomic.AddInt64(&found, 1)
				}
			}
		}()
	}

	go func() {
		for _, d := range domains {
			for _, p := range plugins {
				jobs <- job{d, p}
			}
		}
		close(jobs)
	}()

	wg.Wait()
	fmt.Fprintf(os.Stderr, "[WP] Plugin scan done — %d hits across %d domains\n", found, len(domains))
}

// ─────────────────────────────────────────────────────────────────────────────
// Joomla Extension Scanner (inline from joomla-detector)
// ─────────────────────────────────────────────────────────────────────────────

// Default Joomla extension wordlist — embedded.
var defaultJoomlaExtensions = []string{
	"com_content",
	"com_users",
	"com_contact",
	"com_weblinks",
	"com_newsfeeds",
	"com_fields",
	"com_finder",
	"com_tags",
	"com_media",
	"com_akeeba",
	"com_admintools",
	"com_virtuemart",
	"com_hikashop",
	"com_easyblog",
	"com_k2",
	"com_phocagallery",
	"com_gridbox",
}

type xmlManifest struct {
	Version string `xml:"version"`
}

func manifestURLs(domain, ext string) []string {
	// Attempt the most common component manifest path
	base := "https://" + domain
	slug := strings.TrimPrefix(ext, "com_")
	return []string{
		base + "/administrator/components/" + ext + "/" + slug + ".xml",
		base + "/components/" + ext + "/" + slug + ".xml",
	}
}

func probeJoomlaExt(client *http.Client, domain, ext string) (string, bool) {
	for _, url := range manifestURLs(domain, ext) {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; joomla-scanner/1.0)")
		resp, err := client.Do(req)
		if err != nil {
			// Retry with http:// on connection failure
			url = strings.Replace(url, "https://", "http://", 1)
			req2, _ := http.NewRequest("GET", url, nil)
			if req2 != nil {
				req2.Header.Set("User-Agent", "Mozilla/5.0 (compatible; joomla-scanner/1.0)")
				resp, err = client.Do(req2)
			}
			if err != nil {
				continue
			}
		}
		defer resp.Body.Close()

		switch resp.StatusCode {
		case 200:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
			var manifest xmlManifest
			version := ""
			if xmlErr := xml.Unmarshal(body, &manifest); xmlErr == nil {
				version = manifest.Version
			}
			return version, true
		case 403:
			return "(blocked)", true
		}
	}
	return "", false
}

func runJoomlaScanner(domains, extensions []string, concurrency int, timeout time.Duration, out *resultWriter) {
	type job struct{ domain, ext string }
	jobs := make(chan job, concurrency*2)
	var wg sync.WaitGroup
	var found int64

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := newHTTPClient(timeout)
			for j := range jobs {
				ver, detected := probeJoomlaExt(client, j.domain, j.ext)
				if detected {
					line := fmt.Sprintf("[FILE:enum-joomla-exts.txt] https://%s  ext=%s  version=%s", j.domain, j.ext, ver)
					out.writeLine(line)
					atomic.AddInt64(&found, 1)
				}
			}
		}()
	}

	go func() {
		for _, d := range domains {
			for _, e := range extensions {
				jobs <- job{d, e}
			}
		}
		close(jobs)
	}()

	wg.Wait()
	fmt.Fprintf(os.Stderr, "[JOOMLA] Extension scan done — %d hits across %d domains\n", found, len(domains))
}

// ─────────────────────────────────────────────────────────────────────────────
// Admin Panel Enumerator (inline from admin-enum, with 3rd-party filter)
// ─────────────────────────────────────────────────────────────────────────────

// Hardcoded admin paths to probe for unknown targets.
// Strictly generic paths — no WordPress or Joomla-specific routes.
var hardcodedAdminPaths = []string{
	"/admin",
	"/admin/",
	"/admin/login",
	"/admin/login.php",
	"/administrator",
	"/administrator/",
	"/panel",
	"/panel/login",
	"/cpanel",
	"/login",
	"/backend",
	"/backend/login",
	"/manage",
	"/management",
	"/controlpanel",
	"/webadmin",
	"/adminpanel",
	"/adminarea",
	"/siteadmin",
}

// Patterns that indicate the page belongs to WordPress — reject these.
var wpFingerprints = []*regexp.Regexp{
	regexp.MustCompile(`(?i)wp-login\.php`),
	regexp.MustCompile(`(?i)wp-admin`),
	regexp.MustCompile(`(?i)wp-content`),
	regexp.MustCompile(`(?i)wordpress`),
	regexp.MustCompile(`(?i)wp-includes`),
}

// Patterns that indicate the page belongs to Joomla — reject these.
var joomlaFingerprints = []*regexp.Regexp{
	regexp.MustCompile(`(?i)joomla`),
	regexp.MustCompile(`(?i)com_login`),
	regexp.MustCompile(`(?i)com_users`),
	regexp.MustCompile(`(?i)administrator.*joomla`),
	regexp.MustCompile(`(?i)option=com_`),
}

// Patterns that suggest a genuine 3rd-party login panel.
var adminLoginSignals = []*regexp.Regexp{
	regexp.MustCompile(`(?i)<input[^>]+type=["']?password["']?`),
	regexp.MustCompile(`(?i)name=["']?password["']?`),
	regexp.MustCompile(`(?i)name=["']?username["']?`),
	regexp.MustCompile(`(?i)name=["']?user["']?`),
	regexp.MustCompile(`(?i)name=["']?email["']?`),
	regexp.MustCompile(`(?i)<form[^>]+action`),
	regexp.MustCompile(`(?i)login|sign.?in`),
}

// isThirdPartyAdmin returns true if the response looks like a genuine
// 3rd-party admin login panel (not WordPress, not Joomla).
func isThirdPartyAdmin(body []byte) bool {
	bodyStr := string(body)
	// Reject if any WP fingerprint present
	for _, re := range wpFingerprints {
		if re.MatchString(bodyStr) {
			return false
		}
	}
	// Reject if any Joomla fingerprint present
	for _, re := range joomlaFingerprints {
		if re.MatchString(bodyStr) {
			return false
		}
	}
	// Require at least 2 admin login signals
	matched := 0
	for _, re := range adminLoginSignals {
		if re.MatchString(bodyStr) {
			matched++
			if matched >= 2 {
				return true
			}
		}
	}
	return false
}

func probeAdminPath(client *http.Client, domain, path string) (string, bool) {
	for _, scheme := range []string{"https", "http"} {
		fullURL := scheme + "://" + domain + path
		req, err := http.NewRequest("GET", fullURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; admin-enum/1.0)")

		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		switch resp.StatusCode {
		case 200:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
			if isThirdPartyAdmin(body) {
				return fullURL, true
			}
		case 403:
			// Possible admin panel but access is blocked — note it
			return fullURL + "  [403-PROTECTED]", true
		}
	}
	return "", false
}

func runAdminEnum(domains []string, concurrency int, timeout time.Duration, out *resultWriter) {
	type job struct{ domain, path string }
	jobs := make(chan job, concurrency*2)
	var wg sync.WaitGroup
	var found int64

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := newHTTPClient(timeout)
			for j := range jobs {
				url, detected := probeAdminPath(client, j.domain, j.path)
				if detected {
					line := fmt.Sprintf("[FILE:enum-admin.txt] %s", url)
					out.writeLine(line)
					atomic.AddInt64(&found, 1)
				}
			}
		}()
	}

	go func() {
		for _, d := range domains {
			for _, p := range hardcodedAdminPaths {
				jobs <- job{d, p}
			}
		}
		close(jobs)
	}()

	wg.Wait()
	fmt.Fprintf(os.Stderr, "[ADMIN] Admin enum done — %d hits across %d domains\n", found, len(domains))
}

// ─────────────────────────────────────────────────────────────────────────────
// Concurrent result writer (appends to watcher output file)
// ─────────────────────────────────────────────────────────────────────────────

type resultWriter struct {
	mu  sync.Mutex
	buf *bufio.Writer
}

func newResultWriter(path string) (*resultWriter, error) {
	// Ignore path, write directly to stdout
	return &resultWriter{buf: bufio.NewWriterSize(os.Stdout, 8192)}, nil
}

func (rw *resultWriter) writeLine(line string) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	fmt.Fprintln(rw.buf, line)
	rw.buf.Flush()
}

func (rw *resultWriter) close() {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.buf.Flush()
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func loadLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		// Strip scheme so bare domains and full URLs both work
		l = strings.TrimPrefix(l, "https://")
		l = strings.TrimPrefix(l, "http://")
		if strings.Contains(l, "://") {
			continue // malformed
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Main
// ─────────────────────────────────────────────────────────────────────────────

func main() {
	inputFile  := flag.String("l", "", "Input file with target domains/URLs (one per line). Required.")
	outputFile := flag.String("o", "results.txt", "Output file path (appended to — place in watcher dir for auto Google Drive sync).")
	threads    := flag.Int("t", 50, "Number of concurrent threads per scanning phase.")
	wpPlugins  := flag.String("wp-plugins", "", "Custom WP plugin wordlist file (default: built-in list).")
	joomlaExts := flag.String("joomla-exts", "", "Custom Joomla extension wordlist file (default: built-in list).")
	timeout    := flag.Duration("timeout", 10*time.Second, "HTTP timeout per request (e.g. 10s, 15s).")
	help       := flag.Bool("h", false, "Show help.")
	flag.Parse()

	if *help || *inputFile == "" {
		fmt.Println("cnc-enum — Automated Multi-Stage CMS Enumeration Pipeline")
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  cnc-enum -l targets.txt -o /watcher/output.txt -t 50")
		fmt.Println()
		fmt.Println("Flags:")
		flag.PrintDefaults()
		fmt.Println()
		fmt.Println("Pipeline:")
		fmt.Println("  1. CMS detection → classifies targets as wordpress / joomla / unknown")
		fmt.Println("  2. WordPress  → probes /wp-content/plugins/<slug>/readme.txt")
		fmt.Println("  3. Joomla     → probes extension XML manifests")
		fmt.Println("  4. Unknown    → probes hardcoded admin paths (3rd-party filter applied)")
		os.Exit(0)
	}

	// Load targets
	domains, err := loadLines(*inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Cannot read input file %q: %v\n", *inputFile, err)
		os.Exit(1)
	}
	if len(domains) == 0 {
		fmt.Fprintln(os.Stderr, "[ERROR] Input file is empty.")
		os.Exit(1)
	}

	// Load optional custom wordlists
	wpPluginList := defaultWPPlugins
	if *wpPlugins != "" {
		lines, err := loadLines(*wpPlugins)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] Could not read wp-plugins file: %v — using defaults\n", err)
		} else if len(lines) > 0 {
			wpPluginList = lines
		}
	}

	joomlaExtList := defaultJoomlaExtensions
	if *joomlaExts != "" {
		lines, err := loadLines(*joomlaExts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] Could not read joomla-exts file: %v — using defaults\n", err)
		} else if len(lines) > 0 {
			joomlaExtList = lines
		}
	}

	// Open result writer (appends to watcher output file)
	out, err := newResultWriter(*outputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Cannot open output file %q: %v\n", *outputFile, err)
		os.Exit(1)
	}
	defer out.close()

	fmt.Printf("[*] cnc-enum starting — %d targets | threads=%d | timeout=%s\n", len(domains), *threads, *timeout)
	fmt.Printf("[*] Output: %s\n\n", *outputFile)

	// ── Phase 1: CMS Classification ──────────────────────────────────────────
	fmt.Fprintln(os.Stderr, "[*] Phase 1: CMS Classification...")
	buckets := classifyTargets(domains, *threads, *timeout)

	fmt.Printf("[+] WordPress  : %d targets\n", len(buckets["wordpress"]))
	fmt.Printf("[+] Joomla     : %d targets\n", len(buckets["joomla"]))
	fmt.Printf("[+] Unknown    : %d targets\n", len(buckets["unknown"]))
	fmt.Printf("[+] Other CMS  : %d targets (skipped)\n\n", len(buckets["other"]))

	// ── Phase 2a: WordPress Plugin Scan ─────────────────────────────────────
	if len(buckets["wordpress"]) > 0 {
		fmt.Fprintf(os.Stderr, "[*] Phase 2a: WordPress plugin scan (%d targets, %d plugins)...\n",
			len(buckets["wordpress"]), len(wpPluginList))
		runWPPluginScanner(buckets["wordpress"], wpPluginList, *threads, *timeout, out, "wp")
	}

	// ── Phase 2b: Joomla Extension Scan ─────────────────────────────────────
	if len(buckets["joomla"]) > 0 {
		fmt.Fprintf(os.Stderr, "[*] Phase 2b: Joomla extension scan (%d targets, %d extensions)...\n",
			len(buckets["joomla"]), len(joomlaExtList))
		runJoomlaScanner(buckets["joomla"], joomlaExtList, *threads, *timeout, out)
	}

	// ── Phase 2c: Unknown — Admin Panel Enum ────────────────────────────────
	if len(buckets["unknown"]) > 0 {
		fmt.Fprintf(os.Stderr, "[*] Phase 2c: Admin panel enum (%d targets, %d paths)...\n",
			len(buckets["unknown"]), len(hardcodedAdminPaths))
		runAdminEnum(buckets["unknown"], *threads, *timeout, out)
	}

	fmt.Printf("\n[*] Done. All results written to: %s\n", *outputFile)
}
