package scheduler

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// lifetimeCandidate models generation ownership independently of an endpoint's
// response-body reference. It lets us hold a cancelled attempt past handler exit.
type lifetimeCandidate struct {
	id        string
	refs      atomic.Int32
	started   chan struct{}
	proceed   chan struct{}
	released  chan struct{}
	underflow atomic.Bool
}

func newLifetimeCandidate(id string) *lifetimeCandidate {
	c := &lifetimeCandidate{id: id, started: make(chan struct{}), proceed: make(chan struct{}), released: make(chan struct{})}
	c.refs.Store(1) // The simulated parent request's generation.
	return c
}

func (c *lifetimeCandidate) ID() string { return c.id }
func (c *lifetimeCandidate) Retain()    { c.refs.Add(1) }
func (c *lifetimeCandidate) Release() {
	switch n := c.refs.Add(-1); {
	case n == 0:
		close(c.released)
	case n < 0:
		c.underflow.Store(true)
	}
}

func (c *lifetimeCandidate) Attempt(ctx context.Context, _ Request) (*Result, error) {
	close(c.started)
	<-c.proceed
	return nil, ctx.Err()
}

func awaitLifetime(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("background attempt did not finish")
	}
}

func TestHedgedAttemptPinsCandidatePastHandlerCancellation(t *testing.T) {
	c := newLifetimeCandidate("delayed")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := (Hedged{MaxParallel: 1}).Execute(ctx, Request{}, []Candidate{c})
		done <- err
	}()
	awaitLifetime(t, c.started)
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler waited for cancelled loser")
	}
	c.Release() // The handler and its retired generation can now finish.
	if c.refs.Load() != 1 {
		t.Fatalf("background attempt lost its owner: %d", c.refs.Load())
	}
	close(c.proceed)
	awaitLifetime(t, c.released)
	if c.underflow.Load() {
		t.Fatal("candidate released more than once")
	}
}

func TestAdaptiveProbePinsCandidatePastBodyClose(t *testing.T) {
	c := newLifetimeCandidate("alternate")
	ctx, cancel := context.WithCancel(t.Context())
	b := &adaptiveBody{
		ctx: ctx, cancel: cancel, body: io.NopCloser(strings.NewReader("old")),
		candidates: []Candidate{c}, tried: map[string]bool{}, total: 100,
		policy: Adaptive{ProbeTimeout: time.Second},
	}
	b.startProbe(1)
	awaitLifetime(t, c.started)
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	c.Release()
	if c.refs.Load() != 1 {
		t.Fatalf("background probe lost its owner: %d", c.refs.Load())
	}
	close(c.proceed)
	awaitLifetime(t, c.released)
	if c.underflow.Load() {
		t.Fatal("candidate released more than once")
	}
}
