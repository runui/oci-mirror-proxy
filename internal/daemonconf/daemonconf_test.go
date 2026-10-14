package daemonconf

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const proxyURL = "http://127.0.0.1:24817"

func dockerHubTarget() Target {
	return Target{ProxyURL: proxyURL, Routes: []Route{{Alias: "docker.io", Origin: "https://registry-1.docker.io"}}}
}

func TestContainerdRenderPerRegistry(t *testing.T) {
	certs := t.TempDir()
	target := Target{ProxyURL: proxyURL, Routes: []Route{
		{Alias: "docker.io", Origin: "https://registry-1.docker.io"},
		{Alias: "ghcr.io", Origin: "https://ghcr.io"},
	}}
	plan, err := ContainerdRenderer{CertsDir: certs}.Render(target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.NeedsReload {
		t.Error("containerd reads hosts.toml on every resolution; no reload should be requested")
	}
	if len(plan.Files) != 2 {
		t.Fatalf("files = %d, want one hosts.toml per registry", len(plan.Files))
	}

	hub := plan.Files[0]
	if hub.Path != filepath.Join(certs, "docker.io", "hosts.toml") {
		t.Fatalf("docker.io path = %s", hub.Path)
	}
	for _, want := range []string{
		`server = "https://registry-1.docker.io"`,
		`[host."http://127.0.0.1:24817"]`,
		`capabilities = ["pull", "resolve", "referrers"]`,
	} {
		if !strings.Contains(string(hub.Content), want) {
			t.Errorf("docker.io hosts.toml missing %q\n%s", want, hub.Content)
		}
	}

	ghcr := plan.Files[1]
	if !strings.Contains(string(ghcr.Content), `[host."http://127.0.0.1:24817/ghcr.io"]`) {
		t.Errorf("ghcr.io must be routed under its path prefix\n%s", ghcr.Content)
	}
}

func TestContainerdRenderRejectsEmptyTarget(t *testing.T) {
	if _, err := (ContainerdRenderer{CertsDir: t.TempDir()}).Render(Target{ProxyURL: proxyURL}, nil); !errors.Is(err, ErrNoRoutes) {
		t.Fatalf("error = %v, want ErrNoRoutes", err)
	}
}

func TestTomlStringEscapes(t *testing.T) {
	tests := map[string]string{
		`plain`:       `"plain"`,
		`a"b`:         `"a\"b"`,
		`a\b`:         `"a\\b"`,
		"tab\there":   `"tab\u0009here"`,
		"http://x:1/": `"http://x:1/"`,
	}
	for in, want := range tests {
		if got := tomlString(in); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", in, got, want)
		}
	}
}

func readFrom(path string) ReadFunc {
	return func(p string) ([]byte, error) {
		if p != path {
			return nil, fs.ErrNotExist
		}
		return os.ReadFile(p)
	}
}

func noFile(string) ([]byte, error) { return nil, fs.ErrNotExist }

func TestLegacyRenderCreatesMirrorList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	plan, err := LegacyRenderer{DaemonJSON: path}.Render(dockerHubTarget(), noFile)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.NeedsReload {
		t.Error("legacy store needs a reload to pick up registry-mirrors")
	}
	var cfg struct {
		Mirrors []string `json:"registry-mirrors"`
	}
	if err := json.Unmarshal(plan.Files[0].Content, &cfg); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Mirrors, []string{proxyURL}) {
		t.Fatalf("registry-mirrors = %v", cfg.Mirrors)
	}
}

