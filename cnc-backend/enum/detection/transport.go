package detection

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type targetKey struct{}

var ErrIncompleteResponse = errors.New("response incomplete or oversized")

func TargetContext(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, targetKey{}, key)
}

type waiter struct {
	req     *http.Request
	ready   chan struct{}
	granted bool
}
type Metrics struct {
	Concurrency, Active int
	Requests, CacheHits uint64
}

// Scheduler maintains one fair request queue per scan; the worker gateway is a
// separate authoritative limit shared with other jobs.
type Scheduler struct {
	Base               http.RoundTripper
	mu                 sync.Mutex
	limit, max, active int
	automatic          bool
	order              []string
	queues             map[string][]*waiter
	hosts              map[string]int
	cooldown           map[string]time.Time
	changed            chan struct{}
	window             time.Time
	samples, bad       int
	requests           atomic.Uint64
	hits               atomic.Uint64
}

func NewScheduler(base http.RoundTripper, max int, automatic bool) *Scheduler {
	if max < 1 || max > 256 {
		max = 256
	}
	limit := max
	if automatic && limit > 64 {
		limit = 64
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &Scheduler{Base: base, limit: limit, max: max, automatic: automatic, queues: map[string][]*waiter{}, hosts: map[string]int{}, cooldown: map[string]time.Time{}, changed: make(chan struct{}), window: time.Now()}
}
func (s *Scheduler) signal() { close(s.changed); s.changed = make(chan struct{}) }
func (s *Scheduler) dispatch() {
	for s.active < s.limit && len(s.order) > 0 {
		granted := false
		for n := len(s.order); n > 0; n-- {
			key := s.order[0]
			s.order = s.order[1:]
			q := s.queues[key]
			w := q[0]
			host := w.req.URL.Scheme + "://" + w.req.URL.Host
			if s.hosts[host] >= 4 || time.Now().Before(s.cooldown[host]) {
				s.order = append(s.order, key)
				continue
			}
			s.queues[key] = q[1:]
			if len(q) > 1 {
				s.order = append(s.order, key)
			} else {
				delete(s.queues, key)
			}
			s.active++
			s.hosts[host]++
			w.granted = true
			close(w.ready)
			granted = true
			break
		}
		if !granted {
			break
		}
	}
}
func (s *Scheduler) acquire(req *http.Request) (func(), error) {
	key, _ := req.Context().Value(targetKey{}).(string)
	if key == "" {
		key = req.URL.Host
	}
	w := &waiter{req: req, ready: make(chan struct{})}
	s.mu.Lock()
	if len(s.queues[key]) == 0 {
		s.order = append(s.order, key)
	}
	s.queues[key] = append(s.queues[key], w)
	s.dispatch()
	s.mu.Unlock()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-w.ready:
			if req.Context().Err() != nil {
				s.release(req)
				return nil, req.Context().Err()
			}
			return func() { s.release(req) }, nil
		case <-req.Context().Done():
			s.mu.Lock()
			if w.granted {
				s.active--
				s.hosts[req.URL.Scheme+"://"+req.URL.Host]--
			} else {
				q := s.queues[key]
				for i, x := range q {
					if x == w {
						s.queues[key] = append(q[:i], q[i+1:]...)
						break
					}
				}
				if len(s.queues[key]) == 0 {
					delete(s.queues, key)
					for i, k := range s.order {
						if k == key {
							s.order = append(s.order[:i], s.order[i+1:]...)
							break
						}
					}
				}
			}
			s.dispatch()
			s.mu.Unlock()
			return nil, req.Context().Err()
		case <-ticker.C:
			s.mu.Lock()
			s.dispatch()
			s.mu.Unlock()
		}
	}
}
func (s *Scheduler) release(req *http.Request) {
	s.mu.Lock()
	s.active--
	host := req.URL.Scheme + "://" + req.URL.Host
	s.hosts[host]--
	if s.hosts[host] == 0 {
		delete(s.hosts, host)
	}
	s.dispatch()
	s.signal()
	s.mu.Unlock()
}
func (s *Scheduler) observe(bad bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples++
	if bad {
		s.bad++
	}
	if s.automatic && time.Since(s.window) >= 5*time.Second {
		if s.samples > 0 && s.bad*5 >= s.samples {
			s.limit /= 2
			if s.limit < 16 {
				s.limit = 16
			}
			if s.limit > s.max {
				s.limit = s.max
			}
		} else if s.samples > 0 && s.bad == 0 {
			s.limit *= 2
			if s.limit > s.max {
				s.limit = s.max
			}
		}
		s.samples = 0
		s.bad = 0
		s.window = time.Now()
	}
}
func (s *Scheduler) Snapshot() Metrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Metrics{s.limit, s.active, s.requests.Load(), s.hits.Load()}
}
func retryDelay(header string) time.Duration {
	if n, e := strconv.Atoi(header); e == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, e := http.ParseTime(header); e == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return time.Second
}
func (s *Scheduler) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 0; attempt < 2; attempt++ {
		release, e := s.acquire(req)
		if e != nil {
			return nil, e
		}
		s.requests.Add(1)
		resp, e := s.Base.RoundTrip(req)
		if e != nil {
			release()
			// DNS failures, refused connections, and timeouts describe this
			// target, not congestion across unrelated origins. Only explicit
			// upstream overload signals reduce global request admission.
			s.observe(false)
			return nil, e
		}
		data, e := io.ReadAll(io.LimitReader(resp.Body, BodyLimit+1))
		closeErr := resp.Body.Close()
		if resp.StatusCode == 429 || resp.StatusCode == 503 {
			delay := retryDelay(resp.Header.Get("Retry-After"))
			host := req.URL.Scheme + "://" + req.URL.Host
			s.mu.Lock()
			until := time.Now().Add(delay)
			if until.After(s.cooldown[host]) {
				s.cooldown[host] = until
			}
			s.mu.Unlock()
		}
		release()
		if e != nil || closeErr != nil || len(data) > BodyLimit {
			s.observe(false)
			return nil, ErrIncompleteResponse
		}
		resp.Body = io.NopCloser(bytes.NewReader(data))
		resp.ContentLength = int64(len(data))
		s.observe(resp.StatusCode == 429 || resp.StatusCode == 503)
		if resp.StatusCode != 429 && resp.StatusCode != 503 {
			return resp, nil
		}
		delay := retryDelay(resp.Header.Get("Retry-After"))
		if attempt == 1 || delay > 30*time.Second {
			return resp, nil
		}
		resp.Body.Close()
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		}
	}
	return nil, fmt.Errorf("retry exhausted")
}

