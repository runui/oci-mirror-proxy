// Package middleware provides route.Middleware implementations: request logging
// and resumable blob downloads.
package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/route"
	"github.com/runui/oci-mirror-proxy/internal/scheduler"
)

// isContentMiss reports whether err means every upstream answered 404.
func isContentMiss(err error) bool {
	ex, ok := errors.AsType[*scheduler.Exhausted](err)
	return ok && ex.AllNotFound()
}

// Logging records one structured line per dispatched request. Client
// cancellations are logged at debug level, and content misses at info level,
// because both are routine. Only genuine failures are logged as warnings.
func Logging(logger *slog.Logger) route.Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next route.Dispatcher) route.Dispatcher {
		return route.DispatcherFunc(func(ctx context.Context, req scheduler.Request) (*http.Response, error) {
			start := time.Now()
			resp, err := next.Dispatch(ctx, req)
			attrs := []any{
				"registry", req.Registry,
				"kind", req.Kind.String(),
				"repo", req.Repo,
				"elapsed", time.Since(start).String(),
			}
			switch {
			case err != nil && ctx.Err() != nil:
				logger.Debug("request cancelled by client", attrs...)
			case isContentMiss(err):
				// Normal: dockerd will fall back to the official registry.
				logger.Info("content not found on any upstream", attrs...)
			case err != nil:
				logger.Warn("request failed", append(attrs, "error", err)...)
			default:
				logger.Debug("request served", append(attrs, "status", resp.StatusCode)...)
			}
			return resp, err
		})
	}
}
