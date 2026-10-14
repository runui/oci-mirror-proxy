// Package fakeregistry is an in-memory Registry v2 server for tests. It reproduces
// the failure modes the proxy must survive: silence, truncated transfers, rejected
// range requests, hard status overrides, and token authentication.
package fakeregistry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Server is a fake Registry v2 endpoint.
type Server struct {
	*httptest.Server

	mu        sync.Mutex
	blobs     map[string][]byte // digest -> content
	manifests map[string][]byte // "repo ref" -> content, stored by tag and by digest
	mediaType string
	token     string // when set, every /v2/ request needs "Bearer <token>"

	requests    atomic.Int64
	hang        atomic.Bool  // never answer; block until the client gives up
	status      atomic.Int32 // when non-zero, answer every /v2/ request with this status
	cutAfter    atomic.Int64 // when positive, whole-blob responses are truncated after this many bytes
	rejectRange atomic.Bool  // answer Range requests with 500
}

// New starts a fake registry that is closed when the test finishes.
func New(t testing.TB) *Server {
	t.Helper()
	s := Start()
	t.Cleanup(s.Close)
	return s
}

// Start starts a fake registry on a random local port. The caller must Close it.
func Start() *Server {
	s := &Server{
		blobs:     make(map[string][]byte),
		manifests: make(map[string][]byte),
		mediaType: "application/vnd.oci.image.manifest.v1+json",
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Digest computes the sha256 digest in the registry format.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// PutBlob stores a blob and returns its digest.
func (s *Server) PutBlob(data []byte) string {
	d := Digest(data)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blobs[d] = bytes.Clone(data)
	return d
}

// PutManifest stores a manifest under a tag and returns its digest.
func (s *Server) PutManifest(repo, tag string, body []byte) string {
	d := Digest(body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manifests[repo+" "+tag] = bytes.Clone(body)
	s.manifests[repo+" "+d] = bytes.Clone(body)
	return d
}

// RequireToken makes every /v2/ request demand the given bearer token.
// An empty token disables the requirement.
func (s *Server) RequireToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = token
}

// SetHang makes the server accept connections and then never answer.
func (s *Server) SetHang(on bool) { s.hang.Store(on) }

// SetStatus makes every /v2/ request answer with code. Zero restores normal behaviour.
func (s *Server) SetStatus(code int) { s.status.Store(int32(code)) }

// CutBlobsAfter truncates whole-blob responses after n bytes. Zero disables it.
func (s *Server) CutBlobsAfter(n int64) { s.cutAfter.Store(n) }

// RejectRanges answers Range requests with a server error.
func (s *Server) RejectRanges(on bool) { s.rejectRange.Store(on) }

// Requests returns how many requests the server has received.
func (s *Server) Requests() int64 { return s.requests.Load() }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.requests.Add(1)
	if s.hang.Load() {
		<-r.Context().Done()
		return
	}
	if r.URL.Path == "/token" {
		s.serveToken(w)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/v2/") {
		http.NotFound(w, r)
		return
	}
	if code := int(s.status.Load()); code != 0 {
		w.WriteHeader(code)
		return
	}
	if !s.authorized(w, r) {
		return
	}
	if r.URL.Path == "/v2/" {
		w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
		w.WriteHeader(http.StatusOK)
		return
	}
	s.serveObject(w, r)
}

func (s *Server) authorized(w http.ResponseWriter, r *http.Request) bool {
	s.mu.Lock()
	token := s.token
	s.mu.Unlock()
	if token == "" || r.Header.Get("Authorization") == "Bearer "+token {
		return true
	}
	repo := repoFromPath(r.URL.Path)
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(
		`Bearer realm="http://%s/token",service="fake-registry",scope="repository:%s:pull"`, r.Host, repo))
	w.WriteHeader(http.StatusUnauthorized)
	return false
}

func (s *Server) serveToken(w http.ResponseWriter) {
	s.mu.Lock()
	token := s.token
	s.mu.Unlock()
	if token == "" {
		token = "anonymous"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "expires_in": 300})
}

// serveObject answers manifest and blob requests. The path has the form
// /v2/<repo>/manifests/<ref> or /v2/<repo>/blobs/<digest>.
func (s *Server) serveObject(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v2/")
	if i := strings.LastIndex(rest, "/manifests/"); i >= 0 {
		repo, ref := rest[:i], rest[i+len("/manifests/"):]
		s.mu.Lock()
		body, ok := s.manifests[repo+" "+ref]
		s.mu.Unlock()
		if !ok {
			notFound(w, "MANIFEST_UNKNOWN")
			return
		}
		w.Header().Set("Content-Type", s.mediaType)
		w.Header().Set("Docker-Content-Digest", Digest(body))
		http.ServeContent(w, r, "", zeroTime, bytes.NewReader(body))
		return
	}
	if i := strings.LastIndex(rest, "/blobs/"); i >= 0 {
		digest := rest[i+len("/blobs/"):]
		s.mu.Lock()
		body, ok := s.blobs[digest]
		s.mu.Unlock()
		if !ok {
			notFound(w, "BLOB_UNKNOWN")
			return
		}
		s.serveBlob(w, r, digest, body)
		return
	}
	notFound(w, "NAME_UNKNOWN")
}

func (s *Server) serveBlob(w http.ResponseWriter, r *http.Request, digest string, body []byte) {
	ranged := r.Header.Get("Range") != ""
	if ranged && s.rejectRange.Load() {
		http.Error(w, "range requests disabled", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Docker-Content-Digest", digest)
	if cut := s.cutAfter.Load(); cut > 0 && !ranged && r.Method == http.MethodGet {
		// Promise the full length, then stop early. The client sees a truncated body.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body[:min(int(cut), len(body))])
		return
	}
	http.ServeContent(w, r, "", zeroTime, bytes.NewReader(body))
}

// zeroTime tells http.ServeContent not to emit a Last-Modified header.
var zeroTime time.Time

func notFound(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = fmt.Fprintf(w, `{"errors":[{"code":%q,"message":"not found"}]}`, code)
}

// repoFromPath extracts the repository name from /v2/<repo>/<op>/<ref>.
func repoFromPath(p string) string {
	rest := strings.TrimPrefix(p, "/v2/")
	for _, marker := range []string{"/manifests/", "/blobs/"} {
		if i := strings.LastIndex(rest, marker); i >= 0 {
			return rest[:i]
		}
	}
	return rest
}
