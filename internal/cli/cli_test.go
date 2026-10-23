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
	if code != 2 || !strings.Contains(errOut, "--url is required") {
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

func TestDockerdPlanLegacyWarnsAboutOtherRegistries(t *testing.T) {
	cfg := config.Default()
	cfg.Registries["ghcr.io"] = config.Registry{}
	cfg.ApplyDefaults()
	path := writeConfig(t, cfg)

	code, out, errOut := run(t, "dockerd", "plan", "--config", path, "--store", "legacy",
		"--daemon-json", filepath.Join(t.TempDir(), "daemon.json"))
	if code != 0 || !strings.Contains(errOut, "warning:") || !strings.Contains(errOut, "skipping ghcr.io") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	if !strings.Contains(out, "registry-mirrors") || !strings.Contains(out, cfg.ProxyURL()) {
		t.Fatalf("missing Docker Hub mirror: %s", out)
	}
}

func TestDockerdApplyLegacyWarnsAndStillConfiguresDockerHub(t *testing.T) {
	root := t.TempDir()
	daemon := filepath.Join(root, "daemon.json")
	cfg := config.Default()
	cfg.Registries["gcr.io"] = config.Registry{}
	cfg.ApplyDefaults()
	path := writeConfig(t, cfg)
	args := []string{"--config", path, "--store", "legacy", "--daemon-json", daemon,
		"--state-dir", filepath.Join(root, "state"), "--no-reload"}
	code, out, errOut := run(t, append([]string{"dockerd", "apply"}, args...)...)
	if code != 0 || !strings.Contains(errOut, "warning:") || !strings.Contains(errOut, "skipping gcr.io") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	data, err := os.ReadFile(daemon)
	if err != nil || !strings.Contains(string(data), cfg.ProxyURL()) {
		t.Fatalf("Docker Hub mirror not applied: %s, %v", data, err)
	}
	if code, _, errOut := run(t, append([]string{"dockerd", "revert"}, args...)...); code != 0 {
		t.Fatalf("revert: code = %d, stderr = %q", code, errOut)
	}
}

func TestDockerdLegacyUnsupportedOnlyIsNoOp(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "existing"}[existing], func(t *testing.T) {
			root := t.TempDir()
			daemon := filepath.Join(root, "daemon.json")
			state := filepath.Join(root, "state", "dockerd-state.json")
			original := []byte("unchanged")
			if existing {
				if err := os.MkdirAll(filepath.Dir(state), 0o755); err != nil {
					t.Fatal(err)
				}
				for _, file := range []string{daemon, state} {
					if err := os.WriteFile(file, original, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			cfg := config.Default()
			delete(cfg.Registries, "docker.io")
			cfg.Registries["gcr.io"] = config.Registry{}
			cfg.ApplyDefaults()
			path := writeConfig(t, cfg)
			for _, action := range []string{"plan", "apply"} {
				args := []string{"dockerd", action, "--config", path, "--store", "legacy", "--daemon-json", daemon}
				if action == "apply" {
					args = append(args, "--state-dir", filepath.Dir(state))
				}
				code, out, errOut := run(t, args...)
				if code != 0 || !strings.Contains(errOut, "warning:") || !strings.Contains(errOut, "skipping gcr.io") || !strings.Contains(out, "nothing to apply") {
					t.Fatalf("%s: code = %d, stdout = %q, stderr = %q", action, code, out, errOut)
				}
				for _, file := range []string{daemon, state} {
					data, err := os.ReadFile(file)
					if existing {
						if err != nil || !bytes.Equal(data, original) {
							t.Fatalf("%s changed: %q, %v", file, data, err)
						}
					} else if !os.IsNotExist(err) {
						t.Fatalf("%s must not be created: %v", file, err)
					}
				}
			}
		})
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
	if data, err := os.ReadFile(hosts); err != nil || strings.Contains(string(data), config.Default().ProxyURL()) {
		t.Fatalf("revert must remove only the proxy host: %s %v", data, err)
	}
	if _, err := os.Stat(certs); err != nil {
		t.Fatal("revert must preserve configuration directories")
	}
}

func TestDockerdRevertUsesDefaultConfigWithoutNewArguments(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("APPDATA", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Save(path, config.Default()); err != nil {
		t.Fatal(err)
	}
	certs := filepath.Join(root, "certs.d")
	// Only pre-existing offline target options are used; revert discovers the
	// service configuration itself and never requires a new identity parameter.
	args := []string{"--store", "containerd", "--certs-dir", certs, "--state-dir", filepath.Join(root, "backups"), "--no-reload"}
	for _, action := range []string{"apply", "revert", "revert"} {
		if code, _, stderr := run(t, append([]string{"dockerd", action}, args...)...); code != 0 {
			t.Fatalf("%s: %s", action, stderr)
		}
	}
	data, err := os.ReadFile(filepath.Join(certs, "docker.io", "hosts.toml"))
	if err != nil || strings.Contains(string(data), config.Default().ProxyURL()) {
		t.Fatalf("default revert failed: %s %v", data, err)
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
	t.Setenv("USERPROFILE", home)
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
