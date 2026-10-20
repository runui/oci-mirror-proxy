package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/admin"
	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/ociapi"
	"github.com/runui/oci-mirror-proxy/internal/route"
	"github.com/runui/oci-mirror-proxy/internal/scheduler"
	"github.com/runui/oci-mirror-proxy/internal/testutil/fakeregistry"
)

var manifest = []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)

// baseConfig returns a valid configuration whose docker.io registry has the given upstreams.
func baseConfig(upstreams ...config.Upstream) config.Config {
	c := config.Default()
	reg := c.Registries["docker.io"]
	reg.Upstreams = upstreams
	c.Registries["docker.io"] = reg
	return c
}

// upstreamFor returns an anonymous upstream with short timeouts suited to tests.
func upstreamFor(id, url string) config.Upstream {
	return config.Upstream{
		ID:       id,
		URL:      url,
		Timeouts: config.Timeouts{FirstByte: 300 * time.Millisecond, Overall: 5 * time.Second},
		Auth:     config.Auth{Type: config.AuthNone},
	}
}

// newService writes cfg to a temporary file and starts a Service from it.
func newService(t *testing.T, cfg config.Config) (*Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	svc, err := New(t.Context(), Options{ConfigPath: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)
	return svc, path
}

// proxyFor serves svc over HTTP and returns the base URL.
func proxyFor(t *testing.T, svc *Service) string {
	t.Helper()
	srv := httptest.NewServer(svc.Handler())
	t.Cleanup(srv.Close)
	return srv.URL
}

func get(t *testing.T, base, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp, body
}

func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &e); err != nil || len(e.Errors) == 0 {
		t.Fatalf("not a registry error body: %q", body)
	}
	return e.Errors[0].Code
}

const manifestPath = "/v2/library/nginx/manifests/latest"

func TestDeliveryPolicyReloadKeepsDispatcherSnapshot(t *testing.T) {
	var attempts atomic.Int32
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download" {
			w.Header().Set("Content-Length", "7")
			io.WriteString(w, "payload")
			return
		}
		attempts.Add(1)
		w.Header().Set("Location", "/download")
		w.WriteHeader(307)
	}))
	defer registry.Close()
	// Distinct path prefixes are distinct upstream URLs; duplicate URLs are
	// intentionally merged into one candidate by configuration loading.
	cfg := baseConfig(upstreamFor("primary", registry.URL), upstreamFor("alternate", registry.URL+"/alternate"))
	svc, path := newService(t, cfg)
	old, _ := svc.table.Lookup("docker.io")
	reg := cfg.Registries["docker.io"]
	reg.Adaptive.Enabled = true
	cfg.Registries["docker.io"] = reg
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	current, _ := svc.table.Lookup("docker.io")
	check := func(d route.Dispatcher, wantAttempts int32) {
		t.Helper()
		attempts.Store(0)
		req := scheduler.Request{Kind: ociapi.KindBlob, Method: http.MethodGet,
			Path: "/v2/library/test/blobs/sha256:" + strings.Repeat("a", 64), AllowBlobRedirect: true}
		resp, err := d.Dispatch(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(body) != "payload" || attempts.Load() != wantAttempts {
			t.Fatalf("body=%q err=%v attempts=%d, want %d", body, err, attempts.Load(), wantAttempts)
		}
	}
	// The captured pre-reload dispatcher still permits delegation. Its HTTP
	// target fails the safety check, so it reissues once for relay. The new
	// adaptive dispatcher owns the stream from the start and sends only once.
	check(old, 2)
	check(current, 1)
}

func TestConfiguredLogLevelReloadAndRouting(t *testing.T) {
	miss := httptest.NewServer(http.NotFoundHandler())
	defer miss.Close()
	healthy := fakeregistry.New(t)
	healthy.PutManifest("library/nginx", "latest", manifest)
	cfg := baseConfig(upstreamFor("miss", miss.URL), upstreamFor("healthy", healthy.URL))
	cfg.LogLevel = "debug"
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	var level slog.LevelVar
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: &level}))
	svc, err := New(t.Context(), Options{ConfigPath: path, Logger: logger, LogLevel: &level})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	base := proxyFor(t, svc)
	resp, _ := get(t, base, manifestPath)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, want := range []string{"request_id=", `msg="route selected"`, "strategy=sequential", `msg="upstream attempt failed"`, "status=404", `msg="upstream hit"`, `msg="upstream selected"`, "upstream=healthy", "via=" + healthy.URL} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("missing %q in logs:\n%s", want, logs.String())
		}
	}
	cfg.LogLevel = "error"
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(); err != nil || level.Level() != slog.LevelError {
		t.Fatalf("reload level = %v, err = %v", level.Level(), err)
	}
	logs.Reset()
	get(t, base, manifestPath)
	if logs.Len() != 0 {
		t.Fatalf("debug logs enabled at error level: %s", logs.String())
	}
	if err := os.WriteFile(path, []byte("version: 1\nlog_level: invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(); err == nil || level.Level() != slog.LevelError {
		t.Fatalf("invalid reload changed level: %v, %v", level.Level(), err)
	}
}

