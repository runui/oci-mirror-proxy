// Package route connects the frontend to upstream pools. A Route serves one
// registry alias, and middleware wraps it to add cross-cutting behaviour.
package route

import (
	"context"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/runui/oci-mirror-proxy/internal/scheduler"
)

// Dispatcher serves requests for one registry alias.
type Dispatcher interface {
	Dispatch(ctx context.Context, req scheduler.Request) (*scheduler.Result, error)
}

// DispatcherFunc adapts a plain function to the Dispatcher interface.
type DispatcherFunc func(ctx context.Context, req scheduler.Request) (*scheduler.Result, error)

// Dispatch calls f.
func (f DispatcherFunc) Dispatch(ctx context.Context, req scheduler.Request) (*scheduler.Result, error) {
	return f(ctx, req)
}

// Middleware wraps a Dispatcher to add behaviour such as logging or resumption.
type Middleware func(next Dispatcher) Dispatcher

// Chain wraps d with mws. The first middleware is the outermost layer.
func Chain(d Dispatcher, mws ...Middleware) Dispatcher {
	for i := len(mws) - 1; i >= 0; i-- {
		d = mws[i](d)
	}
	return d
}

// CandidateSource supplies the enabled upstreams in priority order.
type CandidateSource interface {
	Candidates() []scheduler.Candidate
}

// Route is the base Dispatcher: one strategy over one candidate source.
type Route struct {
	Source   CandidateSource
	Strategy scheduler.Strategy
}

// Dispatch takes a fresh candidate snapshot and runs the strategy over it.
func (r Route) Dispatch(ctx context.Context, req scheduler.Request) (*scheduler.Result, error) {
	return r.Strategy.Execute(ctx, req, r.Source.Candidates())
}

// Table publishes whole immutable generations. Acquire briefly locks to pin a
// generation safely against retirement; streaming and retries never hold it.
type Table struct {
	mu      sync.RWMutex // held only to acquire/publish a generation, never while serving bytes
	current atomic.Pointer[generation]
}

type generation struct {
	routes  map[string]Dispatcher
	refs    atomic.Int64
	cleanup func()
}

func (g *generation) release() {
	if g.refs.Add(-1) == 0 && g.cleanup != nil {
		g.cleanup()
	}
}

// NewTable returns a table with no routes.
func NewTable() *Table {
	t := &Table{}
	g := &generation{routes: map[string]Dispatcher{}}
	g.refs.Store(1)
	t.current.Store(g)
	return t
}

// Swap atomically replaces every route. The map is copied, so the caller may reuse it.
func (t *Table) Swap(routes map[string]Dispatcher) {
	t.SwapWithCleanup(routes, nil)
}

// SwapWithCleanup publishes all registries together. The previous generation
// is reclaimed only after every acquired HTTP request has closed its body.
func (t *Table) SwapWithCleanup(routes map[string]Dispatcher, cleanup func()) {
	copied := maps.Clone(routes)
	if copied == nil {
		copied = map[string]Dispatcher{}
	}
	g := &generation{routes: copied, cleanup: cleanup}
	g.refs.Store(1)
	t.mu.Lock()
	old := t.current.Swap(g)
	t.mu.Unlock()
	old.release()
}

// Acquire pins the entire generation, not just its first upstream attempt.
// Resume and Adaptive must keep using the same candidate set after reload.
func (t *Table) Acquire(alias string) (Dispatcher, func(), bool) {
	t.mu.RLock()
	g := t.current.Load()
	d, ok := g.routes[alias]
	if ok {
		g.refs.Add(1)
	}
	t.mu.RUnlock()
	if !ok {
		return nil, func() {}, false
	}
	var once sync.Once
	return d, func() { once.Do(g.release) }, true
}

// Lookup is a non-owning inspection. Request handlers must use Acquire so the
// dispatcher's transports cannot be retired while its body/retries are live.
func (t *Table) Lookup(alias string) (Dispatcher, bool) {
	d, ok := t.current.Load().routes[alias]
	return d, ok
}

// Aliases returns the configured aliases in sorted order.
func (t *Table) Aliases() []string {
	return slices.Sorted(maps.Keys(t.current.Load().routes))
}
