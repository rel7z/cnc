package scanner

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Status represents the result of probing a single extension on a domain.
type Status int

const (
	StatusFound    Status = iota // 200 + version extracted from XML manifest
	StatusBlocked                // 403 confirmed via fingerprint — extension present, manifest access denied
	StatusNotFound               // 404 or 403 that failed fingerprint check (false positive filtered)
	StatusError                  // network / TLS / other error
)

func (s Status) String() string {
	switch s {
	case StatusFound:
		return "found"
	case StatusBlocked:
		return "blocked (403)"
	case StatusNotFound:
		return "not-found"
	default:
		return "error"
	}
}

// Result holds the outcome of a single (domain, extension) probe.
type Result struct {
	Domain  string
	Key     string // entry.Key()
	Version string // non-empty only when Status == StatusFound
	Status  Status
}

// xmlManifest is a minimal parser for Joomla's extension XML manifests.
type xmlManifest struct {
	Version string `xml:"version"`
}

// newClient builds a shared HTTP client with timeout and capped redirects.
func newClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			return nil
		},
	}
}

// ProbeExtension checks whether the given Joomla extension is installed on the
// domain. On a 403 it runs a secondary fingerprint check against the homepage
// to filter out blanket directory deny rules.
//
// URL patterns probed (primary):
//
//	Component : /administrator/components/com_<slug>/<slug>.xml
//	            (fallback) /components/com_<slug>/<slug>.xml
//	Plugin    : /plugins/<group>/<slug>/<slug>.xml
//	Module    : /modules/mod_<slug>/mod_<slug>.xml
//	Template  : /templates/<slug>/templateDetails.xml
//	Library   : /libraries/<slug>/<slug>.xml
//
// HTTPS is tried first; HTTP is used as fallback on transport failure.
func ProbeExtension(client *http.Client, domain string, entry PluginEntry) Result {
	base := Result{Domain: domain, Key: entry.Key()}

	for _, u := range manifestURLs(domain, entry) {
		r, conclusive := doProbe(client, u, base)
		if !conclusive {
			continue
		}

		// On 403: confirm with homepage fingerprint before counting as a hit.
		if r.Status == StatusBlocked {
			if confirmByFingerprint(client, domain, fingerprintPatterns(entry)) {
				r.Status = StatusBlocked // confirmed — really installed
			} else {
				r.Status = StatusNotFound // blanket 403, not actually installed
			}
		}

		return r
	}

	base.Status = StatusError
	return base
}

// manifestURLs builds the ordered list of primary manifest URLs to probe.
// HTTPS variants always precede HTTP variants.
func manifestURLs(domain string, entry PluginEntry) []string {
	domain = stripScheme(domain)

	var paths []string
	switch entry.Type {
	case ExtComponent:
		paths = []string{
			fmt.Sprintf("/administrator/components/com_%s/%s.xml", entry.Slug, entry.Slug),
			fmt.Sprintf("/components/com_%s/%s.xml", entry.Slug, entry.Slug),
		}
	case ExtPlugin:
		group := entry.Group
		if group == "" {
			group = "system"
		}
		paths = []string{
			fmt.Sprintf("/plugins/%s/%s/%s.xml", group, entry.Slug, entry.Slug),
		}
	case ExtModule:
		paths = []string{
			fmt.Sprintf("/modules/mod_%s/mod_%s.xml", entry.Slug, entry.Slug),
		}
	case ExtTemplate:
		paths = []string{
			fmt.Sprintf("/templates/%s/templateDetails.xml", entry.Slug),
		}
	case ExtLibrary:
		paths = []string{
			fmt.Sprintf("/libraries/%s/%s.xml", entry.Slug, entry.Slug),
		}
	}

	schemes := []string{"https", "http"}
	var urls []string
	for _, scheme := range schemes {
		for _, p := range paths {
			urls = append(urls, scheme+"://"+domain+p)
		}
	}
	return urls
}

