package version

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/idna"
)

func canonicalHost(raw string) (string, error) {
	if raw == "" || strings.HasSuffix(raw, ".") || strings.ContainsAny(raw, "*/%\\ \t\n\r") {
		return "", fmt.Errorf("invalid exact host")
	}
	if ip, err := netip.ParseAddr(raw); err == nil {
		return ip.Unmap().String(), nil
	}
	host, err := idna.Lookup.ToASCII(raw)
	if err != nil {
		return "", fmt.Errorf("invalid IDNA host")
	}
	host = strings.ToLower(host)
	if len(host) > 253 {
		return "", fmt.Errorf("host too long")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid DNS label")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", fmt.Errorf("invalid DNS label")
			}
		}
	}
	return host, nil
}

// Unrestricted reports whether no optional target constraints were supplied.
func (s TargetScope) Unrestricted() bool {
	return len(s.Hosts)+len(s.CIDRs)+len(s.Schemes)+len(s.Ports) == 0
}

func NormalizeScope(input TargetScope) (TargetScope, error) {
	out := TargetScope{Hosts: []string{}, CIDRs: []string{}, Schemes: []string{}, Ports: []int{}}
	if input.Unrestricted() {
		return out, nil
	}
	seen := map[string]bool{}
	for _, h := range input.Hosts {
		value, err := canonicalHost(h)
		if err != nil {
			return out, err
		}
		if !seen[value] {
			out.Hosts = append(out.Hosts, value)
			seen[value] = true
		}
	}
	seen = map[string]bool{}
	for _, c := range input.CIDRs {
		p, err := netip.ParsePrefix(c)
		if err != nil || p.Addr().Is4In6() || p.Masked().String() != c {
			return out, fmt.Errorf("CIDR must be canonical with no host bits")
		}
		if !seen[c] {
			out.CIDRs = append(out.CIDRs, c)
			seen[c] = true
		}
	}
	seen = map[string]bool{}
	for _, s := range input.Schemes {
		if s != "http" && s != "https" {
			return out, fmt.Errorf("unsupported scope scheme")
		}
		if !seen[s] {
			out.Schemes = append(out.Schemes, s)
			seen[s] = true
		}
	}
	ports := map[int]bool{}
	for _, p := range input.Ports {
		if p < 1 || p > 65535 {
			return out, fmt.Errorf("invalid scope port")
		}
		if !ports[p] {
			out.Ports = append(out.Ports, p)
			ports[p] = true
		}
	}
	if len(out.Hosts)+len(out.CIDRs) == 0 || len(out.Schemes) == 0 || len(out.Ports) == 0 {
		return out, fmt.Errorf("scope requires hosts or CIDRs, schemes, and ports")
	}
	sort.Strings(out.Hosts)
	sort.Strings(out.CIDRs)
	sort.Strings(out.Schemes)
	sort.Ints(out.Ports)
	return out, nil
}

func CanonicalTarget(raw string) (TargetLocator, error) {
	var target TargetLocator
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return target, fmt.Errorf("invalid HTTP target")
	}
	host, err := canonicalHost(u.Hostname())
	if err != nil {
		return target, err
	}
	port := 443
	if u.Scheme == "http" {
		port = 80
	}
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return target, fmt.Errorf("invalid target port")
		}
	}
	if strings.Contains(u.Path, ";") {
		return target, fmt.Errorf("path parameters are not supported")
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	var normalized strings.Builder
	for i := 0; i < len(path); i++ {
		if path[i] != '%' {
			normalized.WriteByte(path[i])
			continue
		}
		if i+2 >= len(path) {
			return target, fmt.Errorf("invalid path escape")
		}
		n, err := strconv.ParseUint(path[i+1:i+3], 16, 8)
		if err != nil {
			return target, fmt.Errorf("invalid path escape")
		}
		c := byte(n)
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", rune(c)) {
			normalized.WriteByte(c)
		} else {
			fmt.Fprintf(&normalized, "%%%02X", c)
		}
		i += 2
	}
	return TargetLocator{Scheme: u.Scheme, Host: host, Port: port, Path: removeDotSegments(normalized.String())}, nil
}

func removeDotSegments(input string) string {
	output := ""
	for input != "" {
		switch {
		case strings.HasPrefix(input, "../"):
			input = input[3:]
		case strings.HasPrefix(input, "./"):
			input = input[2:]
		case strings.HasPrefix(input, "/./"):
			input = input[2:]
		case input == "/.":
			input = "/"
		case strings.HasPrefix(input, "/../"):
			input = input[3:]
			output = removeLastSegment(output)
		case input == "/..":
			input = "/"
			output = removeLastSegment(output)
		case input == "." || input == "..":
			input = ""
		default:
			start := 0
			if input[0] == '/' {
				start = 1
			}
			end := strings.IndexByte(input[start:], '/')
			if end < 0 {
				output += input
				input = ""
			} else {
				end += start
				output += input[:end]
				input = input[end:]
			}
		}
	}
	if output == "" {
		return "/"
	}
	return output
}
func removeLastSegment(s string) string {
	if index := strings.LastIndexByte(s, '/'); index >= 0 {
		return s[:index]
	}
	return ""
}