func TestFailoverSkipsSilentUpstream(t *testing.T) {
	silent := fakeregistry.New(t)
	silent.SetHang(true)
	healthy := fakeregistry.New(t)
	digest := healthy.PutManifest("library/nginx", "latest", manifest)

	svc, _ := newService(t, baseConfig(upstreamFor("silent", silent.URL), upstreamFor("healthy", healthy.URL)))
	base := proxyFor(t, svc)

	start := time.Now()
	resp, body := get(t, base, manifestPath)
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, manifest) {
		t.Fatalf("status = %d, body = %q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Docker-Content-Digest"); got != digest {
		t.Fatalf("digest header = %q, want %q", got, digest)
	}
	// The silent upstream costs one first-byte timeout (300ms), not an indefinite wait.
	if elapsed > 3*time.Second {
		t.Fatalf("failover took %v", elapsed)
	}
}

func TestBreakerStopsSendingToFailingUpstream(t *testing.T) {
	failing := fakeregistry.New(t)
	failing.SetStatus(http.StatusInternalServerError)
	healthy := fakeregistry.New(t)
	healthy.PutManifest("library/nginx", "latest", manifest)

	cfg := baseConfig(upstreamFor("failing", failing.URL), upstreamFor("healthy", healthy.URL))
	reg := cfg.Registries["docker.io"]
	reg.Breaker = config.Breaker{Threshold: 2, Cooldown: time.Hour}
	cfg.Registries["docker.io"] = reg
	svc, _ := newService(t, cfg)
	base := proxyFor(t, svc)

	for range 2 {
		if resp, _ := get(t, base, manifestPath); resp.StatusCode != http.StatusOK {
			t.Fatalf("request status = %d, want 200 via the healthy upstream", resp.StatusCode)
		}
	}
	before := failing.Requests()
	for range 3 {
		get(t, base, manifestPath)
	}
	if after := failing.Requests(); after != before {
		t.Fatalf("open circuit still sent %d requests to the failing upstream", after-before)
	}
}

func TestRacingPrefersFastUpstream(t *testing.T) {
	silent := fakeregistry.New(t)
	silent.SetHang(true)
	healthy := fakeregistry.New(t)
	healthy.PutManifest("library/nginx", "latest", manifest)

	cfg := baseConfig(upstreamFor("silent", silent.URL), upstreamFor("healthy", healthy.URL))
	cfg.Registries["docker.io"] = withRace(cfg.Registries["docker.io"], 50*time.Millisecond)
	for i, u := range cfg.Registries["docker.io"].Upstreams {
		u.Timeouts.FirstByte = 10 * time.Second // racing, not the timeout, must rescue the request
		cfg.Registries["docker.io"].Upstreams[i] = u
	}
	svc, _ := newService(t, cfg)
	base := proxyFor(t, svc)

	start := time.Now()
	resp, body := get(t, base, manifestPath)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, manifest) {
		t.Fatalf("status = %d, body = %q", resp.StatusCode, body)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("racing took %v; the hedge should have answered after about 50ms", elapsed)
	}
}

func withRace(reg config.Registry, delay time.Duration) config.Registry {
	reg.Race = config.Race{Enabled: true, Delay: delay, MaxParallel: 2}
	return reg
}

func TestBlobResumesOnAnotherUpstream(t *testing.T) {
	blob := bytes.Repeat([]byte("0123456789abcdef"), 4096) // 64 KiB
	digest := fakeregistry.Digest(blob)

	flaky := fakeregistry.New(t)
	flaky.PutBlob(blob)
	flaky.CutBlobsAfter(10_000) // drop the connection mid-layer
	flaky.RejectRanges(true)    // so the resume cannot succeed on the same upstream

	stable := fakeregistry.New(t)
	stable.PutBlob(blob)

	svc, _ := newService(t, baseConfig(upstreamFor("flaky", flaky.URL), upstreamFor("stable", stable.URL)))
	base := proxyFor(t, svc)

	resp, body := get(t, base, "/v2/library/nginx/blobs/"+digest)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !bytes.Equal(body, blob) {
		t.Fatalf("resumed blob has %d bytes, want %d (content must match exactly)", len(body), len(blob))
	}
}

func TestTokenAuthenticatedUpstream(t *testing.T) {
	secured := fakeregistry.New(t)
	secured.RequireToken("t0ken")
	secured.PutManifest("library/nginx", "latest", manifest)

	cfg := baseConfig(upstreamFor("secured", secured.URL))
	reg := cfg.Registries["docker.io"]
	reg.Upstreams[0].Auth = config.Auth{Type: config.AuthToken}
	cfg.Registries["docker.io"] = reg
	svc, _ := newService(t, cfg)
	base := proxyFor(t, svc)

	resp, body := get(t, base, manifestPath)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, manifest) {
		t.Fatalf("token flow failed: status = %d, body = %q", resp.StatusCode, body)
	}
}

