package upstream

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/ociapi"
	"github.com/runui/oci-mirror-proxy/internal/scheduler"
)

// fakeClock is a manually advanced clock for deterministic breaker tests.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestBreakerLifecycle(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_000, 0)}
	b := NewBreaker(2, 30*time.Second, clock.now)

	if !b.Allow() || b.State() != Closed {
		t.Fatal("new breaker must be closed and allow requests")
	}
	b.Failure()
	if b.State() != Closed {
		t.Fatal("one failure below threshold must not open the circuit")
	}
	if !b.Allow() {
		t.Fatal("request must be allowed below threshold")
	}
	b.Failure()
	if b.State() != Open || b.Allow() {
		t.Fatal("threshold reached: circuit must be open and reject requests")
	}

	clock.advance(31 * time.Second)
	if b.State() != HalfOpen {
		t.Fatalf("state after cooldown = %v, want half-open", b.State())
	}
	if !b.Allow() {
		t.Fatal("first request after cooldown must be the probe")
	}
	if b.Allow() {
		t.Fatal("only one probe may be in flight in half-open state")
	}
	b.Failure() // failed probe reopens immediately
	if b.State() != Open {
		t.Fatalf("failed probe state = %v, want open", b.State())
	}

	clock.advance(31 * time.Second)
	if !b.Allow() {
		t.Fatal("second probe must be allowed after another cooldown")
	}
	b.Success()
	if b.State() != Closed || !b.Allow() {
		t.Fatal("successful probe must close the circuit")
	}
}

func TestBreakerAbortReleasesProbe(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_000, 0)}
	b := NewBreaker(1, time.Second, clock.now)
	b.Failure()
	clock.advance(2 * time.Second)

	if !b.Allow() {
		t.Fatal("probe must be allowed")
	}
	b.Abort() // caller cancelled: the upstream was never judged
	if !b.Allow() {
		t.Fatal("aborted probe must not block the next probe")
	}
}

func newTestEndpoint(t *testing.T, u config.Upstream, threshold int) *Endpoint {
	t.Helper()
	e, err := NewEndpoint(t.Context(), u, config.Breaker{Threshold: threshold, Cooldown: time.Hour}, Env{})
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	return e
}

func getManifest(t *testing.T, e *Endpoint, repo string) (*http.Response, error) {
	t.Helper()
	return e.Attempt(t.Context(), scheduler.Request{
		Kind:   ociapi.KindManifest,
		Method: http.MethodGet,
		Repo:   repo,
		Path:   "/v2/" + repo + "/manifests/latest",
	})
}

func TestEndpointPathPrefixAndMethod(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := newTestEndpoint(t, config.Upstream{
		ID:       "prefixed",
		URL:      srv.URL + "/hub/",
		Timeouts: config.Timeouts{FirstByte: time.Second, Overall: time.Second},
	}, 3)
	resp, err := e.Attempt(t.Context(), scheduler.Request{
		Kind: ociapi.KindManifest, Method: http.MethodHead, Repo: "library/nginx",
		Path: "/v2/library/nginx/manifests/latest",
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotPath != "/hub/v2/library/nginx/manifests/latest" {
		t.Fatalf("upstream saw path %q, want the base path prefix applied", gotPath)
	}
	if gotMethod != http.MethodHead {
		t.Fatalf("upstream saw method %q, want HEAD", gotMethod)
	}
}

func TestServerErrorsOpenBreakerAndSkipUpstream(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	e := newTestEndpoint(t, config.Upstream{
		ID:       "flaky",
		URL:      srv.URL,
		Timeouts: config.Timeouts{FirstByte: time.Second, Overall: time.Second},
	}, 2)
	for range 2 {
		resp, err := getManifest(t, e, "library/nginx")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	before := hits.Load()
	_, err := getManifest(t, e, "library/nginx")
	if !errors.Is(err, scheduler.ErrSkipped) {
		t.Fatalf("third attempt error = %v, want ErrSkipped", err)
	}
	if hits.Load() != before {
		t.Fatal("open circuit must not send any request upstream")
	}
	if got := e.Status(); got.Breaker != "open" || got.Failures != 2 {
		t.Fatalf("status = %+v, want open breaker with 2 failures", got)
	}
}

func TestNotFoundIsNotAHealthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	e := newTestEndpoint(t, config.Upstream{
		ID:       "content-miss",
		URL:      srv.URL,
		Timeouts: config.Timeouts{FirstByte: time.Second, Overall: time.Second},
	}, 1)
	for range 3 {
		resp, err := getManifest(t, e, "library/missing")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if got := e.Status(); got.Breaker != "closed" || got.Failures != 0 {
		t.Fatalf("404 responses changed health: %+v", got)
	}
}

func TestFirstByteTimeoutIsAFailure(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		// Never send headers: block until the client gives up or the test releases us.
		select {
		case <-release:
		case <-req.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	e := newTestEndpoint(t, config.Upstream{
		ID:       "blackhole",
		URL:      srv.URL,
		Timeouts: config.Timeouts{FirstByte: 100 * time.Millisecond, Overall: 10 * time.Second},
	}, 5)
	start := time.Now()
	_, err := getManifest(t, e, "library/nginx")
	if err == nil {
		t.Fatal("silent upstream must fail within the first-byte timeout")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error = %v, want a response-header timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("first-byte timeout took %v", elapsed)
	}
	if e.Status().Failures != 1 {
		t.Fatalf("timeout must count as a failure, status = %+v", e.Status())
	}
}

func TestBlobIsNotBoundedByOverallTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte("layer"))
	}))
	defer srv.Close()

	e := newTestEndpoint(t, config.Upstream{
		ID:       "slow-blob",
		URL:      srv.URL,
		Timeouts: config.Timeouts{FirstByte: time.Second, Overall: 50 * time.Millisecond},
	}, 3)
	resp, err := e.Attempt(t.Context(), scheduler.Request{
		Kind:   ociapi.KindBlob,
		Method: http.MethodGet,
		Repo:   "library/nginx",
		Path:   "/v2/library/nginx/blobs/sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("blob request must not hit the overall timeout: %v", err)
	}
	resp.Body.Close()
}

