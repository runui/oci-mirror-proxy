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
	"net/url"
	"strings"

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
	// AllowBlobRedirect permits delegation; it never guarantees it. The zero
	// value requires a stream, so internal probes/resumes cannot accidentally
	// return a redirect after downstream bytes have already been committed.
	AllowBlobRedirect bool
	// ClientHost prevents delegating back to this proxy even when it is exposed
	// through a public hostname. It is not an authorization or reachability hint.
	ClientHost string
}

// Result distinguishes a fetched response from a verified blob handoff without
// trusting arbitrary upstream Location headers. Response is always non-nil on
// success; for a handoff it is a normalized, empty 307 envelope, not a live body.
type Result struct {
	*http.Response
	redirect string
}

func Stream(resp *http.Response) *Result {
	if resp == nil {
		return nil
	}
	return &Result{Response: resp}
}

// BlobRedirect must only be constructed after the endpoint has checked that the
// location can be fetched without proxy credentials. It owns no network stream.
func BlobRedirect(location string) *Result {
	return &Result{Response: &http.Response{
		StatusCode: http.StatusTemporaryRedirect,
		Header:     make(http.Header), Body: http.NoBody, ContentLength: 0,
	}, redirect: location}
}

// RedirectLocation is separate from Header: an unverified upstream 3xx must
// never become acceptable merely because it carries a Location header.
func (r *Result) RedirectLocation() string { return r.redirect }

// ErrSkipped is returned by a candidate that refused to run, for example because
// its circuit is open. Strategies move on without recording a failure.
var ErrSkipped = errors.New("candidate skipped")

// Candidate is one upstream as seen by a strategy.
type Candidate interface {
	ID() string
	// Attempt performs exactly one request. It returns ErrSkipped instead of
	// sending anything when the candidate is unavailable. A nil error always
	// comes with a non-nil response.
	Attempt(ctx context.Context, req Request) (*Result, error)
}

// retainCandidate pins transports across goroutine launch: the parent HTTP
// handler can finish before a hedged loser or adaptive probe even starts.
func retainCandidate(c Candidate) func() {
	if owner, ok := c.(interface {
		Retain()
		Release()
	}); ok {
		owner.Retain()
		return owner.Release
	}
	return func() {}
}

// Strategy spreads one request over candidates given in priority order.
// Implementations must close every response they discard, and must return an
// *Exhausted error when no candidate produced an acceptable response.
type Strategy interface {
	Execute(ctx context.Context, req Request, cands []Candidate) (*Result, error)
}

// Acceptable reports whether a response can be handed to the client.
// Only fetched 2xx responses or explicitly verified handoffs are acceptable.
func Acceptable(resp *Result) bool {
	return resp != nil && resp.Response != nil && (resp.StatusCode/100 == 2 || resp.redirect != "")
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

// Error lists why each attempted candidate failed, so logs explain a 502.
func (e *Exhausted) Error() string {
	if len(e.Failures) == 0 {
		return "no upstream available"
	}
	parts := make([]string, 0, len(e.Failures))
	for _, f := range e.Failures {
		reason := "no response"
		switch {
		case f.Status != 0:
			reason = fmt.Sprintf("HTTP %d", f.Status)
		case f.Err != nil:
			reason = f.Err.Error()
		}
		parts = append(parts, f.Candidate+": "+reason)
	}
	return fmt.Sprintf("all %d upstream attempts failed (%s)", len(e.Failures), strings.Join(parts, "; "))
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

func newFailure(id string, resp *Result, err error) Failure {
	// Exhausted is also logged by outer middleware. Strip URL wrappers here,
	// not just in debug tracing, so signed CDN capabilities cannot leak when
	// every candidate fails before a response is available.
	f := Failure{Candidate: id, Err: withoutURL(err)}
	if resp != nil {
		f.Status = resp.StatusCode
	}
	return f
}

// URL wrappers include signed CDN capabilities. All error logging paths,
// including adaptive probes, must use only the underlying transport cause.
func withoutURL(err error) error {
	var urlErr *url.Error
	for errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return err
}

// discard releases a response that will not be used. It is nil-safe.
// The body is closed without draining it, so a slow upstream cannot stall
// the strategy.
func discard(resp *Result) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}

// AttachCancel ties cancel to the lifetime of resp's body. The request context
// stays alive while the body is read, and is released when the body is closed.
func AttachCancel(resp *Result, cancel context.CancelFunc) *Result {
	if resp.redirect != "" {
		cancel() // a handoff has no body whose lifetime needs the attempt context
		return resp
	}
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
