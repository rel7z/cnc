package version

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
)

type BudgetMetrics struct {
	Active    int    `json:"active"`
	Queued    int    `json:"queued"`
	Cancelled uint64 `json:"cancelled"`
}
type budgetWaiter struct {
	ready   chan struct{}
	granted bool
}

// RequestBudget is shared by a worker process, never by an individual scanner
// subprocess. Waiting jobs are visited in round-robin order, one permit each.
type RequestBudget struct {
	mu            sync.Mutex
	limit, active int
	order         []string
	queues        map[string][]*budgetWaiter
	metrics       map[string]BudgetMetrics
}

func BudgetLimit(raw string) (int, error) {
	if raw == "" {
		return 64, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 256 {
		return 0, fmt.Errorf("ENUM_MAX_INFLIGHT_REQUESTS must be an integer from 1 to 256")
	}
	return n, nil
}
func NewRequestBudget(limit int) (*RequestBudget, error) {
	if limit < 1 || limit > 256 {
		return nil, fmt.Errorf("invalid request budget")
	}
	return &RequestBudget{limit: limit, queues: map[string][]*budgetWaiter{}, metrics: map[string]BudgetMetrics{}}, nil
}

func (b *RequestBudget) Acquire(ctx context.Context, job string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w := &budgetWaiter{ready: make(chan struct{})}
	b.mu.Lock()
	if len(b.queues[job]) == 0 {
		b.order = append(b.order, job)
	}
	b.queues[job] = append(b.queues[job], w)
	m := b.metrics[job]
	m.Queued++
	b.metrics[job] = m
	b.dispatch()
	b.mu.Unlock()
	select {
	case <-ctx.Done():
		b.mu.Lock()
		m := b.metrics[job]
		m.Cancelled++
		if w.granted {
			b.active--
			m.Active--
		} else {
			q := b.queues[job]
			for i, item := range q {
				if item == w {
					b.queues[job] = append(q[:i], q[i+1:]...)
					break
				}
			}
			m.Queued--
			b.removeEmpty(job)
		}
		b.metrics[job] = m
		b.dispatch()
		b.mu.Unlock()
		return nil, ctx.Err()
	case <-w.ready:
		var once sync.Once
		release := func() {
			once.Do(func() {
				b.mu.Lock()
				b.active--
				m := b.metrics[job]
				m.Active--
				if ctx.Err() != nil {
					m.Cancelled++
				}
				b.metrics[job] = m
				b.dispatch()
				b.mu.Unlock()
			})
		}
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		return release, nil
	}
}

func (b *RequestBudget) removeEmpty(job string) {
	if len(b.queues[job]) != 0 {
		return
	}
	delete(b.queues, job)
	for i, name := range b.order {
		if name == job {
			b.order = append(b.order[:i], b.order[i+1:]...)
			return
		}
	}
}
func (b *RequestBudget) dispatch() {
	for b.active < b.limit && len(b.order) > 0 {
		job := b.order[0]
		b.order = b.order[1:]
		q := b.queues[job]
		w := q[0]
		b.queues[job] = q[1:]
		if len(q) > 1 {
			b.order = append(b.order, job)
		} else {
			delete(b.queues, job)
		}
		w.granted = true
		b.active++
		m := b.metrics[job]
		m.Queued--
		m.Active++
		b.metrics[job] = m
		close(w.ready)
	}
}
func (b *RequestBudget) Snapshot() map[string]BudgetMetrics {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]BudgetMetrics{}
	for k, v := range b.metrics {
		out[k] = v
	}
	return out
}

type BudgetTransport struct {
	Budget *RequestBudget
	JobID  string
	Base   http.RoundTripper
}

func (t *BudgetTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	release, err := t.Budget.Acquire(req.Context(), t.JobID)
	if err != nil {
		return nil, err
	}
	response, err := t.Base.RoundTrip(req)
	if err != nil {
		release()
		return nil, err
	}
	body := &permitBody{ReadCloser: response.Body, release: release}
	body.stopMu.Lock()
	body.stop = context.AfterFunc(req.Context(), func() { body.Close() })
	body.stopMu.Unlock()
	response.Body = body
	return response, nil
}

type permitBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
	stop    func() bool
	stopMu  sync.Mutex
	err     error
}

func (b *permitBody) Close() error {
	b.once.Do(func() {
		b.stopMu.Lock()
		if b.stop != nil {
			b.stop()
		}
		b.stopMu.Unlock()
		b.err = b.ReadCloser.Close()
		b.release()
	})
	return b.err
}