func (t TargetLocator) URL() string {
	return t.Scheme + "://" + net.JoinHostPort(t.Host, strconv.Itoa(t.Port)) + t.Path
}

type ResolveFunc func(context.Context, string) ([]net.IP, error)
type DialFunc func(context.Context, string, string) (net.Conn, error)

type ScopeGuard struct {
	Scope   TargetScope
	Resolve ResolveFunc
	Dial    DialFunc
	poolMu  sync.Mutex
	pools   map[string]*http.Transport
}

func NewScopeGuard(scope TargetScope) (*ScopeGuard, error) {
	scope, err := NormalizeScope(scope)
	if err != nil {
		return nil, err
	}
	return &ScopeGuard{Scope: scope, Resolve: func(ctx context.Context, host string) ([]net.IP, error) {
		return net.DefaultResolver.LookupIP(ctx, "ip", host)
	}, Dial: (&net.Dialer{Timeout: 10 * time.Second}).DialContext}, nil
}

// Authorize resolves exactly once per request. With optional constraints enabled,
// every DNS answer must be authorized. An empty scope adds no host, scheme or
// port restrictions; canonical HTTP targets and pinned connections still apply.
func (g *ScopeGuard) Authorize(ctx context.Context, raw string) (TargetLocator, net.IP, error) {
	target, err := CanonicalTarget(raw)
	if err != nil {
		return target, nil, err
	}
	if !g.Scope.Unrestricted() && !contains(g.Scope.Schemes, target.Scheme) {
		return target, nil, fmt.Errorf("scheme outside authorized scope")
	}
	portOK := g.Scope.Unrestricted()
	for _, p := range g.Scope.Ports {
		portOK = portOK || p == target.Port
	}
	if !portOK {
		return target, nil, fmt.Errorf("port outside authorized scope")
	}
	ips := []net.IP{}
	if ip := net.ParseIP(target.Host); ip != nil {
		ips = append(ips, ip)
	} else {
		ips, err = g.Resolve(ctx, target.Host)
		if err != nil {
			return target, nil, fmt.Errorf("target resolution failed: %w", err)
		}
	}
	if len(ips) == 0 {
		return target, nil, fmt.Errorf("target has no addresses")
	}
	hostAllowed := contains(g.Scope.Hosts, target.Host)
	allCIDR, allGlobal := true, true
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok {
			return target, nil, fmt.Errorf("invalid resolved address")
		}
		addr = addr.Unmap()
		cidrAllowed := false
		for _, rawPrefix := range g.Scope.CIDRs {
			prefix, _ := netip.ParsePrefix(rawPrefix)
			cidrAllowed = cidrAllowed || prefix.Contains(addr)
		}
		allCIDR = allCIDR && cidrAllowed
		allGlobal = allGlobal && addr.IsGlobalUnicast()
	}
	if !g.Scope.Unrestricted() && !allCIDR && !(hostAllowed && allGlobal) {
		return target, nil, fmt.Errorf("resolved address outside authorized scope")
	}
	return target, append(net.IP{}, ips[0]...), nil
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// Reauthorize every request and pool only by exact host, scheme, port and
// authorized IP. A changed DNS answer never reuses a connection to the old IP.
func (g *ScopeGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	target, ip, err := g.Authorize(req.Context(), req.URL.String())
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%s|%s|%d|%s", target.Scheme, target.Host, target.Port, ip)
	g.poolMu.Lock()
	if g.pools == nil {
		g.pools = map[string]*http.Transport{}
	}
	transport := g.pools[key]
	if transport == nil {
		if len(g.pools) >= 128 {
			for k, t := range g.pools {
				t.CloseIdleConnections()
				delete(g.pools, k)
				break
			}
		}
		address := net.JoinHostPort(ip.String(), strconv.Itoa(target.Port))
		transport = &http.Transport{Proxy: nil, MaxIdleConns: 4, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 4, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 120 * time.Second, MaxResponseHeaderBytes: 64 * 1024, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: target.Host}}
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) { return g.Dial(ctx, network, address) }
		g.pools[key] = transport
	}
	g.poolMu.Unlock()
	clone := req.Clone(req.Context())
	cleanURL := *clone.URL
	cleanURL.User = nil
	clone.URL = &cleanURL
	clone.Header.Del("Authorization")
	clone.Header.Del("Cookie")
	clone.Host = req.URL.Host
	return transport.RoundTrip(clone)
}
func (g *ScopeGuard) CloseIdleConnections() {
	g.poolMu.Lock()
	defer g.poolMu.Unlock()
	for _, t := range g.pools {
		t.CloseIdleConnections()
	}
}

func (g *ScopeGuard) Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: g, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}}
}
