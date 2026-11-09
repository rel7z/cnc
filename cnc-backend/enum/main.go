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
	"cnc-enum/detection"
	"context"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// CMS Detection Engine (inline from cms-scan)
// ─────────────────────────────────────────────────────────────────────────────

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

func detectCMS(body string, headers http.Header, _ *http.Client, _ string) string {
	cms, _ := detection.Analyze([]byte(body), headers)
	return cms
}

// classifyTargets reads the input list and pushes domains to corresponding channels.
func classifyTargets(domains []string, concurrency int, timeout time.Duration, wpChan, joomlaChan, unknownChan chan<- string) {
	jobs := make(chan string, concurrency)
	var wg sync.WaitGroup
	var processed int64

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := newHTTPClient(timeout)
			for domain := range jobs {
				r := detection.Classify(context.Background(), client, domain)
				cms := r.CMS
				switch cms {
				case "wordpress":
					fmt.Fprintf(os.Stdout, "[FILE:wordpress.txt] %s\n", domain)
					wpChan <- domain
				case "joomla":
					fmt.Fprintf(os.Stdout, "[FILE:joomla.txt] %s\n", domain)
					joomlaChan <- domain
				case "unknown":
					unknownChan <- domain
				}

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
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

			// Reject soft 404s or block pages
			bodyStr := string(body)
			if regexp.MustCompile(`(?i)<title>[^<]*(?:404|not found|page doesn't exist|error|forbidden|blocked|security)[^<]*</title>`).MatchString(bodyStr) {
				continue
			}
			if regexp.MustCompile(`(?i)(?:cloudflare|sucuri|wordfence|imperva|incapsula|blocked|forbidden)`).MatchString(bodyStr) && strings.Contains(bodyStr, "<html") {
				continue
			}

			if m := stableTagRe.FindSubmatch(body); len(m) >= 2 {
				version := strings.TrimSpace(string(m[1]))
				// Check if version is somewhat valid: not too long, no HTML, contains at least one digit
				if version != "" && len(version) < 20 && !strings.ContainsAny(version, "<>") && regexp.MustCompile(`\d`).MatchString(version) {
					return version, true
				}
			}
		}
	}
	return "", false
}

func runWPPluginScanner(domainChan <-chan string, plugins []string, concurrency int, timeout time.Duration, out *resultWriter, label string) {
	type job struct{ domain, slug string }
	jobs := make(chan job, concurrency*2)
	var wg sync.WaitGroup
	var found int64
	var totalDomains int64

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := newHTTPClient(timeout)
			for j := range jobs {
				ver, detected := probeWPPlugin(client, j.domain, j.slug)
				if detected {
					line := fmt.Sprintf("[FILE:%s.txt] https://%s/wp-content/plugins/%s  version=%s", j.slug, j.domain, j.slug, ver)
					out.writeLine(line)
					atomic.AddInt64(&found, 1)
				}
			}
		}()
	}

	go func() {
		for d := range domainChan {
			atomic.AddInt64(&totalDomains, 1)
			for _, p := range plugins {
				jobs <- job{d, p}
			}
		}
		close(jobs)
	}()

	wg.Wait()
	fmt.Fprintf(os.Stderr, "[WP] Plugin scan done — %d hits across %d domains\n", found, totalDomains)
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
	base := "https://" + domain
	// Distinguish plugin and library entries from components.
	if strings.HasPrefix(ext, "plugin:") {
		parts := strings.Split(ext, ":")
		if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
			return nil
		}
		return []string{base + "/plugins/" + parts[1] + "/" + parts[2] + "/" + parts[2] + ".xml"}
	}
	if strings.HasPrefix(ext, "lib_") {
		slug := strings.TrimPrefix(ext, "lib_")
		return []string{base + "/libraries/" + slug + "/" + slug + ".xml"}
	}
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
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

			// Quick soft 404 block check (since valid Joomla manifests are pure XML, not HTML)
			if strings.Contains(strings.ToLower(string(body)), "<html") {
				continue
			}

			var manifest xmlManifest
			if xmlErr := xml.Unmarshal(body, &manifest); xmlErr == nil {
				version := strings.TrimSpace(manifest.Version)
				if version != "" && len(version) < 20 && !strings.ContainsAny(version, "<>") && regexp.MustCompile(`\d`).MatchString(version) {
					return version, true
				}
			}
		}
	}
	return "", false
}

