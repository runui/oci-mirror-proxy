package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/config"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(t.Context(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func writeConfig(t *testing.T, cfg config.Config) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNoArgumentsIsUsageError(t *testing.T) {
	if code, _, errOut := run(t); code != 2 || !strings.Contains(errOut, "Usage") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	if code, _, errOut := run(t, "frobnicate"); code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

func TestVersionAndHelp(t *testing.T) {
	if code, out, _ := run(t, "version"); code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("version: code = %d, out = %q", code, out)
	}
	if code, out, _ := run(t, "help"); code != 0 || !strings.Contains(out, "dockerd apply") {
		t.Fatalf("help: code = %d", code)
	}
}

func TestInitRefusesToOverwriteWithoutForce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if code, _, errOut := run(t, "init", "--config", path); code != 0 {
		t.Fatalf("first init: code = %d, stderr = %q", code, errOut)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("init wrote an invalid config: %v", err)
	}
	if code, _, errOut := run(t, "init", "--config", path); code != 1 || !strings.Contains(errOut, "--force") {
		t.Fatalf("second init: code = %d, stderr = %q", code, errOut)
	}
	if code, _, _ := run(t, "init", "--config", path, "--force"); code != 0 {
		t.Fatalf("init --force: code = %d", code)
	}
}

func TestUpstreamAddRequiresIDAndURL(t *testing.T) {
	code, _, errOut := run(t, "upstream", "add", "--admin-socket", "/tmp/nope.sock")
	if code != 2 || !strings.Contains(errOut, "--id and --url are required") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

func TestUpstreamReportsMissingProxy(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "absent.sock")
	code, _, errOut := run(t, "status", "--admin-socket", sock)
	if code != 1 || !strings.Contains(errOut, "is the proxy running") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

func TestDockerdPlanContainerdWritesNothing(t *testing.T) {
	certs := filepath.Join(t.TempDir(), "certs.d")
	cfg := writeConfig(t, config.Default())

	code, out, errOut := run(t, "dockerd", "plan", "--config", cfg, "--store", "containerd", "--certs-dir", certs)
	if code != 0 {
		t.Fatalf("plan: code = %d, stderr = %q", code, errOut)
	}
	if !strings.Contains(out, "[host.\"http://127.0.0.1:24817\"]") {
		t.Fatalf("plan does not show the proxy host:\n%s", out)
	}
	if _, err := os.Stat(certs); err == nil {
		t.Fatal("plan must not create anything on disk")
	}
}

func TestDockerdPlanLegacyRejectsOtherRegistries(t *testing.T) {
	cfg := config.Default()
	cfg.Registries["ghcr.io"] = config.Registry{}
	cfg.ApplyDefaults()
	path := writeConfig(t, cfg)

	code, _, errOut := run(t, "dockerd", "plan", "--config", path, "--store", "legacy",
		"--daemon-json", filepath.Join(t.TempDir(), "daemon.json"))
	if code != 1 || !strings.Contains(errOut, "legacy image store can only mirror") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

func TestDockerdApplyAndRevertRoundTrip(t *testing.T) {
	root := t.TempDir()
	certs := filepath.Join(root, "certs.d")
	state := filepath.Join(root, "state")
	cfg := writeConfig(t, config.Default())
	args := []string{"--config", cfg, "--store", "containerd", "--certs-dir", certs, "--state-dir", state, "--no-reload"}

	if code, _, errOut := run(t, append([]string{"dockerd", "apply"}, args...)...); code != 0 {
		t.Fatalf("apply: code = %d, stderr = %q", code, errOut)
	}
	hosts := filepath.Join(certs, "docker.io", "hosts.toml")
	if _, err := os.Stat(hosts); err != nil {
		t.Fatalf("apply did not write hosts.toml: %v", err)
	}

	if code, _, errOut := run(t, append([]string{"dockerd", "revert"}, args...)...); code != 0 {
		t.Fatalf("revert: code = %d, stderr = %q", code, errOut)
	}
	if _, err := os.Stat(hosts); !os.IsNotExist(err) {
		t.Fatal("revert must remove the file apply created")
	}
	if _, err := os.Stat(filepath.Join(certs)); !os.IsNotExist(err) {
		t.Fatal("revert must remove the directories apply created")
	}
}

func TestDockerdAutoDetectNeedsRunningDaemon(t *testing.T) {
	cfg := writeConfig(t, config.Default())
	code, _, errOut := run(t, "dockerd", "plan", "--config", cfg,
		"--docker-socket", filepath.Join(t.TempDir(), "no-docker.sock"))
	if code != 1 || !strings.Contains(errOut, "pass --store explicitly") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

func TestDockerdRejectsUnknownAction(t *testing.T) {
	if code, _, _ := run(t, "dockerd", "explode"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestDefaultDockerPathsRootful(t *testing.T) {
	p, err := defaultDockerPaths(false)
	if err != nil {
		t.Fatal(err)
	}
	if p.CertsDir != "/etc/docker/certs.d" || p.DaemonJSON != "/etc/docker/daemon.json" {
		t.Fatalf("rootful paths = %+v", p)
	}
}

func TestDefaultDockerPathsRootlessUsesUserDirectories(t *testing.T) {
	home := t.TempDir()
	runtimeDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	p, err := defaultDockerPaths(true)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".config", "docker", "daemon.json"); p.DaemonJSON != want {
		t.Fatalf("daemon.json = %s, want %s", p.DaemonJSON, want)
	}
	if want := filepath.Join(runtimeDir, "docker.sock"); p.DockerSocket != want {
		t.Fatalf("socket = %s, want %s", p.DockerSocket, want)
	}
}

func TestRootlessWithoutRuntimeDirFails(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	if _, err := defaultDockerPaths(true); err == nil {
		t.Fatal("rootless setup must require XDG_RUNTIME_DIR")
	}
}