func TestLegacyRenderPreservesOtherKeysAndMirrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	existing := `{"log-level":"info","registry-mirrors":["https://other.example.com",` + `"` + proxyURL + `"],"insecure-registries":["x:5000"]}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := LegacyRenderer{DaemonJSON: path}.Render(dockerHubTarget(), readFrom(path))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(plan.Files[0].Content, &cfg); err != nil {
		t.Fatal(err)
	}
	if string(cfg["log-level"]) != `"info"` || len(cfg["insecure-registries"]) == 0 {
		t.Fatalf("unrelated keys were lost: %s", plan.Files[0].Content)
	}
	var mirrors []string
	if err := json.Unmarshal(cfg["registry-mirrors"], &mirrors); err != nil {
		t.Fatal(err)
	}
	want := []string{proxyURL, "https://other.example.com"}
	if !slices.Equal(mirrors, want) {
		t.Fatalf("mirrors = %v, want the proxy first and no duplicate: %v", mirrors, want)
	}
}

func TestLegacyRenderRejectsOtherRegistries(t *testing.T) {
	target := Target{ProxyURL: proxyURL, Routes: []Route{{Alias: "ghcr.io", Origin: "https://ghcr.io"}}}
	if _, err := (LegacyRenderer{DaemonJSON: "x"}).Render(target, noFile); err == nil {
		t.Fatal("legacy store must refuse non-docker.io registries")
	}
}

func TestLegacyRenderRefusesInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (LegacyRenderer{DaemonJSON: path}).Render(dockerHubTarget(), readFrom(path)); err == nil {
		t.Fatal("invalid daemon.json must not be rewritten")
	}
}

func TestApplyAndRevertRestoresOriginalState(t *testing.T) {
	root := t.TempDir()
	certs := filepath.Join(root, "certs.d")
	state := filepath.Join(root, "state", "state.json")
	applier := Applier{StatePath: state, BackupDir: filepath.Join(root, "backups"), Now: time.Now}

	plan, err := ContainerdRenderer{CertsDir: certs}.Render(dockerHubTarget(), nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := applier.Apply(plan, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(st.Dirs) == 0 {
		t.Fatal("apply must record the directories it created")
	}
	written, err := os.ReadFile(plan.Files[0].Path)
	if err != nil || string(written) != string(plan.Files[0].Content) {
		t.Fatalf("file not written correctly: %v", err)
	}

	if _, err := applier.Revert(); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if _, err := os.Stat(plan.Files[0].Path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("revert must remove the file that apply created")
	}
	if _, err := os.Stat(certs); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("revert must remove directories apply created")
	}
	if _, err := os.Stat(state); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("revert must delete the state file")
	}
	if _, err := applier.Revert(); !errors.Is(err, ErrNothingToRevert) {
		t.Fatalf("second revert = %v, want ErrNothingToRevert", err)
	}
}

func TestApplyRefusesUnmanagedFileWithoutForce(t *testing.T) {
	root := t.TempDir()
	certs := filepath.Join(root, "certs.d")
	target := filepath.Join(certs, "docker.io", "hosts.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	userContent := []byte("server = \"https://user.example.com\"\n")
	if err := os.WriteFile(target, userContent, 0o644); err != nil {
		t.Fatal(err)
	}
	applier := Applier{StatePath: filepath.Join(root, "state.json"), BackupDir: filepath.Join(root, "backups")}
	plan, _ := ContainerdRenderer{CertsDir: certs}.Render(dockerHubTarget(), nil)

	if _, err := applier.Apply(plan, false); !errors.Is(err, ErrUnmanaged) {
		t.Fatalf("Apply without force = %v, want ErrUnmanaged", err)
	}
	if got, _ := os.ReadFile(target); string(got) != string(userContent) {
		t.Fatal("refused apply must not touch the user's file")
	}

	if _, err := applier.Apply(plan, true); err != nil {
		t.Fatalf("Apply with force: %v", err)
	}
	if _, err := applier.Revert(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != string(userContent) {
		t.Fatalf("revert did not restore the user's original file: %q", got)
	}
}

func TestReapplyIsIdempotent(t *testing.T) {
	root := t.TempDir()
	applier := Applier{StatePath: filepath.Join(root, "state.json"), BackupDir: filepath.Join(root, "backups")}
	plan, _ := ContainerdRenderer{CertsDir: filepath.Join(root, "certs.d")}.Render(dockerHubTarget(), nil)

	first, err := applier.Apply(plan, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := applier.Apply(plan, false)
	if err != nil {
		t.Fatalf("second apply must not require --force: %v", err)
	}
	if len(second.Files) != len(first.Files) {
		t.Fatalf("file records changed on re-apply: %d -> %d", len(first.Files), len(second.Files))
	}
}

func TestApplyRestoresFilesDroppedFromPlan(t *testing.T) {
	root := t.TempDir()
	applier := Applier{StatePath: filepath.Join(root, "state.json"), BackupDir: filepath.Join(root, "backups")}
	certs := filepath.Join(root, "certs.d")

	two := Target{ProxyURL: proxyURL, Routes: []Route{
		{Alias: "docker.io", Origin: "https://registry-1.docker.io"},
		{Alias: "ghcr.io", Origin: "https://ghcr.io"},
	}}
	planTwo, _ := ContainerdRenderer{CertsDir: certs}.Render(two, nil)
	if _, err := applier.Apply(planTwo, false); err != nil {
		t.Fatal(err)
	}

	planOne, _ := ContainerdRenderer{CertsDir: certs}.Render(dockerHubTarget(), nil)
	if _, err := applier.Apply(planOne, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(planTwo.Files[1].Path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a file the new plan no longer needs must be removed")
	}
}

func TestDetectStore(t *testing.T) {
	containerd := Info{DriverStatus: [][2]string{{"driver-type", "io.containerd.snapshotter.v1"}}}
	legacy := Info{DriverStatus: [][2]string{{"Backing Filesystem", "extfs"}, {"Native Overlay Diff", "true"}}}
	if DetectStore(containerd) != StoreContainerd {
		t.Error("containerd snapshotter must be detected")
	}
	if DetectStore(legacy) != StoreLegacy {
		t.Error("graphdriver info must be detected as legacy")
	}
}

func TestFetchInfoOverSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "omd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/info" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"DriverStatus":[["driver-type","io.containerd.snapshotter.v1"]],"RegistryConfig":{"Mirrors":["http://127.0.0.1:24817"]}}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	info, err := FetchInfo(t.Context(), sock)
	if err != nil {
		t.Fatalf("FetchInfo: %v", err)
	}
	if DetectStore(info) != StoreContainerd {
		t.Fatal("store not detected from /info")
	}
	if info.RegistryConfig == nil || !slices.Contains(info.RegistryConfig.Mirrors, proxyURL) {
		t.Fatalf("mirrors not decoded: %+v", info.RegistryConfig)
	}
}

func TestDiagnoseReportsMissingMirror(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	info := Info{
		DriverStatus:   [][2]string{{"Backing Filesystem", "extfs"}},
		RegistryConfig: &RegistryConfig{Mirrors: []string{"https://someone-else.example.com"}},
	}
	checks := Diagnose(t.Context(), DiagnoseInput{ProxyURL: srv.URL, Info: info})

	var mirrorOK, proxyOK bool
	for _, c := range checks {
		switch c.Name {
		case "dockerd registry-mirrors":
			mirrorOK = c.OK
		case "proxy":
			proxyOK = c.OK
		}
	}
	if mirrorOK {
		t.Error("a daemon without the proxy in registry-mirrors must fail the check")
	}
	if !proxyOK {
		t.Error("the reachable proxy must pass its check")
	}
}