func runJoomlaScanner(domainChan <-chan string, extensions []string, concurrency int, timeout time.Duration, out *resultWriter) {
	type job struct{ domain, ext string }
	jobs := make(chan job, concurrency*2)
	var wg sync.WaitGroup
	var found int64
	var totalDomains int64

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := newHTTPClient(timeout)
			for j := range jobs {
				ver, detected := probeJoomlaExt(client, j.domain, j.ext)
				if detected {
					line := fmt.Sprintf("[FILE:%s.txt] https://%s  ext=%s  version=%s", j.ext, j.domain, j.ext, ver)
					out.writeLine(line)
					atomic.AddInt64(&found, 1)
				}
			}
		}()
	}

	go func() {
		for d := range domainChan {
			atomic.AddInt64(&totalDomains, 1)
			for _, e := range extensions {
				jobs <- job{d, e}
			}
		}
		close(jobs)
	}()

	wg.Wait()
	fmt.Fprintf(os.Stderr, "[JOOMLA] Extension scan done — %d hits across %d domains\n", found, totalDomains)
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
	return loginFormKind(body) != ""
}

// Admin enumeration compares candidate pages with a missing-page baseline on
// the same origin. A login form alone does not prove that a path exists.
func probeAdminPath(client *http.Client, origin, path string, baseline adminResponse) (string, bool) {
	// If even a nonexistent path looks like a login, path existence is not
	// established. Reject this catch-all despite dynamic tokens in its body.
	if isThirdPartyAdmin(baseline.body) {
		return "", false
	}
	response, err := fetchAdminResponse(client, origin+path)
	if err != nil || response.status != http.StatusOK || !isThirdPartyAdmin(response.body) {
		return "", false
	}
	if response.finalURL == baseline.finalURL || response.signature == baseline.signature {
		return "", false
	}
	return response.finalURL, true
}

func runAdminEnum(domainChan <-chan string, concurrency int, timeout time.Duration, out *resultWriter) {
	var wg sync.WaitGroup
	var found, totalDomains int64
	var seen sync.Map
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := newHTTPClient(timeout)
			for domain := range domainChan {
				domain = strings.ToLower(strings.TrimRight(strings.TrimSpace(domain), "/"))
				if _, loaded := seen.LoadOrStore(domain, true); loaded {
					continue
				}
				atomic.AddInt64(&totalDomains, 1)
				// Keep one deterministic, validated result per domain. Each origin needs
				// its own baseline; HTTP and HTTPS can serve different applications.
				detected := false
				for _, scheme := range []string{"https", "http"} {
					origin := scheme + "://" + domain
					baseline, err := fetchAdminResponse(client, origin+missingAdminPath())
					if err != nil {
						continue
					}
					for _, path := range hardcodedAdminPaths {
						if url, ok := probeAdminPath(client, origin, path, baseline); ok {
							out.writeLine(fmt.Sprintf("[FILE:enum-admin.txt] %s", url))
							atomic.AddInt64(&found, 1)
							detected = true
							break
						}
					}
					if detected {
						break
					}
				}
			}
		}()
	}
	wg.Wait()
	fmt.Fprintf(os.Stderr, "[ADMIN] Admin enum done — %d hits across %d domains\n", found, totalDomains)
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
		if idx := strings.Index(l, "|"); idx != -1 {
			l = l[:idx]
		}
		if idx := strings.Index(l, ","); idx != -1 {
			l = l[:idx]
		}
		l = strings.TrimSpace(l)
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
	inputFile := flag.String("l", "", "Input file with target domains/URLs (one per line). Required.")
	flag.String("o", "results.txt", "Legacy output name; structured output is job and shard scoped.")
	threads := flag.Int("t", 256, "Maximum concurrent requests (1–256).")
	automatic := flag.Bool("auto-concurrency", true, "Adapt request concurrency automatically")
	wpPlugins := flag.String("wp-plugins", "", "Custom WP plugin wordlist file (default: built-in list).")
	joomlaExts := flag.String("joomla-exts", "", "Custom Joomla extension wordlist file (default: built-in list).")
	timeout := flag.Duration("timeout", 10*time.Second, "HTTP timeout per request (e.g. 10s, 15s).")
	help := flag.Bool("h", false, "Show help.")
	artifactFile := flag.String("version-rules-file", "", "Internal immutable rule artifact")
	artifactDigest := flag.String("version-rules-sha256", "", "Internal artifact SHA256")
	enumJob := flag.String("enum-job-id", "", "Internal job binding")
	enumShard := flag.String("enum-shard-id", "", "Internal shard binding")
	outputDir := flag.String("enum-output-dir", ".", "Internal isolated result directory")
	checkpoint := flag.Bool("enum-checkpoint", false, "Persist progress in the isolated result directory")
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := runVersionPipeline(ctx, pipelineOptions{Input: *inputFile, Artifact: *artifactFile, Digest: *artifactDigest, JobID: *enumJob, ShardID: *enumShard, OutputDir: *outputDir, WPPlugins: *wpPlugins, JoomlaExts: *joomlaExts, Threads: *threads, Timeout: *timeout, Automatic: *automatic, Checkpoint: *checkpoint}, os.Stdout, os.Stderr)
	os.Exit(code)
}
