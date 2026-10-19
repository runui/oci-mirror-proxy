package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type loggerKey struct{}

// WithLogger carries request-scoped logging through racing and blob resumption.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

func debug(ctx context.Context, message string, attrs ...any) {
	if logger, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		logger.DebugContext(ctx, message, attrs...)
	}
}

// DebugDelivery records the decision, never the signed download capability.
// A handoff log means headers were verified, not that the client finished it.
func DebugDelivery(ctx context.Context, mode, reason string) {
	debug(ctx, "blob delivery selected", "delivery", mode, "reason", reason)
}

func candidateAttrs(c Candidate) []any {
	attrs := []any{"upstream", c.ID()}
	if endpoint, ok := c.(interface{ URL() string }); ok {
		attrs = append(attrs, "via", endpoint.URL())
	}
	return attrs
}

func runAttempt(ctx context.Context, req Request, c Candidate) (*Result, error) {
	attrs := candidateAttrs(c)
	attrs = append(attrs, "range", req.Header.Get("Range"))
	debug(ctx, "upstream attempt started", attrs...)
	start := time.Now()
	resp, err := c.Attempt(ctx, req)
	attrs = append(attrs, "elapsed", time.Since(start).String())
	if resp != nil {
		attrs = append(attrs, "status", resp.StatusCode)
	}
	if err != nil {
		// HTTP errors can contain signed redirect URLs; log only their cause.
		attrs = append(attrs, "error", withoutURL(err))
	}
	switch {
	case errors.Is(err, ErrSkipped):
		debug(ctx, "upstream skipped", attrs...)
	case ctx.Err() != nil:
		debug(ctx, "upstream attempt cancelled", attrs...)
	case err == nil && Acceptable(resp):
		debug(ctx, "upstream hit", attrs...)
	default:
		debug(ctx, "upstream attempt failed", attrs...)
	}
	return resp, err
}

func logRoute(ctx context.Context, strategy string, cands []Candidate) {
	ids := make([]string, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, c.ID())
	}
	debug(ctx, "route selected", "strategy", strategy, "candidates", ids)
}
