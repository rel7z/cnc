package version

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const WordPressHomepageLimit = 64 << 10

const WordPressFreshJobPrefix = "enum_fresh_"

func WordPressOutputName(jobID string) string {
	if strings.HasPrefix(jobID, WordPressFreshJobPrefix) {
		return "wordpress-fresh-" + jobID + ".txt"
	}
	return "wordpress-" + jobID + ".txt"
}

func WordPressHomepage(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 2048 {
		return "", ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || u.Hostname() == "" {
		return "", ""
	}
	if (u.Scheme != "https" && u.Scheme != "http") ||
		(u.Port() != "" && !(u.Scheme == "https" && u.Port() == "443") && !(u.Scheme == "http" && u.Port() == "80")) {
		return "", ""
	}
	target, err := CanonicalTarget(raw)
	if err != nil || net.ParseIP(target.Host) != nil || !strings.Contains(target.Host, ".") {
		return "", ""
	}
	return target.Scheme + "://" + target.Host + "/", strings.TrimPrefix(target.Host, "www.")
}

var nonPublicHomepageNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/3"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}
var publicHomepageIPv6 = netip.MustParsePrefix("2000::/3")

func PublicHomepageIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || (addr.Is6() && !publicHomepageIPv6.Contains(addr)) {
		return false
	}
	for _, prefix := range nonPublicHomepageNetworks {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func NewPublicHomepageGuard() (*ScopeGuard, error) {
	guard, err := NewScopeGuard(TargetScope{})
	if err != nil {
		return nil, err
	}
	resolve := guard.Resolve
	guard.Resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		ips, err := resolve(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !PublicHomepageIP(ip) {
				return nil, errors.New("site resolves outside public address space")
			}
		}
		return ips, nil
	}
	return guard, nil
}

func NewWordPressHomepageClient() (*http.Client, func(), error) {
	guard, err := NewPublicHomepageGuard()
	if err != nil {
		return nil, nil, err
	}
	client := guard.Client(4 * time.Second)
	client.CheckRedirect = WordPressHomepageRedirect
	return client, guard.CloseIdleConnections, nil
}

func WordPressHomepageRedirect(req *http.Request, via []*http.Request) error {
	if len(via) != 1 {
		return http.ErrUseLastResponse
	}
	initial := via[0].URL
	path := strings.ToLower(req.URL.Path)
	if req.URL.User != nil || req.URL.Scheme != initial.Scheme || !strings.EqualFold(req.URL.Host, initial.Host) ||
		strings.HasPrefix(path, "/wp-admin") || strings.HasPrefix(path, "/wp-login") || path == "/xmlrpc.php" {
		return http.ErrUseLastResponse
	}
	return nil
}

const (
	WordPressVerified = "verified"
	WordPressNoMarker = "no_marker"
)

type wordpressHomepageProbe struct {
	url, outcome string
	tryAlias     bool
}

func VerifyWordPressHomepage(ctx context.Context, client *http.Client, raw string) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	home, host := WordPressHomepage(raw)
	if home == "" {
		return "", "invalid"
	}
	u, _ := url.Parse(home)
	result := probeWordPressHomepage(ctx, client, home)
	if result.tryAlias && ctx.Err() == nil {
		alternative := host
		if u.Host == host {
			alternative = "www." + host
		}
		retry := probeWordPressHomepage(ctx, client, u.Scheme+"://"+alternative+"/")
		// Preserve the original diagnosis if the alternate host also fails
		// to resolve or connect. A real HTTP response takes precedence.
		if retry.outcome != "dns" && retry.outcome != "transport" && retry.outcome != "timeout" {
			return retry.url, retry.outcome
		}
	}
	return result.url, result.outcome
}

func homepageTransportOutcome(err error) string {
	var failure *net.DNSError
	if errors.As(err, &failure) && !failure.Timeout() {
		return "dns"
	}
	var timed net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timed) && timed.Timeout()) {
		return "timeout"
	}
	return "transport"
}

func probeWordPressHomepage(ctx context.Context, client *http.Client, raw string) wordpressHomepageProbe {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return wordpressHomepageProbe{outcome: "invalid"}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CNC WordPress Finder)")
	req.Header.Set("Accept", "text/html, application/xhtml+xml")
	res, err := client.Do(req)
	if err != nil {
		outcome := homepageTransportOutcome(err)
		return wordpressHomepageProbe{outcome: outcome, tryAlias: outcome != "timeout"}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return wordpressHomepageProbe{
			outcome: "http_" + strconv.Itoa(res.StatusCode),
		}
	}
	verified, err := wordpressHomepageEvidence(res)
	if err != nil {
		return wordpressHomepageProbe{outcome: homepageTransportOutcome(err)}
	}
	if !verified {
		return wordpressHomepageProbe{outcome: WordPressNoMarker}
	}
	home, _ := WordPressHomepage(res.Request.URL.String())
	return wordpressHomepageProbe{url: home, outcome: WordPressVerified}
}

var wordpressHomepageMarkers = [][]byte{
	[]byte("/wp-content/themes/"),
	[]byte("/wp-content/plugins/"),
	[]byte("/wp-includes/"),
	[]byte(`content="wordpress `),
}

func wordpressHomepageEvidence(res *http.Response) (bool, error) {
	contentType := strings.ToLower(res.Header.Get("Content-Type"))
	if !strings.HasPrefix(contentType, "text/html") && !strings.HasPrefix(contentType, "application/xhtml+xml") {
		return false, nil
	}
	if strings.Contains(strings.ToLower(res.Header.Get("Link")), "rel=\"https://api.w.org/\"") {
		return true, nil
	}
	// Keep enough overlap to recognize a marker split across consecutive reads.
	const overlap = 32
	var chunk [8192 + overlap]byte
	reader := io.LimitReader(res.Body, WordPressHomepageLimit)
	previous := 0
	for {
		n, err := reader.Read(chunk[previous:])
		if n > 0 {
			window := chunk[:previous+n]
			for i := previous; i < len(window); i++ {
				if window[i] >= 'A' && window[i] <= 'Z' {
					window[i] += 'a' - 'A'
				}
			}
			for _, marker := range wordpressHomepageMarkers {
				if bytes.Contains(window, marker) {
					return true, nil
				}
			}
			previous = min(len(window), overlap)
			copy(chunk[:previous], window[len(window)-previous:])
		}
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
}
