package upstream

import (
	"context"
	"fmt"
	"sync"

	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/scheduler"
)

// Status is the operator-facing view of one upstream.
type Status struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Disabled    bool   `json:"disabled,omitzero"`
	Breaker     string `json:"breaker"`
	Requests    int64  `json:"requests"`
	Failures    int64  `json:"failures"`
	LastLatency string `json:"last_latency,omitempty"`
}

// Pool owns the upstreams of one registry. Readers take an immutable snapshot of
// the endpoint slice, so they never wait for configuration changes. Writers
// are serialized by writeMu and publish a new slice in one step.
type Pool struct {
	env Env

	writeMu sync.Mutex

	mu        sync.RWMutex
	breaker   config.Breaker
	endpoints []*Endpoint
}

// NewPool creates an empty pool.
func NewPool(env Env) *Pool {
	return &Pool{env: env}
}

// Reconcile is the mutable-pool compatibility API. The service uses Fork so old
// HTTP requests and their later resumes never observe a changed candidate set.
func (p *Pool) Reconcile(ctx context.Context, reg config.Registry) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	next, err := p.Fork(ctx, reg)
	if err != nil {
		return err
	}
	p.mu.Lock()
	old := p.endpoints
	p.endpoints = next.endpoints
	p.breaker = reg.Breaker
	p.mu.Unlock()
	for _, e := range old {
		e.Release()
	}
	return nil
}

// Fork prepares an immutable generation without changing the published pool.
// Even unchanged endpoints get an ownership reference in the new generation;
// retirement of the old generation must not close a reused connection pool.
func (p *Pool) Fork(ctx context.Context, reg config.Registry) (*Pool, error) {
	next := NewPool(p.env)
	p.mu.RLock()
	current := map[string]*Endpoint{}
	for _, e := range p.endpoints {
		// Pin the snapshot before dropping the lock: the compatibility API
		// may concurrently remove an endpoint while this fork is preparing.
		e.Retain()
		current[e.URL()] = e
	}
	reuse := p.breaker == reg.Breaker
	p.mu.RUnlock()
	defer func() {
		for _, e := range current {
			e.Release()
		}
	}()
	seen := map[string]bool{}
	for _, u := range reg.Upstreams {
		if seen[u.URL] {
			next.Release()
			return nil, fmt.Errorf("duplicate upstream URL %q", u.URL)
		}
		seen[u.URL] = true
		e := current[u.URL]
		if e == nil || !reuse || e.Spec() != u {
			var err error
			e, err = NewEndpoint(ctx, u, reg.Breaker, p.env)
			if err != nil {
				next.Release()
				return nil, err
			}
		}
		e.Retain()
		next.endpoints = append(next.endpoints, e)
	}
	next.breaker = reg.Breaker
	return next, nil
}

// Release is called once when the owning generation has drained.
func (p *Pool) Release() {
	for _, e := range p.endpoints {
		e.Release()
	}
}

// Candidates returns the enabled endpoints in priority order. The result is a
// fresh slice, so callers may hold it while the pool is reconciled.
func (p *Pool) Candidates() []scheduler.Candidate {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]scheduler.Candidate, 0, len(p.endpoints))
	for _, e := range p.endpoints {
		if !e.Spec().Disabled {
			out = append(out, e)
		}
	}
	return out
}

// Status returns the status of every upstream, enabled or not, in priority order.
func (p *Pool) Status() []Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Status, 0, len(p.endpoints))
	for _, e := range p.endpoints {
		out = append(out, e.Status())
	}
	return out
}
