package version

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// Gateway owns the single request budget used by every scanner child of one
// CNC worker. Each unguessable registration is bound to a validated artifact
// and the task cancellation context, not to a caller-supplied job or scope.
type Gateway struct {
	Budget        *RequestBudget
	URL           string
	mu            sync.Mutex
	registrations map[string]gatewayRegistration
	server        *http.Server
}
type gatewayRegistration struct {
	ctx   context.Context
	jobID string
	guard *ScopeGuard
}
type gatewayRequest struct {
	URL string `json:"url"`
}

func StartGateway(limit int) (*Gateway, error) {
	budget, err := NewRequestBudget(limit)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	g := &Gateway{Budget: budget, URL: "http://" + listener.Addr().String() + "/fetch", registrations: map[string]gatewayRegistration{}}
	g.server = &http.Server{Handler: http.HandlerFunc(g.serve), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 135 * time.Second, IdleTimeout: 30 * time.Second}
	go g.server.Serve(listener)
	return g, nil
}
func (g *Gateway) Close() error { return g.server.Close() }
func (g *Gateway) Register(ctx context.Context, artifact ArtifactV1) (string, func(), error) {
	if err := ValidateArtifact(&artifact); err != nil {
		return "", nil, err
	}
	guard, err := NewScopeGuard(artifact.TargetScope)
	if err != nil {
		return "", nil, err
	}
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", nil, err
	}
	token := hex.EncodeToString(data)
	g.mu.Lock()
	g.registrations[token] = gatewayRegistration{ctx: ctx, jobID: artifact.JobID, guard: guard}
	g.mu.Unlock()
	remove := func() { g.mu.Lock(); delete(g.registrations, token); g.mu.Unlock(); guard.CloseIdleConnections() }
	return token, remove, nil
}
func (g *Gateway) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Enum-Error", "invalid gateway request")
	if r.Method != http.MethodPost || r.URL.Path != "/fetch" {
		http.Error(w, "invalid evidence request", http.StatusBadRequest)
		return
	}
	g.mu.Lock()
	registration, ok := g.registrations[r.Header.Get("X-Enum-Token")]
	g.mu.Unlock()
	if !ok {
		http.Error(w, "invalid evidence registration", http.StatusUnauthorized)
		return
	}
	if registration.ctx.Err() != nil {
		http.Error(w, "task is canceled", http.StatusGone)
		return
	}
	var request gatewayRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil {
		http.Error(w, "invalid evidence request", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 130*time.Second)
	defer cancel()
	stop := context.AfterFunc(registration.ctx, cancel)
	defer stop()
	upstream, err := http.NewRequestWithContext(ctx, http.MethodGet, request.URL, nil)
	if err != nil {
		http.Error(w, "invalid evidence locator", http.StatusBadRequest)
		return
	}
	// No cookies, authorization, request bodies, or forwarded credentials.
	upstream.URL.User = nil
	upstream.Header.Set("User-Agent", "cnc-enum/public-evidence")
	transport := &BudgetTransport{Budget: g.Budget, JobID: registration.jobID, Base: registration.guard}
	response, err := transport.RoundTrip(upstream)
	if err != nil {
		w.Header().Set("X-Enum-Error", "request rejected")
		http.Error(w, "evidence request rejected", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, EvidenceBodyLimit+1))
	if readErr != nil || len(data) > EvidenceBodyLimit {
		w.Header().Set("X-Enum-Error", "invalid response")
		http.Error(w, "evidence response incomplete or oversized", http.StatusBadGateway)
		return
	}
	w.Header().Del("X-Enum-Error")
	// Forward only public detection signals, never hop-by-hop or credentials.
	for _, key := range []string{"Content-Type", "Link", "X-Powered-By", "X-Generator", "X-Drupal-Cache", "X-Drupal-Dynamic-Cache", "X-Shopify-Store", "X-Shopify-Stage", "X-Magento-Tags", "X-Magento-Cache-Control", "X-Wix-Request-Id", "X-Squarespace-Request-Id", "Set-Cookie", "Retry-After"} {
		for _, value := range response.Header.Values(key) {
			w.Header().Add(key, value)
		}
	}
	if location := response.Header.Get("Location"); location != "" {
		w.Header().Set("Location", location)
	}
	w.WriteHeader(response.StatusCode)
	w.Write(data)
}

// GatewayTransport makes no outbound target connection in the scanner child;
// the worker gateway enforces scope, DNS pinning, and its shared fair budget.
type GatewayTransport struct {
	URL, Token string
	Client     *http.Client
}

// Share the loopback connection pool across requests. Creating a transport per
// request leaves one idle socket behind for every probe until it times out.
var gatewayHTTPClient = &http.Client{
	Transport:     &http.Transport{Proxy: nil, MaxIdleConns: 16, MaxIdleConnsPerHost: 8, IdleConnTimeout: 10 * time.Second},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (t *GatewayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return nil, fmt.Errorf("evidence only supports GET")
	}
	if _, err := CanonicalTarget(req.URL.String()); err != nil {
		return nil, err
	}
	data, _ := json.Marshal(gatewayRequest{URL: req.URL.String()})
	request, err := http.NewRequestWithContext(req.Context(), http.MethodPost, t.URL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Enum-Token", t.Token)
	request.Header.Set("Content-Type", "application/json")
	client := t.Client
	if client == nil {
		client = gatewayHTTPClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.Header.Get("X-Enum-Error") != "" {
		response.Body.Close()
		return nil, fmt.Errorf("worker rejected evidence request")
	}
	response.Request = req
	return response, nil
}