func TestMissingEverywhereReturnsNotFound(t *testing.T) {
	empty := fakeregistry.New(t)
	svc, _ := newService(t, baseConfig(upstreamFor("empty", empty.URL)))
	base := proxyFor(t, svc)

	resp, body := get(t, base, manifestPath)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 so dockerd falls back to the origin", resp.StatusCode)
	}
	if code := errorCode(t, body); code != "MANIFEST_UNKNOWN" {
		t.Fatalf("code = %s, want MANIFEST_UNKNOWN", code)
	}
}

func TestNoUpstreamsReturnsBadGateway(t *testing.T) {
	svc, _ := newService(t, baseConfig())
	base := proxyFor(t, svc)

	resp, body := get(t, base, manifestPath)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 with no upstreams", resp.StatusCode)
	}
	if code := errorCode(t, body); code != "UNAVAILABLE" {
		t.Fatalf("code = %s, want UNAVAILABLE", code)
	}
}

func TestAdminChangesApplyWithoutRestart(t *testing.T) {
	hub := fakeregistry.New(t)
	hub.PutManifest("library/nginx", "latest", manifest)

	svc, path := newService(t, baseConfig())
	base := proxyFor(t, svc)

	if resp, _ := get(t, base, manifestPath); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("before add: status = %d", resp.StatusCode)
	}
	if err := svc.AddUpstream("docker.io", upstreamFor("hub", hub.URL)); err != nil {
		t.Fatalf("AddUpstream: %v", err)
	}
	if resp, _ := get(t, base, manifestPath); resp.StatusCode != http.StatusOK {
		t.Fatalf("after add: status = %d, want 200 without a restart", resp.StatusCode)
	}

	// The change must be persisted, not only held in memory.
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Registries["docker.io"].Upstreams) != 1 {
		t.Fatalf("persisted upstreams = %+v", saved.Registries["docker.io"].Upstreams)
	}

	if err := svc.SetUpstreamDisabled("docker.io", "hub", true); err != nil {
		t.Fatal(err)
	}
	if resp, _ := get(t, base, manifestPath); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("disabled upstream still serving: status = %d", resp.StatusCode)
	}
	if err := svc.SetUpstreamDisabled("docker.io", "hub", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveUpstream("docker.io", "hub"); err != nil {
		t.Fatal(err)
	}
	if resp, _ := get(t, base, manifestPath); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("removed upstream still serving: status = %d", resp.StatusCode)
	}
}

func TestAdminErrorsAreTyped(t *testing.T) {
	svc, _ := newService(t, baseConfig(upstreamFor("hub", "https://hub.example.com")))

	if err := svc.AddUpstream("ghcr.io", upstreamFor("x", "https://x.example.com")); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("unknown registry = %v, want ErrNotFound", err)
	}
	if err := svc.AddUpstream("docker.io", upstreamFor("hub", "https://other.example.com")); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("duplicate id = %v, want ErrConflict", err)
	}
	if err := svc.AddUpstream("docker.io", upstreamFor("bad", "ftp://nope.example.com")); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("invalid URL = %v, want ErrInvalid", err)
	}
	if err := svc.RemoveUpstream("docker.io", "missing"); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("missing upstream = %v, want ErrNotFound", err)
	}
}

func TestFailedMutationLeavesNoTrace(t *testing.T) {
	svc, path := newService(t, baseConfig(upstreamFor("hub", "https://hub.example.com")))
	before, _ := os.ReadFile(path)

	_ = svc.AddUpstream("docker.io", upstreamFor("bad", "ftp://nope.example.com"))

	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("a rejected change rewrote the configuration file")
	}
	ups, err := svc.ListUpstreams("docker.io")
	if err != nil || len(ups) != 1 {
		t.Fatalf("running upstreams changed: %+v, %v", ups, err)
	}
}

func TestReloadPicksUpHandEditsAndRejectsBrokenFiles(t *testing.T) {
	first := fakeregistry.New(t)
	first.PutManifest("library/nginx", "latest", []byte(`{"from":"first"}`))
	second := fakeregistry.New(t)
	second.PutManifest("library/nginx", "latest", []byte(`{"from":"second"}`))

	svc, path := newService(t, baseConfig(upstreamFor("first", first.URL)))
	base := proxyFor(t, svc)

	if err := config.Save(path, baseConfig(upstreamFor("second", second.URL))); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, body := get(t, base, manifestPath); !strings.Contains(string(body), "second") {
		t.Fatalf("reload did not apply the hand edit, body = %s", body)
	}

	if err := os.WriteFile(path, []byte("version: 1\nlisten: [broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("broken file reload = %v, want ErrInvalid", err)
	}
	if _, body := get(t, base, manifestPath); !strings.Contains(string(body), "second") {
		t.Fatalf("a broken file changed the running configuration: %s", body)
	}
}

func TestUnconfiguredRegistryIsNotFound(t *testing.T) {
	svc, _ := newService(t, baseConfig())
	base := proxyFor(t, svc)

	resp, body := get(t, base, "/ghcr.io/v2/owner/repo/manifests/latest")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if code := errorCode(t, body); code != "NAME_UNKNOWN" {
		t.Fatalf("code = %s, want NAME_UNKNOWN", code)
	}
}
