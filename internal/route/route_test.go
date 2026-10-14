package route

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/scheduler"
)

type fixedSource []scheduler.Candidate

func (f fixedSource) Candidates() []scheduler.Candidate { return f }

type idCandidate string

func (c idCandidate) ID() string { return string(c) }

func (c idCandidate) Attempt(context.Context, scheduler.Request) (*http.Response, error) {
	return nil, scheduler.ErrSkipped
}

func TestChainOrderIsOutermostFirst(t *testing.T) {
	var order []string
	layer := func(name string) Middleware {
		return func(next Dispatcher) Dispatcher {
			return DispatcherFunc(func(ctx context.Context, req scheduler.Request) (*http.Response, error) {
				order = append(order, name)
				return next.Dispatch(ctx, req)
			})
		}
	}
	base := DispatcherFunc(func(context.Context, scheduler.Request) (*http.Response, error) {
		order = append(order, "base")
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, nil
	})
	if _, err := Chain(base, layer("outer"), layer("inner")).Dispatch(t.Context(), scheduler.Request{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"outer", "inner", "base"}; !slices.Equal(order, want) {
		t.Fatalf("execution order = %v, want %v", order, want)
	}
}

func TestRouteUsesCurrentCandidates(t *testing.T) {
	var seen []string
	strategy := strategyFunc(func(_ context.Context, _ scheduler.Request, cands []scheduler.Candidate) (*http.Response, error) {
		for _, c := range cands {
			seen = append(seen, c.ID())
		}
		return nil, &scheduler.Exhausted{}
	})
	src := fixedSource{idCandidate("a"), idCandidate("b")}
	_, _ = Route{Source: src, Strategy: strategy}.Dispatch(t.Context(), scheduler.Request{})
	if !slices.Equal(seen, []string{"a", "b"}) {
		t.Fatalf("strategy saw %v", seen)
	}
}

type strategyFunc func(context.Context, scheduler.Request, []scheduler.Candidate) (*http.Response, error)

func (f strategyFunc) Execute(ctx context.Context, req scheduler.Request, c []scheduler.Candidate) (*http.Response, error) {
	return f(ctx, req, c)
}

func TestTableSwapIsAtomicAndCopied(t *testing.T) {
	table := NewTable()
	if _, ok := table.Lookup("docker.io"); ok {
		t.Fatal("new table must be empty")
	}

	routes := map[string]Dispatcher{"docker.io": DispatcherFunc(nil)}
	table.Swap(routes)
	routes["ghcr.io"] = DispatcherFunc(nil) // mutating the caller's map must not affect the table

	if _, ok := table.Lookup("docker.io"); !ok {
		t.Fatal("swapped route not found")
	}
	if _, ok := table.Lookup("ghcr.io"); ok {
		t.Fatal("table shares storage with the caller's map")
	}
	if got := table.Aliases(); !slices.Equal(got, []string{"docker.io"}) {
		t.Fatalf("aliases = %v", got)
	}
}
