package scheduler

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Hedged races candidates. It starts the first candidate immediately. Whenever
// Delay passes without any answer, it starts another candidate in parallel, up
// to MaxParallel attempts in flight. A failed attempt starts the next candidate
// at once. The first acceptable response wins, and every other attempt is cancelled.
type Hedged struct {
	Delay       time.Duration
	MaxParallel int
}

type attempt struct {
	idx  int
	id   string
	resp *http.Response
	err  error
}

// Execute implements Strategy.
func (h Hedged) Execute(ctx context.Context, req Request, cands []Candidate) (*http.Response, error) {
	logRoute(ctx, "hedged", cands)
	if len(cands) == 0 {
		debug(ctx, "route exhausted", "attempts", 0)
		return nil, &Exhausted{}
	}
	maxParallel := max(h.MaxParallel, 1)

	// Buffered to len(cands): every attempt can deliver its result without
	// blocking, even after Execute has returned.
	results := make(chan attempt, len(cands))
	cancels := make([]context.CancelFunc, 0, len(cands))
	next, inFlight := 0, 0
	var failures []Failure

	// launch starts cands[next]. Its index equals its position in cancels.
	launch := func() {
		c, idx := cands[next], next
		next++
		inFlight++
		// Each attempt has its own context, so cancelling a loser can never
		// affect the winner's response body.
		actx, cancel := context.WithCancel(ctx)
		cancels = append(cancels, cancel)
		go func() {
			resp, err := runAttempt(actx, req, c)
			results <- attempt{idx: idx, id: c.ID(), resp: resp, err: err}
		}()
	}

	launch()
	for inFlight > 0 {
		// The hedge timer is armed only while another candidate could still start.
		// A nil channel never fires, which disables that select case.
		var hedge <-chan time.Time
		if inFlight < maxParallel && next < len(cands) {
			hedge = time.After(h.Delay)
		}

		select {
		case a := <-results:
			inFlight--
			if a.err == nil && Acceptable(a.resp) {
				debug(ctx, "upstream selected", candidateAttrs(cands[a.idx])...)
				cancelExcept(cancels, a.idx)
				go drain(results, inFlight)
				return AttachCancel(a.resp, cancels[a.idx]), nil
			}
			if errors.Is(a.err, ErrSkipped) {
				// Not a real attempt: move on without recording a failure.
				if next < len(cands) {
					launch()
				}
				continue
			}
			failures = append(failures, newFailure(a.id, a.resp, a.err))
			discard(a.resp)
			if next < len(cands) {
				launch() // fast fail: do not wait for the hedge timer
			}

		case <-hedge:
			debug(ctx, "hedge delay elapsed", "delay", h.Delay.String())
			// Nothing has answered within Delay, so hedge with one more candidate.
			launch()

		case <-ctx.Done():
			cancelExcept(cancels, -1)
			go drain(results, inFlight)
			return nil, ctx.Err()
		}
	}
	debug(ctx, "route exhausted", "attempts", len(failures))
	return nil, &Exhausted{Failures: failures}
}

// cancelExcept cancels every attempt except keep. Pass -1 to cancel all of them.
func cancelExcept(cancels []context.CancelFunc, keep int) {
	for i, cancel := range cancels {
		if i != keep {
			cancel()
		}
	}
}

// drain collects the results of attempts still running after a decision has
// been made, and closes any response they produce.
func drain(results <-chan attempt, pending int) {
	for range pending {
		if a := <-results; a.resp != nil {
			discard(a.resp)
		}
	}
}
