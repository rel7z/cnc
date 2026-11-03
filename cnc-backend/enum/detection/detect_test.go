package detection

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSignalsAndConflicts(t *testing.T) {
	cases := []struct {
		name, body string
		headers    http.Header
		want       string
	}{
		{"wp generator", "<meta content='WordPress 6.5' name='generator'>", nil, "wordpress"},
		{"joomla structure", "<script src='/media/system/a.js'></script><a href='?option=com_content'>x</a>", nil, "joomla"},
		{"drupal structure", "<script src='/sites/default/a.js'></script><script>drupalSettings={}</script>", nil, "drupal"},
		{"shopify header", "", http.Header{"X-Shopify-Store": []string{"fixture"}}, "shopify"},
		{"second link header", "", http.Header{"Link": []string{"</feed>; rel=alternate", "<https://fixture.test/wp-json/>; rel=\"https://api.w.org/\""}}, "wordpress"},
		{"ghost", "<meta name='generator' content='Ghost 5'>", nil, "ghost"},
		{"magento", "", http.Header{"X-Magento-Tags": []string{"fixture"}}, "magento"},
		{"wix", "<script src='https://static.wixstatic.com/app.js'></script>", nil, "wix"},
		{"squarespace", "<link rel='stylesheet' href='https://static1.squarespace.com/x.css'>", nil, "squarespace"},
		{"prose", "WordPress Joomla Drupal Magento ghost.org wix.com squarespace", nil, "unknown"},
		{"brand link", "<a href='https://wix.com'>I use wix</a>", nil, "unknown"},
		{"one weak", "<script src='/wp-content/a.js'></script>", nil, "unknown"},
		{"cookies", "<script src='/wp-content/a.js'></script>", http.Header{"Set-Cookie": []string{"tracking=x", "wordpress_logged_in_fixture=x"}}, "wordpress"},
		{"conflict", "<meta name='generator' content='Joomla'><meta name='generator' content='WordPress'>", nil, "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := Analyze([]byte(c.body), c.headers)
			if got != c.want {
				t.Fatalf("got %s want %s", got, c.want)
			}
		})
	}
}
func TestBlockedDistinguishesChallengeFromOrdinaryText(t *testing.T) {
	if Blocked(200, []byte(`<html><title>CMS guide</title><p>How to fix access denied errors and verify you are human.</p></html>`)) {
		t.Fatal("ordinary article text classified as a challenge")
	}
	if !Blocked(200, []byte(`<html><title>Access Denied</title></html>`)) ||
		!Blocked(200, []byte(`<html><h1>Verify <span>you</span> are human</h1></html>`)) ||
		!Blocked(403, []byte("")) {
		t.Fatal("challenge response was not blocked")
	}
}
func TestClassifyBudgetAndErrors(t *testing.T) {
	for _, kind := range []string{"clear", "unknown", "rest", "404", "403", "challenge", "oversize", "catchall", "redirect"} {
		t.Run(kind, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				switch kind {
				case "clear":
					fmt.Fprint(w, `<meta name="generator" content="WordPress">`)
				case "unknown":
					fmt.Fprint(w, "plain page")
				case "rest":
					if r.URL.Path == "/wp-json/" {
						fmt.Fprint(w, `{"namespaces":["wp/v2"],"routes":{"/wp/v2/posts":{}}}`)
					} else {
						fmt.Fprint(w, "plain page")
					}
				case "404", "403":
					if kind == "404" {
						w.WriteHeader(404)
					} else {
						w.WriteHeader(403)
					}
					fmt.Fprint(w, `<meta name="generator" content="WordPress">`)
				case "challenge":
					fmt.Fprint(w, `cf-chl-<meta name="generator" content="WordPress">`)
				case "oversize":
					fmt.Fprint(w, strings.Repeat("x", BodyLimit+1))
				case "catchall":
					fmt.Fprint(w, "plain page")
				case "redirect":
					if r.URL.Path != "/site/" {
						http.Redirect(w, r, "/site/", 302)
					} else {
						fmt.Fprint(w, `<meta name="generator" content="Joomla">`)
					}
				}
			}))
			defer server.Close()
			got := Classify(context.Background(), server.Client(), server.URL)
			switch kind {
			case "clear":
				if got.CMS != "wordpress" || requests.Load() != 1 {
					t.Fatal(got, requests.Load())
				}
			case "rest":
				if got.CMS != "wordpress" || requests.Load() != 2 {
					t.Fatal(got, requests.Load())
				}
			case "unknown", "catchall":
				if got.CMS != "unknown" || requests.Load() != 3 {
					t.Fatal(got, requests.Load())
				}
			case "403", "challenge":
				if got.Outcome != "blocked" {
					t.Fatal(got)
				}
			case "404", "oversize":
				if got.Outcome != "error" {
					t.Fatal(got)
				}
			case "redirect":
				if got.CMS != "joomla" || !strings.HasSuffix(got.URL, "/site/") {
					t.Fatal(got)
				}
			}
		})
	}
}
func TestBareHostFallbackAndExplicitScheme(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<meta name="generator" content="Drupal">`)
	}))
	defer server.Close()
	if r := Classify(context.Background(), server.Client(), strings.TrimPrefix(server.URL, "http://")); r.CMS != "drupal" || !strings.HasPrefix(r.URL, "http://") {
		t.Fatal(r)
	}
	if r := Classify(context.Background(), server.Client(), strings.Replace(server.URL, "http://", "https://", 1)); r.Outcome != "error" {
		t.Fatal(r)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestBareHostDoesNotRetryAfterBadHTTPSResponse(t *testing.T) {
	var schemes []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		schemes = append(schemes, request.URL.Scheme)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", BodyLimit+1))),
			Request:    request,
		}, nil
	})}
	client = NewSession(client, NewScheduler(client.Transport, 16, true))
	result := Classify(context.Background(), client, "example.test")
	if result.Outcome != "error" || len(schemes) != 1 || schemes[0] != "https" {
		t.Fatalf("result=%+v schemes=%v", result, schemes)
	}
}
func TestSessionCacheAndPrivateBypass(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, "public") }))
	defer server.Close()
	scheduler := NewScheduler(server.Client().Transport, 16, true)
	client := NewSession(server.Client(), scheduler)
	for i := 0; i < 2; i++ {
		r, e := client.Get(server.URL)
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
	}
	if calls.Load() != 1 || scheduler.Snapshot().CacheHits != 1 {
		t.Fatal(calls.Load())
	}
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest("GET", server.URL, nil)
		req.Header.Set("Authorization", "Bearer fixture")
		r, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
	}
	other := NewSession(server.Client(), scheduler)
	r, _ := other.Get(server.URL)
	r.Body.Close()
	if calls.Load() != 4 {
		t.Fatal(calls.Load())
	}
}
func TestConcurrencyRetryAndCancellation(t *testing.T) {
	var active, peak, calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for n > peak.Load() {
			old := peak.Load()
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		time.Sleep(10 * time.Millisecond)
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()
	scheduler := NewScheduler(server.Client().Transport, 32, true)
	client := *server.Client()
	client.Transport = scheduler
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := client.Get(server.URL)
			if e != nil {
				t.Error(e)
				return
			}
			r.Body.Close()
		}()
	}
	wg.Wait()
	if peak.Load() > 4 || calls.Load() != 13 {
		t.Fatal(peak.Load(), calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	if _, e := client.Do(req); e == nil {
		t.Fatal("accepted cancelled request")
	}
	scheduler.mu.Lock()
	scheduler.window = time.Now().Add(-6 * time.Second)
	scheduler.samples = 10
	scheduler.bad = 4
	scheduler.mu.Unlock()
	scheduler.observe(true)
	if scheduler.Snapshot().Concurrency != 16 {
		t.Fatal(scheduler.Snapshot())
	}
	scheduler.mu.Lock()
	scheduler.window = time.Now().Add(-6 * time.Second)
	scheduler.mu.Unlock()
	scheduler.observe(false)
	if scheduler.Snapshot().Concurrency != 32 {
		t.Fatal(scheduler.Snapshot())
	}
}

func TestFairQueuedTargetsAndRejectedCancellation(t *testing.T) {
	s := NewScheduler(nil, 1, false)
	mk := func(key string, ctx context.Context) *http.Request {
		r, _ := http.NewRequestWithContext(TargetContext(ctx, key), "GET", "http://fixture.test/", nil)
		return r
	}
	release, e := s.acquire(mk("busy", context.Background()))
	if e != nil {
		t.Fatal(e)
	}
	admitted := make(chan string, 3)
	unblock := make(chan struct{})
	enqueue := func(key string) {
		go func() {
			done, e := s.acquire(mk(key, context.Background()))
			if e != nil {
				return
			}
			admitted <- key
			<-unblock
			done()
		}()
	}
	enqueue("big")
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		n := len(s.queues["big"])
		s.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queue missing")
		}
		time.Sleep(time.Millisecond)
	}
	enqueue("big")
	enqueue("small")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := s.acquire(mk("cancelled", ctx)); e == nil {
		t.Fatal("cancelled permit granted")
	}
	time.Sleep(10 * time.Millisecond)
	release()
	if key := <-admitted; key != "big" {
		t.Fatal(key)
	}
	unblock <- struct{}{}
	// The next round visits the newly queued target rather than draining big.
	if key := <-admitted; key != "small" {
		t.Fatal("starved small target:", key)
	}
	unblock <- struct{}{}
	if key := <-admitted; key != "big" {
		t.Fatal(key)
	}
	unblock <- struct{}{}
}

func TestUnreachableOriginDoesNotThrottleOtherOrigins(t *testing.T) {
	scheduler := NewScheduler(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("fixture connection refused")
	}), 64, true)
	for i := 0; i < 3; i++ {
		scheduler.mu.Lock()
		scheduler.window = time.Now().Add(-6 * time.Second)
		scheduler.mu.Unlock()
		req, _ := http.NewRequest("GET", "http://unreachable.invalid/", nil)
		if _, err := scheduler.RoundTrip(req); err == nil {
			t.Fatal("fixture error disappeared")
		}
	}
	if got := scheduler.Snapshot().Concurrency; got != 64 {
		t.Fatalf("unreachable origin reduced unrelated admission to %d", got)
	}
}

func TestAutomaticStartsEnoughDistinctOriginsAndKeepsHostLimit(t *testing.T) {
	scheduler := NewScheduler(nil, 64, true)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var releases []func()
	for i := 0; i < 64; i++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://origin-%d.invalid/", i/4), nil)
		release, err := scheduler.acquire(req)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if got := scheduler.Snapshot().Active; got != 64 {
		t.Fatalf("idle capacity: active=%d", got)
	}
	for _, release := range releases {
		release()
	}
	if scheduler.Snapshot().Active != 0 {
		t.Fatal("permit leak")
	}
}
