package scanner

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Status represents the result of probing a single plugin on a domain.
type Status int

const (
	StatusFound    Status = iota // 200 + version extracted
	StatusBlocked                // 403 — plugin likely installed but file access blocked
	StatusNotFound               // 404 — plugin not present
	StatusError                  // network / TLS / other error
)

func (s Status) String() string {
	switch s {
	case StatusFound:
		return "found"
	case StatusBlocked:
		return "blocked"
	case StatusNotFound:
		return "not-found"
	default:
		return "error"
	}
}

// Result holds the outcome of a single (domain, plugin) probe.
type Result struct {
	Domain  string
	Slug    string
	Version string // non-empty only when Status == StatusFound
	Status  Status
}

var stableTagRe = regexp.MustCompile(`(?im)^stable tag:\s*(.+?)\s*$`)

// newClient builds an HTTP client with the given timeout and a capped redirect chain.
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

// ProbePlugin checks whether a given plugin slug is installed on a domain.
// It tries HTTPS first; on TLS failure it retries with HTTP.
func ProbePlugin(client *http.Client, domain, slug string) Result {
	result := Result{Domain: domain, Slug: slug}

	// Try HTTPS first, then HTTP as fallback
	schemes := []string{"https", "http"}
	for _, scheme := range schemes {
		url := fmt.Sprintf("%s://%s/wp-content/plugins/%s/readme.txt", scheme, domain, slug)
		r, err := doProbe(client, url, slug, &result)
		if err != nil {
			// TLS or connection error → try next scheme
			continue
		}
		result = r
		return result
	}

	// Both schemes failed
	result.Status = StatusError
	return result
}

// doProbe performs the actual HTTP request and parses the response.
// Returns (result, nil) on a conclusive HTTP response, or (zero, err) on transport error.
func doProbe(client *http.Client, url, slug string, base *Result) (Result, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; wp-scanner/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	result := *base

	switch resp.StatusCode {
	case http.StatusOK:
		// Read up to 32 KB — readme.txt is always tiny
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		if readErr != nil {
			result.Status = StatusError
			return result, nil
		}
		version := extractStableTag(body)
		result.Version = version
		result.Status = StatusFound

	case http.StatusForbidden:
		result.Status = StatusBlocked

	case http.StatusNotFound:
		result.Status = StatusNotFound

	default:
		// Treat unexpected codes (301 loops, 5xx, etc.) as not-found to avoid noise
		result.Status = StatusNotFound
	}

	return result, nil
}

// extractStableTag parses the "Stable tag: x.y.z" line from a readme.txt body.
func extractStableTag(body []byte) string {
	matches := stableTagRe.FindSubmatch(body)
	if len(matches) >= 2 {
		v := strings.TrimSpace(string(matches[1]))
		// Ignore "trunk" — it means no pinned version
		if strings.EqualFold(v, "trunk") {
			return "trunk"
		}
		return v
	}
	return ""
}
