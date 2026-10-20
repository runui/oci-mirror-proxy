package upstream

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sync"
	"sync/atomic"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/auth"
	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/ociapi"
	"github.com/runui/oci-mirror-proxy/internal/scheduler"
	"github.com/runui/oci-mirror-proxy/internal/secret"
)

// Env holds the dependencies shared by every endpoint.
type Env struct {
	Secrets *secret.Resolver
	// Now is the clock used by breakers and token expiry. It defaults to time.Now.
	Now func() time.Time
}

// Endpoint is one upstream registry. It implements scheduler.Candidate.
type Endpoint struct {
	refs    atomic.Int64 // generation owners and live attempts, including background probes
	spec    config.Upstream
	base    *url.URL
	client  *http.Client
	auth    auth.Authorizer
	breaker *Breaker
	stats   stats
	// delegateURL is injectable for local TLS tests; production uses the public
	// destination checks in publicDownloadURL, never a configurable bypass.
	delegateURL func(context.Context, *url.URL) bool
}

type stats struct {
	requests    atomic.Int64
	failures    atomic.Int64
	lastLatency atomic.Int64 // nanoseconds until response headers arrived
}

// NewEndpoint builds an upstream from its definition and the registry's breaker settings.
func NewEndpoint(ctx context.Context, spec config.Upstream, breaker config.Breaker, env Env) (*Endpoint, error) {
	if env.Now == nil {
		env.Now = time.Now
	}
	base, err := url.Parse(spec.URL)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	transport, err := newTransport(spec.TLS, spec.Timeouts.Connect, spec.Timeouts.FirstByte)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: transport}
	a, err := auth.New(ctx, spec.Auth, auth.Options{
		Secrets:    env.Secrets,
		HTTPClient: client,
		Now:        env.Now,
	})
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	return &Endpoint{
		spec:    spec,
		base:    base,
		client:  client,
		auth:    a,
		breaker: NewBreaker(breaker.Threshold, breaker.Cooldown, env.Now),
	}, nil
}

// ID returns the upstream identifier, unique within its registry.
func (e *Endpoint) ID() string {
	if e.spec.ID != "" {
		return e.spec.ID
	}
	return e.spec.URL
}

// URL returns the configured base URL, never an authenticated or redirected URL.
func (e *Endpoint) URL() string { return e.spec.URL }

// Spec returns the definition this endpoint was built from.
func (e *Endpoint) Spec() config.Upstream { return e.spec }

// Attempt sends one request to this upstream and feeds the outcome to the breaker.
// All health logic lives here, so strategies stay unaware of it.
func (e *Endpoint) Attempt(ctx context.Context, req scheduler.Request) (*scheduler.Result, error) {
	// A hedged loser or adaptive probe may outlive its downstream handler.
	// Its transport cannot be retired until its own response has been closed.
	e.Retain()
	resp, err := e.attempt(ctx, req)
	if err != nil || resp == nil || resp.Body == nil || resp.RedirectLocation() != "" {
		e.Release()
	} else {
		resp.Body = &endpointBody{ReadCloser: resp.Body, release: e.Release}
	}
	return resp, err
}

type endpointBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
	err     error
}

func (b *endpointBody) Close() error {
	b.once.Do(func() {
		b.err = b.ReadCloser.Close()
		b.release()
	})
	return b.err
}

// Retain must be called while the caller already owns a live generation or
// attempt reference; it must not resurrect an endpoint after final Release.
func (e *Endpoint) Retain() { e.refs.Add(1) }
func (e *Endpoint) Release() {
	if e.refs.Add(-1) == 0 {
		e.Close()
	}
}

func (e *Endpoint) attempt(ctx context.Context, req scheduler.Request) (*scheduler.Result, error) {
	if !e.breaker.Allow() {
		return nil, scheduler.ErrSkipped
	}
	e.stats.requests.Add(1)
	started := time.Now()
	resp, err := e.send(ctx, req)
	e.stats.lastLatency.Store(int64(time.Since(started)))

	switch {
	case ctx.Err() != nil:
		// The caller cancelled (for example a hedging loser). Not the upstream's fault.
		e.breaker.Abort()
	case err != nil || resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		// Transport errors, server errors and rate limiting count against the upstream.
		e.breaker.Failure()
		e.stats.failures.Add(1)
	default:
		// 2xx to 4xx mean the upstream is alive. A 404 is a content miss, not a health problem.
		e.breaker.Success()
	}
	return resp, err
}

// send applies the overall timeout to metadata requests. Blob transfers are not
// bounded by it, because a large layer can legitimately take much longer.
func (e *Endpoint) send(ctx context.Context, req scheduler.Request) (*scheduler.Result, error) {
	if req.Kind == ociapi.KindBlob || e.spec.Timeouts.Overall <= 0 {
		return e.roundTrip(ctx, req)
	}
	tctx, cancel := context.WithTimeout(ctx, e.spec.Timeouts.Overall)
	resp, err := e.roundTrip(tctx, req)
	if err != nil {
		cancel()
		return nil, err
	}
	// The body may still be read after headers arrive, so the timer is released on Close.
	return scheduler.AttachCancel(resp, cancel), nil
}

// roundTrip sends the request and answers one authentication challenge, if any.
func (e *Endpoint) roundTrip(ctx context.Context, req scheduler.Request) (*scheduler.Result, error) {
	resp, err := e.do(ctx, req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	retry, err := e.auth.Challenge(ctx, resp.Response, req.Repo)
	if err != nil {
		resp.Body.Close()
		return nil, err
	}
	if !retry {
		return resp, nil
	}
	// Retry once. A second 401 fails candidate selection, but is not a breaker
	// health failure: reachable 4xx responses still mean the upstream is alive.
	resp.Body.Close()
	return e.do(ctx, req)
}

func (e *Endpoint) do(ctx context.Context, req scheduler.Request) (*scheduler.Result, error) {
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, e.target(req.Path), nil)
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	httpReq.Header = req.Header.Clone()
	if httpReq.Header == nil {
		httpReq.Header = make(http.Header)
	}
	if err := e.auth.Authorize(ctx, httpReq, req.Repo); err != nil {
		return nil, fmt.Errorf("authorize: %w", err)
	}
	if req.AllowBlobRedirect && req.Kind == ociapi.KindBlob && req.Method == http.MethodGet {
		return e.blobRequest(httpReq, req)
	}
	resp, err := e.client.Do(httpReq)
	return scheduler.Stream(resp), err
}

// target joins the upstream base URL, which may carry a path prefix, with the
// canonical API path. The API path was validated by ociapi, so no segment can escape.
func (e *Endpoint) target(apiPath string) string {
	u := *e.base
	u.Path = path.Join("/", e.base.Path, apiPath)
	u.RawPath = ""
	return u.String()
}

// Status returns a point-in-time view of this endpoint for operators.
func (e *Endpoint) Status() Status {
	return Status{
		ID:          e.spec.ID,
		URL:         e.spec.URL,
		Disabled:    e.spec.Disabled,
		Breaker:     e.breaker.State().String(),
		Requests:    e.stats.requests.Load(),
		Failures:    e.stats.failures.Load(),
		LastLatency: time.Duration(e.stats.lastLatency.Load()).String(),
	}
}

// Close releases idle connections. It is called when the endpoint is dropped
// from a pool; requests already in flight finish normally.
func (e *Endpoint) Close() {
	e.client.CloseIdleConnections()
}
