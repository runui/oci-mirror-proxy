package app

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/ociapi"
	"github.com/runui/oci-mirror-proxy/internal/scheduler"
)

func TestReloadPinsSlowBlobAndItsResumeCandidates(t *testing.T) {
	payload := bytes.Repeat([]byte("0123456789abcdef"), 8192)
	first := len(payload) / 2
	proceed := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(proceed) }) }
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		w.Write(payload[:first])
		w.(http.Flusher).Flush()
		select {
		case <-proceed:
		case <-r.Context().Done():
		}
		// Returning before Content-Length simulates an upstream failure after
		// reload. The old request must still find its OLD alternate for resume.
	}))
	defer func() { release(); primary.Close() }()
	var resumes atomic.Int32
	alternate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != fmt.Sprintf("bytes=%d-", first) {
			http.NotFound(w, r)
			return
		}
		resumes.Add(1)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", first, len(payload)-1, len(payload)))
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)-first))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(payload[first:])
	}))
	defer alternate.Close()
	newSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "new-generation") }))
	defer newSource.Close()
	svc, path := newService(t, baseConfig(upstreamFor("", primary.URL), upstreamFor("", alternate.URL)))
	base := proxyFor(t, svc)
	blobPath := "/v2/library/test/blobs/sha256:" + strings.Repeat("a", 64)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(base + blobPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	start := make([]byte, first)
	if _, err := io.ReadFull(resp.Body, start); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := config.Save(path, baseConfig(upstreamFor("", newSource.URL))); err != nil {
			t.Fatal(err)
		}
		if err := svc.Reload(); err != nil {
			t.Fatal(err)
		}
	}
	if resumes.Load() != 0 {
		t.Fatal("reload itself triggered a resume")
	}
	next, err := client.Get(base + blobPath)
	if err != nil {
		t.Fatal(err)
	}
	newBody, err := io.ReadAll(next.Body)
	next.Body.Close()
	if err != nil || string(newBody) != "new-generation" {
		t.Fatalf("new request: %q %v", newBody, err)
	}
	release()
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reload interrupted blob/resume: %v", err)
	}
	if !bytes.Equal(append(start, rest...), payload) || resumes.Load() != 1 {
		t.Fatalf("corrupted blob or missing resume: %d", resumes.Load())
	}
}

func TestFailedGenerationBuildKeepsEveryRegistryAndState(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "old") }))
	defer first.Close()
	svc, path := newService(t, baseConfig(upstreamFor("", first.URL)))
	old := svc.pools["docker.io"].Candidates()[0]
	c := baseConfig(upstreamFor("", first.URL))
	c.Registries["ghcr.io"] = config.Registry{Upstreams: []config.Upstream{{URL: "https://other.example", TLS: config.TLS{CAFile: filepath.Join(t.TempDir(), "missing.crt")}}}}
	if err := config.Save(path, c); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(); err == nil {
		t.Fatal("accepted invalid runtime generation")
	}
	if _, ok := svc.table.Lookup("ghcr.io"); ok {
		t.Fatal("partially published registry")
	}
	if svc.pools["docker.io"].Candidates()[0] != old {
		t.Fatal("failed reload replaced old endpoint")
	}
	if err := config.Save(path, baseConfig(upstreamFor("", first.URL))); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	if svc.pools["docker.io"].Candidates()[0] != old {
		t.Fatal("unchanged endpoint not reused")
	}
}

func TestMutationReadsNewDropInAndPreservesRemoteHeader(t *testing.T) {
	svc, path := newService(t, baseConfig())
	if err := os.MkdirAll(config.DropInDir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	fragment := filepath.Join(config.DropInDir(path), "source.yaml")
	remote := []byte("# Managed by oci-mirror-proxy remote.\n# remote: {\"version\":1}\n\nregistries:\n  docker.io:\n    upstreams:\n      - url: https://mirror.example/hub\n")
	if err := os.WriteFile(fragment, remote, 0o600); err != nil {
		t.Fatal(err)
	}
	// No Reload here: mutation must pick up external CLI writes on disk rather
	// than overwriting them with the previous in-memory applied configuration.
	if err := svc.AddUpstream("docker.io", upstreamFor("", "https://manual.example")); err != nil {
		t.Fatal(err)
	}
	ups, err := svc.ListUpstreams("docker.io")
	if err != nil || len(ups) != 2 {
		t.Fatalf("%+v %v", ups, err)
	}
	if err := svc.SetUpstreamDisabled("docker.io", "https://mirror.example/hub", true); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := os.ReadFile(fragment)
	if !bytes.Equal(unchanged, remote) {
		t.Fatal("disable modified source")
	}
	if err := svc.RemoveUpstream("docker.io", "https://mirror.example/hub"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(fragment)
	if !bytes.Contains(after, []byte("# remote:")) {
		t.Fatal("generic edit lost remote metadata comment")
	}
}

func TestAcquiredGenerationSurvivesRetirementBeforeDispatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "old") }))
	defer server.Close()
	svc, path := newService(t, baseConfig(upstreamFor("", server.URL)))
	d, release, ok := svc.table.Acquire("docker.io")
	if !ok {
		t.Fatal("missing route")
	}
	defer release()
	if err := config.Save(path, baseConfig()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	r, err := d.Dispatch(t.Context(), scheduler.Request{Kind: ociapi.KindManifest, Method: "GET", Repo: "library/test", Path: "/v2/library/test/manifests/latest"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil || string(body) != "old" {
		t.Fatalf("%q %v", body, err)
	}
}