// fingerprintPatterns derives extension-specific HTML signatures automatically
// from the slug — no hardcoded per-extension cases needed. Joomla's asset
// naming convention is consistent enough to predict where an extension's public
// files appear in page source.
//
//	Library   → /media/<slug>/
//	Component → /media/com_<slug>/  +  com_<slug>  (option= URL refs)
//	Plugin    → /media/plg_<group>_<slug>/  +  plg_<group>_<slug>
//	Module    → /media/mod_<slug>/  +  mod_<slug>
//	Template  → /templates/<slug>/
func fingerprintPatterns(entry PluginEntry) []string {
	switch entry.Type {
	case ExtLibrary:
		return []string{
			fmt.Sprintf("/media/%s/", entry.Slug),
		}
	case ExtComponent:
		return []string{
			fmt.Sprintf("/media/com_%s/", entry.Slug),
			fmt.Sprintf("com_%s", entry.Slug),
		}
	case ExtPlugin:
		group := entry.Group
		if group == "" {
			group = "system"
		}
		return []string{
			fmt.Sprintf("/media/plg_%s_%s/", group, entry.Slug),
			fmt.Sprintf("plg_%s_%s", group, entry.Slug),
		}
	case ExtModule:
		return []string{
			fmt.Sprintf("/media/mod_%s/", entry.Slug),
			fmt.Sprintf("mod_%s", entry.Slug),
		}
	case ExtTemplate:
		return []string{
			fmt.Sprintf("/templates/%s/", entry.Slug),
		}
	}
	return nil
}

// confirmByFingerprint fetches the site homepage and checks whether any of the
// given patterns appear in the response body.
// An empty patterns slice always returns true (no fingerprint = no filtering).
func confirmByFingerprint(client *http.Client, domain string, patterns []string) bool {
	if len(patterns) == 0 {
		return true // no fingerprint defined — keep the 403 as-is
	}

	domain = stripScheme(domain)

	for _, scheme := range []string{"https", "http"} {
		url := scheme + "://" + domain + "/"
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; joomla-scanner/1.0)")

		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		// 256 KB — covers all <head> asset references
		body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		resp.Body.Close()
		if err != nil {
			continue
		}

		bodyStr := string(body)
		for _, pat := range patterns {
			if strings.Contains(bodyStr, pat) {
				return true
			}
		}

		return false // homepage loaded cleanly, no pattern found
	}

	return false
}

// doProbe performs a single HTTP GET and parses the response.
// Returns (result, true) on a conclusive HTTP response (200/403/404).
// Returns (zero, false) on transport errors so the caller can try the next URL.
func doProbe(client *http.Client, url string, base Result) (Result, bool) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return Result{}, false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; joomla-scanner/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return Result{}, false
	}
	defer resp.Body.Close()

	r := base
	switch resp.StatusCode {
	case http.StatusOK:
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if readErr != nil {
			r.Status = StatusError
			return r, true
		}
		r.Version = parseManifestVersion(body)
		r.Status = StatusFound
		return r, true

	case http.StatusForbidden:
		r.Status = StatusBlocked
		return r, true

	case http.StatusNotFound:
		r.Status = StatusNotFound
		return r, true

	default:
		return Result{}, false
	}
}

// parseManifestVersion extracts <version> from a Joomla XML manifest.
func parseManifestVersion(body []byte) string {
	var m xmlManifest
	if err := xml.Unmarshal(body, &m); err != nil {
		return extractVersionFallback(body)
	}
	return strings.TrimSpace(m.Version)
}

// extractVersionFallback does a naive scan for <version>...</version>.
func extractVersionFallback(body []byte) string {
	s := string(body)
	open := strings.Index(s, "<version>")
	if open == -1 {
		return ""
	}
	open += len("<version>")
	close := strings.Index(s[open:], "</version>")
	if close == -1 {
		return ""
	}
	return strings.TrimSpace(s[open : open+close])
}

// stripScheme removes http:// or https:// prefix and trailing slashes.
func stripScheme(s string) string {
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	return strings.TrimRight(s, "/")
}
