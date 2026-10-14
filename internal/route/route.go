// Package route connects the frontend to upstream pools. A Route serves one
// registry alias, and middleware wraps it to add cross-cutting behaviour.
package route

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"sync/atomic"

	"github.com/runui/oci-mirror-proxy/internal/scheduler"
)

// Dispatcher serves requests for one registry alias.
type Dispatcher interface {
	Dispatch(ctx context.Context, req scheduler.Request) (*http.Response, error)
}

// DispatcherFunc adapts a plain function to the Dispatcher interface.
type DispatcherFunc func(ctx context.Context, req scheduler.Request) (*http.Response, error)

// Dispatch calls f.
func (f DispatcherFunc) Dispatch(ctx context.Context, req scheduler.Request) (*http.Response, error) {
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
func (r Route) Dispatch(ctx context.Context, req scheduler.Request) (*http.Response, error) {
	return r.Strategy.Execute(ctx, req, r.Source.Candidates())
}

// Table maps registry aliases to dispatchers. Lookups are lock-free, and a whole
// new table is published with Swap. Requests in flight keep the table they started with.
type Table struct {
	current atomic.Pointer[map[string]Dispatcher]
}

// NewTable returns a table with no routes.
func NewTable() *Table {
	t := &Table{}
	empty := map[string]Dispatcher{}
	t.current.Store(&empty)
	return t
}

// Swap atomically replaces every route. The map is copied, so the caller may reuse it.
func (t *Table) Swap(routes map[string]Dispatcher) {
	copied := maps.Clone(routes)
	if copied == nil {
		copied = map[string]Dispatcher{}
	}
	t.current.Store(&copied)
}

// Lookup returns the dispatcher for alias.
func (t *Table) Lookup(alias string) (Dispatcher, bool) {
	d, ok := (*t.current.Load())[alias]
	return d, ok
}

// Aliases returns the configured aliases in sorted order.
func (t *Table) Aliases() []string {
	return slices.Sorted(maps.Keys(*t.current.Load()))
}