func TestPoolReconcilePreservesUnchangedState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	a := config.Upstream{ID: "a", URL: srv.URL, Timeouts: config.Timeouts{FirstByte: time.Second, Overall: time.Second}}
	b := config.Upstream{ID: "b", URL: srv.URL + "/b", Timeouts: config.Timeouts{FirstByte: time.Second, Overall: time.Second}}
	reg := config.Registry{Breaker: config.Breaker{Threshold: 5, Cooldown: time.Minute}, Upstreams: []config.Upstream{a, b}}

	p := NewPool(Env{})
	if err := p.Reconcile(t.Context(), reg); err != nil {
		t.Fatal(err)
	}
	// Make upstream "a" accumulate a failure.
	resp, _ := getManifest(t, p.endpoints[0], "x/y")
	resp.Body.Close()

	// Add a third upstream. "a" is unchanged, so its failure count must survive.
	reg.Upstreams = append(reg.Upstreams, config.Upstream{
		ID: "c", URL: srv.URL + "/c", Timeouts: config.Timeouts{FirstByte: time.Second, Overall: time.Second},
	})
	if err := p.Reconcile(t.Context(), reg); err != nil {
		t.Fatal(err)
	}
	status := p.Status()
	if len(status) != 3 || status[0].ID != "a" || status[0].Failures != 1 {
		t.Fatalf("unchanged upstream lost its state: %+v", status)
	}
}

func TestPoolDisabledUpstreamsAreNotCandidates(t *testing.T) {
	p := NewPool(Env{})
	reg := config.Registry{
		Breaker: config.Breaker{Threshold: 1, Cooldown: time.Minute},
		Upstreams: []config.Upstream{
			{ID: "on", URL: "https://a.example.com", Timeouts: config.Timeouts{FirstByte: time.Second, Overall: time.Second}},
			{ID: "off", URL: "https://b.example.com", Disabled: true, Timeouts: config.Timeouts{FirstByte: time.Second, Overall: time.Second}},
		},
	}
	if err := p.Reconcile(t.Context(), reg); err != nil {
		t.Fatal(err)
	}
	cands := p.Candidates()
	if len(cands) != 1 || cands[0].ID() != "on" {
		t.Fatalf("candidates = %v, want only the enabled upstream", cands)
	}
	if len(p.Status()) != 2 {
		t.Fatal("status must list disabled upstreams too")
	}
}

func TestPoolReconcileFailureLeavesPoolUnchanged(t *testing.T) {
	p := NewPool(Env{})
	good := config.Upstream{ID: "good", URL: "https://a.example.com", Timeouts: config.Timeouts{FirstByte: time.Second, Overall: time.Second}}
	if err := p.Reconcile(t.Context(), config.Registry{
		Breaker:   config.Breaker{Threshold: 1, Cooldown: time.Minute},
		Upstreams: []config.Upstream{good},
	}); err != nil {
		t.Fatal(err)
	}
	bad := config.Upstream{ID: "bad", URL: "https://b.example.com", TLS: config.TLS{CAFile: "/does/not/exist.pem"}, Timeouts: config.Timeouts{FirstByte: time.Second, Overall: time.Second}}
	err := p.Reconcile(t.Context(), config.Registry{
		Breaker:   config.Breaker{Threshold: 1, Cooldown: time.Minute},
		Upstreams: []config.Upstream{good, bad},
	})
	if err == nil {
		t.Fatal("unreadable CA file must fail reconciliation")
	}
	if status := p.Status(); len(status) != 1 || status[0].ID != "good" {
		t.Fatalf("failed reconcile changed the pool: %+v", status)
	}
}