// Session caches only public, completed GET responses, for one target. At most
// eight 512 KiB responses are retained; private requests always bypass the cache.
type Session struct {
	scheduler *Scheduler
	mu        sync.Mutex
	cache     map[string]cached
	order     []string
}
type cached struct {
	response http.Response
	body     []byte
}

func NewSession(base *http.Client, scheduler *Scheduler) *http.Client {
	clone := *base
	clone.Transport = &Session{scheduler: scheduler, cache: map[string]cached{}}
	return &clone
}
func cloneResponse(c cached, req *http.Request) *http.Response {
	r := c.response
	r.Header = c.response.Header.Clone()
	r.Body = io.NopCloser(bytes.NewReader(c.body))
	r.Request = req
	return &r
}
func (s *Session) RoundTrip(req *http.Request) (*http.Response, error) {
	public := req.Method == "GET" && req.Header.Get("Authorization") == "" && req.Header.Get("Cookie") == "" && req.URL.User == nil
	if public {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", "cnc-enum/public-evidence")
	}
	key := req.URL.String()
	if public {
		s.mu.Lock()
		c, ok := s.cache[key]
		s.mu.Unlock()
		if ok {
			s.scheduler.hits.Add(1)
			return cloneResponse(c, req), nil
		}
	}
	resp, e := s.scheduler.RoundTrip(req)
	if e != nil {
		return nil, e
	}
	if !public || resp.StatusCode != 200 || len(resp.Cookies()) > 0 {
		return resp, nil
	}
	data, e := io.ReadAll(resp.Body)
	resp.Body.Close()
	if e != nil {
		return nil, e
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	c := cached{response: *resp, body: data}
	c.response.Header = resp.Header.Clone()
	c.response.Body = nil
	s.mu.Lock()
	if _, ok := s.cache[key]; !ok {
		if len(s.order) == 8 {
			delete(s.cache, s.order[0])
			s.order = s.order[1:]
		}
		s.order = append(s.order, key)
	}
	s.cache[key] = c
	s.mu.Unlock()
	return resp, nil
}
