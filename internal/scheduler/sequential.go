package scheduler

import (
	"context"
	"errors"
	"net/http"
)

// Sequential tries candidates one at a time, in priority order. A candidate is
// only contacted after the previous one has failed. This is the default strategy.
type Sequential struct{}

// Execute implements Strategy.
func (Sequential) Execute(ctx context.Context, req Request, cands []Candidate) (*http.Response, error) {
	var failures []Failure
	for _, c := range cands {
		resp, err := c.Attempt(ctx, req)
		if err == nil && Acceptable(resp) {
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
	return nil, &Exhausted{Failures: failures}
}
