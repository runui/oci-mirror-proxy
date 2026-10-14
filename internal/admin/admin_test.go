package admin

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/config"
)

// fakeController is an in-memory Controller for exercising the HTTP layer.
type fakeController struct {
	mu        sync.Mutex
	upstreams map[string][]config.Upstream // by alias
	reloads   int
}

func newFakeController() *fakeController {
	return &fakeController{upstreams: map[string][]config.Upstream{"docker.io": {}}}
}

func (f *fakeController) Status() Status {
	return Status{Listen: "127.0.0.1:24817"}
}

func (f *fakeController) ListUpstreams(alias string) ([]config.Upstream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ups, ok := f.upstreams[alias]
	if !ok {
		return nil, fmt.Errorf("%w: registry %q", ErrNotFound, alias)
	}
	return slices.Clone(ups), nil
}

func (f *fakeController) AddUpstream(alias string, u config.Upstream) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	ups, ok := f.upstreams[alias]
	if !ok {
		return fmt.Errorf("%w: registry %q", ErrNotFound, alias)
	}
	for _, existing := range ups {
		if existing.ID == u.ID {
			return fmt.Errorf("%w: upstream %q", ErrConflict, u.ID)
		}
	}
	f.upstreams[alias] = append(ups, u)
	return nil
}

func (f *fakeController) SetUpstreamDisabled(alias, id string, disabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.upstreams[alias] {
		if f.upstreams[alias][i].ID == id {
			f.upstreams[alias][i].Disabled = disabled
			return nil
		}
	}
	return fmt.Errorf("%w: upstream %q", ErrNotFound, id)
}

func (f *fakeController) RemoveUpstream(alias, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	ups := f.upstreams[alias]
	for i, u := range ups {
		if u.ID == id {
			f.upstreams[alias] = append(ups[:i], ups[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("%w: upstream %q", ErrNotFound, id)
}

func (f *fakeController) Reload() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reloads++
	return nil
}

// shortTempDir keeps socket paths below the platform limit.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "omp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// startServer serves ctl on a fresh socket and returns a client for it.
func startServer(t *testing.T, ctl Controller) *Client {
	t.Helper()
	sock := filepath.Join(shortTempDir(t), "admin.sock")
	ln, err := Listen(sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := NewHTTPServer(ctl)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return NewClient(sock)
}

func TestAdminRoundTrip(t *testing.T) {
	ctl := newFakeController()
	client := startServer(t, ctl)

	if st, err := client.Status(); err != nil || st.Listen != "127.0.0.1:24817" {
		t.Fatalf("Status = %+v, %v", st, err)
	}

	spec := UpstreamSpec{
		ID:        "mirror-a",
		URL:       "https://mirror-a.example.com",
		FirstByte: "2s",
		Overall:   "45s",
		Auth:      AuthSpec{Type: "basic", Username: "alice", Password: "env:MIRROR_A"},
	}
	if err := client.AddUpstream("docker.io", spec); err != nil {
		t.Fatalf("AddUpstream: %v", err)
	}

	list, err := client.ListUpstreams("docker.io")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListUpstreams = %+v, %v", list, err)
	}
	if list[0].FirstByte != "2s" || list[0].Overall != "45s" || list[0].Auth.Password != "env:MIRROR_A" {
		t.Fatalf("listed upstream lost data: %+v", list[0])
	}

	if err := client.SetUpstreamDisabled("docker.io", "mirror-a", true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := ctl.upstreams["docker.io"][0].Disabled; !got {
		t.Fatal("disable did not reach the controller")
	}

	if err := client.RemoveUpstream("docker.io", "mirror-a"); err != nil {
		t.Fatalf("RemoveUpstream: %v", err)
	}
	if err := client.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if ctl.reloads != 1 {
		t.Fatalf("reloads = %d, want 1", ctl.reloads)
	}
}

func TestAdminErrorMapping(t *testing.T) {
	client := startServer(t, newFakeController())

	err := client.AddUpstream("ghcr.io", UpstreamSpec{ID: "x", URL: "https://x.example.com"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown registry error = %v, want a not-found message", err)
	}

	spec := UpstreamSpec{ID: "dup", URL: "https://a.example.com"}
	if err := client.AddUpstream("docker.io", spec); err != nil {
		t.Fatal(err)
	}
	if err := client.AddUpstream("docker.io", spec); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate error = %v, want already exists", err)
	}

	bad := UpstreamSpec{ID: "slow", URL: "https://b.example.com", FirstByte: "soon"}
	if err := client.AddUpstream("docker.io", bad); err == nil || !strings.Contains(err.Error(), "first_byte") {
		t.Fatalf("bad duration error = %v, want a first_byte message", err)
	}
}

func TestAdminRejectsUnknownFields(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "admin.sock")
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewHTTPServer(newFakeController())
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	client := NewClient(sock)
	// A raw map with a misspelt key must be rejected, not silently dropped.
	err = client.do(http.MethodPost, upstreamsPath("docker.io"), map[string]string{"id": "x", "url": "https://x.example.com", "urll": "typo"}, nil)
	if err == nil {
		t.Fatal("unknown field must be rejected")
	}
}

func TestListenSetsPrivatePermissions(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "private", "admin.sock")
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	info, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket permissions = %o, want 0600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(sock))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("directory permissions = %o, want 0700", perm)
	}
}

func TestListenRefusesLiveSocketAndReplacesStaleOne(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "admin.sock")
	first, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(sock); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Listen error = %v, want ErrAlreadyRunning", err)
	}
	// Closing the listener removes the socket file, so a crash-like leftover is
	// simulated with a plain file instead.
	_ = first.Close()
	if err := os.WriteFile(sock, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(sock)
	if err != nil {
		t.Fatalf("stale file must be replaced: %v", err)
	}
	ln.Close()
}

func TestListenRejectsLongPaths(t *testing.T) {
	long := "/tmp/" + strings.Repeat("a", maxSocketPath) + "/admin.sock"
	if _, err := Listen(long); err == nil {
		t.Fatal("overlong socket path must be rejected before binding")
	}
}
