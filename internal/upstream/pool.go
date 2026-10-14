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

// Reconcile makes the pool match reg. Upstreams whose definition is unchanged keep
// their runtime state, such as breaker position and counters. Changed or new
// upstreams are rebuilt. When the breaker settings change, every endpoint is
// rebuilt, because existing breakers carry the old thresholds. Any build error
// leaves the pool exactly as it was.
func (p *Pool) Reconcile(ctx context.Context, reg config.Registry) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	p.mu.RLock()
	current := make(map[string]*Endpoint, len(p.endpoints))
	for _, e := range p.endpoints {
		current[e.ID()] = e
	}
	rebuildAll := p.breaker != reg.Breaker
	p.mu.RUnlock()

	next := make([]*Endpoint, 0, len(reg.Upstreams))
	seen := make(map[string]bool, len(reg.Upstreams))
	for _, u := range reg.Upstreams {
		if seen[u.ID] {
			return fmt.Errorf("duplicate upstream id %q", u.ID)
		}
		seen[u.ID] = true

		if old, ok := current[u.ID]; ok && !rebuildAll && old.Spec() == u {
			next = append(next, old)
			continue
		}
		e, err := NewEndpoint(ctx, u, reg.Breaker, p.env)
		if err != nil {
			return fmt.Errorf("upstream %q: %w", u.ID, err)
		}
		next = append(next, e)
	}

	p.mu.Lock()
	dropped := droppedEndpoints(p.endpoints, next)
	p.endpoints = next
	p.breaker = reg.Breaker
	p.mu.Unlock()

	for _, e := range dropped {
		e.Close()
	}
	return nil
}

// droppedEndpoints returns the endpoints of old that do not appear in next.
// An endpoint is identified by pointer, so reused endpoints are never reported.
func droppedEndpoints(old, next []*Endpoint) []*Endpoint {
	keep := make(map[*Endpoint]bool, len(next))
	for _, e := range next {
		keep[e] = true
	}
	var dropped []*Endpoint
	for _, e := range old {
		if !keep[e] {
			dropped = append(dropped, e)
		}
	}
	return dropped
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
