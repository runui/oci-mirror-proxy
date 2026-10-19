package frontend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/ociapi"
	"github.com/runui/oci-mirror-proxy/internal/route"
	"github.com/runui/oci-mirror-proxy/internal/scheduler"
)

type fakeRoutes map[string]route.Dispatcher

func (f fakeRoutes) Lookup(alias string) (route.Dispatcher, bool) {
	d, ok := f[alias]
	return d, ok
}

// recorder captures the last request handed to the dispatcher.
type recorder struct {
	last scheduler.Request
	fn   func() (*scheduler.Result, error)
}

func (r *recorder) Dispatch(_ context.Context, req scheduler.Request) (*scheduler.Result, error) {
	r.last = req
	return r.fn()
}

func okResponse(body string) (*scheduler.Result, error) {
	h := http.Header{}
	h.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	h.Set("Docker-Content-Digest", "sha256:abc")
	h.Set("X-Internal-Secret", "must-not-leak")
	return scheduler.Stream(&http.Response{
		StatusCode: http.StatusOK,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}), nil
}

func exhausted(statuses ...int) (*scheduler.Result, error) {
	ex := &scheduler.Exhausted{}
	for _, s := range statuses {
		ex.Failures = append(ex.Failures, scheduler.Failure{Candidate: "x", Status: s})
	}
	return nil, ex
}

func serve(t *testing.T, routes Routes, method, path string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	New(routes).ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Errors) != 1 {
		t.Fatalf("body is not a registry error: %q (%v)", rec.Body.String(), err)
	}
	return body.Errors[0].Code
}

func TestPingIsAnsweredLocally(t *testing.T) {
	rec := serve(t, fakeRoutes{"docker.io": &recorder{fn: func() (*scheduler.Result, error) {
		t.Error("ping must not reach upstreams")
		return nil, nil
	}}}, http.MethodGet, "/v2/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ping status = %d", rec.Code)
	}
	if rec.Header().Get("Docker-Distribution-Api-Version") != "registry/2.0" {
		t.Fatal("ping must advertise the registry API version")
	}
}

func TestUnconfiguredAliasIsNotFound(t *testing.T) {
	rec := serve(t, fakeRoutes{}, http.MethodGet, "/ghcr.io/v2/", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unconfigured registry", rec.Code)
	}
}

func TestMethodsOtherThanGetAndHeadAreRejected(t *testing.T) {
	rec := serve(t, fakeRoutes{}, http.MethodPost, "/v2/library/nginx/manifests/latest", nil)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST answered %d with Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
}

func TestManifestIsRelayedWithAllowlistedHeaders(t *testing.T) {
	rec := &recorder{fn: func() (*scheduler.Result, error) { return okResponse(`{"schemaVersion":2}`) }}
	h := http.Header{}
	h.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
	h.Set("Authorization", "Basic c2VjcmV0")
	h.Set("Accept-Encoding", "gzip")

	resp := serve(t, fakeRoutes{"docker.io": rec}, http.MethodGet, "/v2/library/nginx/manifests/latest", h)
	if resp.Code != http.StatusOK || resp.Body.String() != `{"schemaVersion":2}` {
		t.Fatalf("response = %d %q", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Docker-Content-Digest"); got != "sha256:abc" {
		t.Fatalf("digest header = %q", got)
	}
	if resp.Header().Get("X-Internal-Secret") != "" {
		t.Fatal("headers outside the allowlist must not be relayed")
	}
	if rec.last.Repo != "library/nginx" || rec.last.Path != "/v2/library/nginx/manifests/latest" {
		t.Fatalf("dispatched %+v", rec.last)
	}
	if rec.last.Header.Get("Authorization") != "" {
		t.Fatal("client credentials must not be forwarded upstream")
	}
	if rec.last.Header.Get("Accept") == "" {
		t.Fatal("Accept must be forwarded upstream")
	}
}

func TestHeadHasNoBody(t *testing.T) {
	rec := &recorder{fn: func() (*scheduler.Result, error) { return okResponse("payload") }}
	resp := serve(t, fakeRoutes{"docker.io": rec}, http.MethodHead, "/v2/library/nginx/manifests/latest", nil)
	if resp.Code != http.StatusOK || resp.Body.Len() != 0 {
		t.Fatalf("HEAD returned %d with body %q", resp.Code, resp.Body.String())
	}
}

func TestAllNotFoundMapsToRegistryNotFound(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantCode string
	}{
		{"manifest", "/v2/library/nginx/manifests/latest", "MANIFEST_UNKNOWN"},
		{"blob", "/v2/library/nginx/blobs/sha256:" + strings.Repeat("a", 64), "BLOB_UNKNOWN"},
		{"tags", "/v2/library/nginx/tags/list", "NAME_UNKNOWN"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{fn: func() (*scheduler.Result, error) { return exhausted(404, 404) }}
			resp := serve(t, fakeRoutes{"docker.io": rec}, http.MethodGet, tc.path, nil)
			if resp.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", resp.Code)
			}
			if got := errorCode(t, resp); got != tc.wantCode {
				t.Fatalf("code = %s, want %s", got, tc.wantCode)
			}
		})
	}
}

