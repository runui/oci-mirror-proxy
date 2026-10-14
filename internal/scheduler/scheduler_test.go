package scheduler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// trackingBody records whether it was closed, so tests can verify that
// discarded responses are released.
type trackingBody struct {
	io.Reader
	closed atomic.Bool
}

func (b *trackingBody) Close() error {
	b.closed.Store(true)
	return nil
}

func newResponse(code int, body string) (*http.Response, *trackingBody) {
	b := &trackingBody{Reader: strings.NewReader(body)}
	return &http.Response{StatusCode: code, Body: b}, b
}

type fakeCandidate struct {
	id string
	fn func(ctx context.Context) (*http.Response, error)
}

func (f fakeCandidate) ID() string { return f.id }

func (f fakeCandidate) Attempt(ctx context.Context, _ Request) (*http.Response, error) {
	return f.fn(ctx)
}

// answer returns a candidate that responds with code immediately.
func answer(id string, code int) fakeCandidate {
	return fakeCandidate{id: id, fn: func(context.Context) (*http.Response, error) {
		resp, _ := newResponse(code, "body-"+id)
		return resp, nil
	}}
}

// hang returns a candidate that never answers until its context ends.
func hang(id string) fakeCandidate {
	return fakeCandidate{id: id, fn: func(ctx context.Context) (*http.Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
}

// skipped returns a candidate whose circuit is open.
func skipped(id string) fakeCandidate {
	return fakeCandidate{id: id, fn: func(context.Context) (*http.Response, error) {
		return nil, ErrSkipped
	}}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestSequentialFailsOverInOrder(t *testing.T) {
	first, firstBody := newResponse(http.StatusInternalServerError, "boom")
	cands := []Candidate{
		fakeCandidate{id: "a", fn: func(context.Context) (*http.Response, error) { return first, nil }},
		answer("b", http.StatusOK),
	}
	resp, err := Sequential{}.Execute(t.Context(), Request{}, cands)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := readBody(t, resp); got != "body-b" {
		t.Fatalf("served body %q, want from candidate b", got)
	}
	if !firstBody.closed.Load() {
		t.Error("discarded response from candidate a was not closed")
	}
}

func TestSequentialSkipsWithoutRecordingFailure(t *testing.T) {
	resp, err := Sequential{}.Execute(t.Context(), Request{}, []Candidate{
		skipped("a"),
		answer("b", http.StatusOK),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	resp.Body.Close()
}

func TestSequentialAllNotFound(t *testing.T) {
	_, err := Sequential{}.Execute(t.Context(), Request{}, []Candidate{
		answer("a", http.StatusNotFound),
		answer("b", http.StatusNotFound),
	})
	var ex *Exhausted
	if !errors.As(err, &ex) {
		t.Fatalf("error = %v, want *Exhausted", err)
	}
	if !ex.AllNotFound() {
		t.Fatal("AllNotFound() = false, want true when every candidate answered 404")
	}
}

func TestExhaustedMixedFailuresAreNotNotFound(t *testing.T) {
	_, err := Sequential{}.Execute(t.Context(), Request{}, []Candidate{
		answer("a", http.StatusNotFound),
		answer("b", http.StatusUnauthorized),
	})
	ex, ok := err.(*Exhausted)
	if !ok {
		t.Fatalf("error = %v, want *Exhausted", err)
	}
	if ex.AllNotFound() {
		t.Fatal("AllNotFound() = true, want false when one candidate returned 401")
	}
}

func TestExhaustedWithOnlySkippedCandidates(t *testing.T) {
	_, err := Sequential{}.Execute(t.Context(), Request{}, []Candidate{skipped("a")})
	ex, ok := err.(*Exhausted)
	if !ok || ex.AllNotFound() {
		t.Fatalf("all-skipped must be Exhausted and not AllNotFound, got %v", err)
	}
}

func TestHedgedStartsSecondWhenFirstIsSlow(t *testing.T) {
	firstCancelled := make(chan struct{})
	cands := []Candidate{
		fakeCandidate{id: "slow", fn: func(ctx context.Context) (*http.Response, error) {
			<-ctx.Done()
			close(firstCancelled)
			return nil, ctx.Err()
		}},
		answer("fast", http.StatusOK),
	}
	h := Hedged{Delay: 20 * time.Millisecond, MaxParallel: 2}

	start := time.Now()
	resp, err := h.Execute(t.Context(), Request{}, cands)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("hedged request took %v; the hedge should answer after about %v", elapsed, h.Delay)
	}
	if got := readBody(t, resp); got != "body-fast" {
		t.Fatalf("served body %q, want from the fast candidate", got)
	}
	select {
	case <-firstCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("losing attempt was not cancelled")
	}
}

func TestHedgedFastFailureStartsNextImmediately(t *testing.T) {
	cands := []Candidate{
		answer("broken", http.StatusBadGateway),
		answer("ok", http.StatusOK),
	}
	// A long delay proves the next candidate was started by the failure, not by the timer.
	h := Hedged{Delay: time.Hour, MaxParallel: 2}

	start := time.Now()
	resp, err := h.Execute(t.Context(), Request{}, cands)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	resp.Body.Close()
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("fast failure waited %v for the hedge timer", elapsed)
	}
}

func TestHedgedWinnerBodyStaysReadable(t *testing.T) {
	cands := []Candidate{answer("only", http.StatusOK)}
	resp, err := Hedged{Delay: time.Second, MaxParallel: 2}.Execute(t.Context(), Request{}, cands)
	if err != nil {
		t.Fatal(err)
	}
	// Reading after Execute returns proves the winner's context was not cancelled early.
	if got := readBody(t, resp); got != "body-only" {
		t.Fatalf("winner body = %q", got)
	}
}

func TestHedgedClosesLateLoserResponses(t *testing.T) {
	loserBody := &trackingBody{Reader: strings.NewReader("late")}
	loser := fakeCandidate{id: "late", fn: func(context.Context) (*http.Response, error) {
		time.Sleep(60 * time.Millisecond) // ignores cancellation on purpose
		return &http.Response{StatusCode: http.StatusOK, Body: loserBody}, nil
	}}
	resp, err := Hedged{Delay: 5 * time.Millisecond, MaxParallel: 2}.Execute(t.Context(), Request{}, []Candidate{
		loser,
		answer("winner", http.StatusOK),
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if loserBody.closed.Load() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("late loser response was never closed")
}

func TestHedgedCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cands := []Candidate{hang("a"), hang("b")}
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := Hedged{Delay: 5 * time.Millisecond, MaxParallel: 2}.Execute(ctx, Request{}, cands)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestHedgedNoCandidates(t *testing.T) {
	_, err := Hedged{Delay: time.Millisecond, MaxParallel: 2}.Execute(t.Context(), Request{}, nil)
	if _, ok := err.(*Exhausted); !ok {
		t.Fatalf("error = %v, want *Exhausted", err)
	}
}

func TestHedgedRespectsMaxParallel(t *testing.T) {
	var started atomic.Int32
	blocker := func(id string) fakeCandidate {
		return fakeCandidate{id: id, fn: func(ctx context.Context) (*http.Response, error) {
			started.Add(1)
			<-ctx.Done()
			return nil, ctx.Err()
		}}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, _ = Hedged{Delay: 5 * time.Millisecond, MaxParallel: 2}.Execute(ctx, Request{}, []Candidate{
		blocker("a"), blocker("b"), blocker("c"), blocker("d"),
	})
	if got := started.Load(); got != 2 {
		t.Fatalf("started %d attempts concurrently, want MaxParallel=2", got)
	}
}

func TestExhaustedErrorExplainsEachFailure(t *testing.T) {
	ex := &Exhausted{Failures: []Failure{
		{Candidate: "hub", Status: http.StatusBadGateway},
		{Candidate: "mirror", Err: errors.New("tls handshake timeout")},
	}}
	msg := ex.Error()
	for _, want := range []string{"hub: HTTP 502", "mirror: tls handshake timeout"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not explain %q", msg, want)
		}
	}
}
