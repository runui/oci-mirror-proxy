package route

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/scheduler"
)

type fixedSource []scheduler.Candidate

func (f fixedSource) Candidates() []scheduler.Candidate { return f }

type idCandidate string

func (c idCandidate) ID() string { return string(c) }

func (c idCandidate) Attempt(context.Context, scheduler.Request) (*scheduler.Result, error) {
	return nil, scheduler.ErrSkipped
}

func TestGenerationCleanupWaitsForLeaseAndRunsOnce(t *testing.T) {
	table := NewTable()
	var cleaned atomic.Int32
	table.SwapWithCleanup(map[string]Dispatcher{"docker.io": DispatcherFunc(nil)}, func() { cleaned.Add(1) })
	_, release, ok := table.Acquire("docker.io")
	if !ok {
		t.Fatal("missing route")
	}
	table.Swap(nil)
	if cleaned.Load() != 0 {
		t.Fatal("retired a live generation")
	}
	release()
	release()
	if cleaned.Load() != 1 {
		t.Fatal("cleanup did not run exactly once")
	}
}

func TestConcurrentGenerationAcquireAndPublish(t *testing.T) {
	table := NewTable()
	var cleaned atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_, release, _ := table.Acquire("docker.io")
				release()
			}
		}()
	}
	for i := 0; i < 100; i++ {
		table.SwapWithCleanup(map[string]Dispatcher{"docker.io": DispatcherFunc(nil)}, func() { cleaned.Add(1) })
	}
	wg.Wait()
	table.Swap(nil)
	if cleaned.Load() != 100 {
		t.Fatalf("leaked generations: %d", cleaned.Load())
	}
}

func TestChainOrderIsOutermostFirst(t *testing.T) {
	var order []string
	layer := func(name string) Middleware {
		return func(next Dispatcher) Dispatcher {
			return DispatcherFunc(func(ctx context.Context, req scheduler.Request) (*scheduler.Result, error) {
				order = append(order, name)
				return next.Dispatch(ctx, req)
			})
		}
	}
	base := DispatcherFunc(func(context.Context, scheduler.Request) (*scheduler.Result, error) {
		order = append(order, "base")
		return scheduler.Stream(&http.Response{StatusCode: http.StatusOK, Header: http.Header{}}), nil
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
	strategy := strategyFunc(func(_ context.Context, _ scheduler.Request, cands []scheduler.Candidate) (*scheduler.Result, error) {
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

type strategyFunc func(context.Context, scheduler.Request, []scheduler.Candidate) (*scheduler.Result, error)

func (f strategyFunc) Execute(ctx context.Context, req scheduler.Request, c []scheduler.Candidate) (*scheduler.Result, error) {
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