func TestReferrersNotFoundIsEmptyIndex(t *testing.T) {
	rec := &recorder{fn: func() (*scheduler.Result, error) { return exhausted(404) }}
	resp := serve(t, fakeRoutes{"docker.io": rec}, http.MethodGet, "/v2/library/nginx/referrers/sha256:"+strings.Repeat("a", 64), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with an empty index", resp.Code)
	}
	var idx struct {
		Manifests []any `json:"manifests"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &idx); err != nil || len(idx.Manifests) != 0 {
		t.Fatalf("body = %q, want an empty image index", resp.Body.String())
	}
}

func TestMixedFailuresBecomeBadGateway(t *testing.T) {
	rec := &recorder{fn: func() (*scheduler.Result, error) { return exhausted(404, 503) }}
	resp := serve(t, fakeRoutes{"docker.io": rec}, http.MethodGet, "/v2/library/nginx/manifests/latest", nil)
	if resp.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 when any upstream failed for a non-404 reason", resp.Code)
	}
	if got := errorCode(t, resp); got != "UNAVAILABLE" {
		t.Fatalf("code = %s, want UNAVAILABLE", got)
	}
}

func TestClientCancellationWritesNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rec := &recorder{fn: func() (*scheduler.Result, error) { return nil, context.Canceled }}
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v2/library/nginx/manifests/latest", nil)
	w := httptest.NewRecorder()
	New(fakeRoutes{"docker.io": rec}).ServeHTTP(w, req)
	if w.Body.Len() != 0 {
		t.Fatalf("a cancelled client received a body: %q", w.Body.String())
	}
}

func TestUnexpectedErrorIsBadGateway(t *testing.T) {
	rec := &recorder{fn: func() (*scheduler.Result, error) { return nil, errors.New("boom") }}
	resp := serve(t, fakeRoutes{"docker.io": rec}, http.MethodGet, "/v2/library/nginx/manifests/latest", nil)
	if resp.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.Code)
	}
}

func TestPrefixedAliasDispatchesToItsRegistry(t *testing.T) {
	rec := &recorder{fn: func() (*scheduler.Result, error) { return okResponse("{}") }}
	resp := serve(t, fakeRoutes{"ghcr.io": rec}, http.MethodGet, "/ghcr.io/v2/owner/repo/manifests/v1", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	if rec.last.Registry != "ghcr.io" || rec.last.Kind != ociapi.KindManifest {
		t.Fatalf("dispatched %+v", rec.last)
	}
}

func TestMalformedPathIsNotFound(t *testing.T) {
	rec := serve(t, fakeRoutes{"docker.io": &recorder{fn: func() (*scheduler.Result, error) {
		t.Error("malformed path must not reach upstreams")
		return nil, nil
	}}}, http.MethodGet, "/v2/../etc/passwd", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestBlobHandoffHasNoPayloadOrLeakedHeaders(t *testing.T) {
	location := "https://cdn.example/blob?signature=private%2Fvalue"
	rec := &recorder{fn: func() (*scheduler.Result, error) {
		result := scheduler.BlobRedirect(location)
		result.Header.Set("Content-Length", "1073741824")
		result.Header.Set("Set-Cookie", "secret")
		return result, nil
	}}
	resp := serve(t, fakeRoutes{"docker.io": rec}, http.MethodGet, "/v2/library/nginx/blobs/sha256:"+strings.Repeat("a", 64), nil)
	if resp.Code != 307 || resp.Header().Get("Location") != location || resp.Header().Get("Cache-Control") != "no-store" || resp.Header().Get("Content-Length") != "0" || resp.Body.Len() != 0 || resp.Header().Get("Set-Cookie") != "" {
		t.Fatalf("unsafe handoff: status=%d headers=%v body=%q", resp.Code, resp.Header(), resp.Body.String())
	}
	if !rec.last.AllowBlobRedirect {
		t.Fatal("frontend did not allow per-request download selection")
	}
}
