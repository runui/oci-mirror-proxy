// Package scheduler decides how one request is spread across candidate upstreams.
// It knows nothing about HTTP clients, authentication or health tracking; those
// live behind the Candidate interface.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/runui/oci-mirror-proxy/internal/ociapi"
)

// Request is what the frontend asks the routing layer to fetch.
type Request struct {
	Registry string // registry alias, used for logging
	Kind     ociapi.Kind
	Method   string // GET or HEAD
	Repo     string // repository name, used to build auth scopes
	Path     string // canonical API path, e.g. /v2/library/nginx/manifests/latest
	Header   http.Header
}

// ErrSkipped is returned by a candidate that refused to run, for example because
// its circuit is open. Strategies move on without recording a failure.
var ErrSkipped = errors.New("candidate skipped")

// Candidate is one upstream as seen by a strategy.
type Candidate interface {
	ID() string
	// Attempt performs exactly one request. It returns ErrSkipped instead of
	// sending anything when the candidate is unavailable. A nil error always
	// comes with a non-nil response.
	Attempt(ctx context.Context, req Request) (*http.Response, error)
}

// Strategy spreads one request over candidates given in priority order.
// Implementations must close every response they discard, and must return an
// *Exhausted error when no candidate produced an acceptable response.
type Strategy interface {
	Execute(ctx context.Context, req Request, cands []Candidate) (*http.Response, error)
}

// Acceptable reports whether a response can be handed to the client.
// Anything outside 2xx, including 404, means "try the next candidate".
func Acceptable(resp *http.Response) bool {
	return resp.StatusCode/100 == 2
}

// Failure records why one candidate did not answer.
type Failure struct {
	Candidate string
	Status    int // zero when no response was received
	Err       error
}

// Exhausted is returned when no candidate produced an acceptable response.
type Exhausted struct {
	Failures []Failure
}

func (e *Exhausted) Error() string {
	if len(e.Failures) == 0 {
		return "no upstream available"
	}
	return fmt.Sprintf("all %d upstream attempts failed", len(e.Failures))
}

// AllNotFound reports whether every attempted candidate answered 404. The
// frontend uses it to answer 404 instead of 502, which lets dockerd fall back
// to the official registry.
func (e *Exhausted) AllNotFound() bool {
	if len(e.Failures) == 0 {
		return false
	}
	for _, f := range e.Failures {
		if f.Status != http.StatusNotFound {
			return false
		}
	}
	return true
}

func newFailure(id string, resp *http.Response, err error) Failure {
	f := Failure{Candidate: id, Err: err}
	if resp != nil {
		f.Status = resp.StatusCode
	}
	return f
}

// discard releases a response that will not be used. It is nil-safe.
// The body is closed without draining it, so a slow upstream cannot stall
// the strategy.
func discard(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}

// AttachCancel ties cancel to the lifetime of resp's body. The request context
// stays alive while the body is read, and is released when the body is closed.
func AttachCancel(resp *http.Response, cancel context.CancelFunc) *http.Response {
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}
