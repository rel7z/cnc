package scanner

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// BaselineInfo holds the result of probing a random junk path on a domain.
// It is used to detect catch-all routes and to pre-seed the body-size
// deduplication map so pages that look identical to the baseline are dropped.
type BaselineInfo struct {
	// BodySize is the byte length of the baseline response body (0 on error).
	BodySize int

	// Matched is true when the baseline response itself satisfies the
	// keyword/title match criteria — i.e. the domain is a catch-all that
	// would confirm every path. All jobs for that domain should be skipped.
	Matched bool

	// Unavailable is true when the baseline HTTP request failed entirely
	// (connection refused, timeout, etc.). Filtering is skipped for this
	// domain so we don't suppress legitimate results.
	Unavailable bool
}

// randomPath returns a URL path made from cryptographically random bytes,
// e.g. "/a3f9c2b1d4e8f701". This is very unlikely to exist on any real server.
func randomPath() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback — still highly unlikely to be a real path.
		return "/kiro-baseline-check-xxxxxxxx"
	}
	return "/" + hex.EncodeToString(b)
}

// ProbeBaseline sends one GET request to a random junk path on domain and
// returns a BaselineInfo describing the response. It reuses the same
// ProbeURL / doProbe machinery so the result is directly comparable.
func ProbeBaseline(client *http.Client, domain string, keywords []string, titles []string, allTrue bool) BaselineInfo {
	domain = strings.TrimRight(domain, "/")
	if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
		domain = "https://" + domain
	}

	junkURL := fmt.Sprintf("%s%s", domain, randomPath())

	// We do a single probe — no HTTPS→HTTP fallback needed here; if HTTPS
	// fails we treat the domain as unavailable for baseline purposes rather
	// than risking a false "catch-all detected" via a protocol error.
	base := Result{FullURL: junkURL}
	if u, err := parseURLParts(junkURL); err == nil {
		base.Domain = u.host
		base.Path = u.path
	} else {
		base.Domain = domain
		base.Path = "/"
	}

	r, err := doProbe(client, junkURL, base, keywords, titles, allTrue)
	if err != nil {
		return BaselineInfo{Unavailable: true}
	}

	// A network-level error stored in the result (Status == StatusError)
	// also means we couldn't get a useful baseline.
	if r.Status == StatusError {
		return BaselineInfo{Unavailable: true}
	}

	return BaselineInfo{
		BodySize:    r.BodySize,
		Matched:     r.Status == StatusConfirmed,
		Unavailable: false,
	}
}

// baselineClient returns a dedicated HTTP client for baseline probes.
// We use a tighter timeout so a slow catch-all doesn't stall the whole scan.
func baselineClient(timeout time.Duration) *http.Client {
	t := timeout
	if t > 5*time.Second {
		t = 5 * time.Second
	}
	return newClient(t)
}
