package scheduler

import (
	"context"
	"errors"
)

// Sequential tries candidates one at a time, in priority order. A candidate is
// only contacted after the previous one has failed. This is the default strategy.
type Sequential struct{}

// Execute implements Strategy.
func (Sequential) Execute(ctx context.Context, req Request, cands []Candidate) (*Result, error) {
	logRoute(ctx, "sequential", cands)
	var failures []Failure
	for _, c := range cands {
		resp, err := runAttempt(ctx, req, c)
		if err == nil && Acceptable(resp) {
			debug(ctx, "upstream selected", candidateAttrs(c)...)
			return resp, nil
		}
		if errors.Is(err, ErrSkipped) {
			continue
		}
		failures = append(failures, newFailure(c.ID(), resp, err))
		discard(resp)
		if ctx.Err() != nil {
			break // the caller has gone; trying more upstreams is pointless
		}
	}
	debug(ctx, "route exhausted", "attempts", len(failures))
	return nil, &Exhausted{Failures: failures}
}
