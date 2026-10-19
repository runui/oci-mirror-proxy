// Package frontend serves the read-only Registry v2 API that dockerd talks to.
// It parses paths, answers the API root itself, and maps dispatch outcomes to
// registry error responses.
package frontend

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/runui/oci-mirror-proxy/internal/ociapi"
	"github.com/runui/oci-mirror-proxy/internal/route"
	"github.com/runui/oci-mirror-proxy/internal/scheduler"
)

// Routes resolves a registry alias to its dispatcher. route.Table satisfies it.
type Routes interface {
	Lookup(alias string) (route.Dispatcher, bool)
}

// forwardedRequestHeaders are the only client headers passed upstream. Client
// credentials are deliberately dropped: upstream authentication belongs to the proxy.
var forwardedRequestHeaders = []string{"Accept", "Range", "User-Agent"}

// relayedResponseHeaders is the allowlist of upstream headers returned to the client.
var relayedResponseHeaders = []string{
	"Content-Type",
	"Content-Length",
	"Content-Range",
	"Accept-Ranges",
	"Docker-Content-Digest",
	"Docker-Distribution-Api-Version",
	"ETag",
	"Last-Modified",
}

const emptyIndex = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`

// Handler is the HTTP entry point for the registry API.
type Handler struct {
	routes Routes
}

// New creates a Handler that dispatches through routes.
func New(routes Routes) *Handler {
	return &Handler{routes: routes}
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "only GET and HEAD are supported")
		return
	}
	parsed, err := ociapi.Parse(r.URL.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "NAME_UNKNOWN", "unrecognised registry API path")
		return
	}
	dispatcher, ok := h.routes.Lookup(parsed.Alias)
	if !ok {
		writeError(w, http.StatusNotFound, "NAME_UNKNOWN", fmt.Sprintf("registry %q is not configured", parsed.Alias))
		return
	}
	if parsed.Kind == ociapi.KindPing {
		// The API root is answered locally. Upstream authentication happens per request.
		w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
		w.WriteHeader(http.StatusOK)
		return
	}

	req := scheduler.Request{
		Registry:          parsed.Alias,
		Kind:              parsed.Kind,
		Method:            r.Method,
		Repo:              parsed.Repo,
		Path:              parsed.Path,
		Header:            forwardedHeaders(r.Header),
		AllowBlobRedirect: true,
		ClientHost:        r.Host,
	}
	resp, err := dispatcher.Dispatch(r.Context(), req)
	if err != nil {
		writeFailure(w, r, parsed.Kind, err)
		return
	}
	defer resp.Body.Close()
	if location := resp.RedirectLocation(); location != "" {
		// Do not use http.Redirect: its HTML body repeats the signed URL. A
		// handoff commits ownership to the client; failures after this point
		// cannot fall back to proxy-side resumption. Never cache expiring URLs.
		w.Header().Set("Location", location)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusTemporaryRedirect)
		return
	}

	relayHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, resp.Body)
	}
}

// writeFailure translates a dispatch error into the response dockerd expects.
// A 404 lets dockerd fall back to the official registry. A 502 does as well,
// because dockerd treats any mirror failure as a reason to try the next endpoint.
func writeFailure(w http.ResponseWriter, r *http.Request, kind ociapi.Kind, err error) {
	if r.Context().Err() != nil {
		return // the client has gone away; there is nobody to answer
	}
	ex, ok := errors.AsType[*scheduler.Exhausted](err)
	if !ok || !ex.AllNotFound() {
		writeError(w, http.StatusBadGateway, "UNAVAILABLE", "no upstream could serve the request")
		return
	}
	switch kind {
	case ociapi.KindReferrers:
		// Referrers are optional metadata, so "none found" is a valid answer.
		w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, emptyIndex)
	case ociapi.KindManifest:
		writeError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown")
	case ociapi.KindBlob:
		writeError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown")
	default:
		writeError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known to registry")
	}
}

type errorBody struct {
	Errors []errorItem `json:"errors"`
}

type errorItem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError writes a response in the registry error format.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Errors: []errorItem{{Code: code, Message: message}}})
}

func forwardedHeaders(src http.Header) http.Header {
	out := make(http.Header, len(forwardedRequestHeaders))
	for _, k := range forwardedRequestHeaders {
		if v := src.Values(k); len(v) > 0 {
			out[k] = slices.Clone(v)
		}
	}
	return out
}

func relayHeaders(dst, src http.Header) {
	for _, k := range relayedResponseHeaders {
		if v := src.Values(k); len(v) > 0 {
			dst[k] = slices.Clone(v)
		}
	}
}
