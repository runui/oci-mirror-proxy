package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/config"
)

func TestInstallerSyncsOfficialRemote(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("installer requires a Unix environment")
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		userConfig   bool
		systemConfig bool
		service      bool
		update       bool
		syncFail     bool
		initFail     bool
		serviceFail  bool
		remoteArgs   []string
		customConfig bool
		customExists bool
	}{
		{name: "first install"},
		{name: "existing user configuration", userConfig: true},
		{name: "existing system configuration", systemConfig: true},
		{name: "user configuration takes precedence", userConfig: true, systemConfig: true},
		{name: "update", userConfig: true, update: true},
		{name: "sync failure is nonfatal", syncFail: true},
		{name: "initialization failure is nonfatal", initFail: true},
		{name: "service uses system configuration", userConfig: true, service: true},
		{name: "service preserves configuration", systemConfig: true, service: true},
		{name: "service sync failure is nonfatal", service: true, syncFail: true},
		{name: "service failure remains fatal", service: true, serviceFail: true},
		{name: "forward remote options", remoteArgs: []string{"--url", "https://example.com/catalog.json?token=a&b=2", "--region", "cn,eu", "--dry-run", "--no-reload", "--allow-empty", "--admin-socket", "{socket}"}},
		{name: "forward empty region", remoteArgs: []string{"--region", ""}},
		{name: "forward equals options", remoteArgs: []string{"--url=https://example.com/catalog.json", "--region=eu", "--no-reload=true"}},
		{name: "forward all remotes", remoteArgs: []string{"--all"}},
		{name: "forward failure is nonfatal", remoteArgs: []string{"--url", "https://example.com/unavailable.json"}, syncFail: true},
		{name: "custom remote configuration", remoteArgs: []string{"--config", "{config}"}, customConfig: true},
		{name: "custom equals configuration", remoteArgs: []string{"--config={config}"}, customConfig: true},
		{name: "preserve custom configuration", remoteArgs: []string{"--config", "{config}"}, customConfig: true, customExists: true},
		{name: "custom configuration does not change service", remoteArgs: []string{"--config", "{config}"}, customConfig: true, service: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write := func(path, data string, mode os.FileMode) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(data), mode); err != nil {
					t.Fatal(err)
				}
			}
			// Redirect system paths into the fixture; never touch host services.
			isolated := string(script)
			for _, path := range []string{"/etc/oci-mirror-proxy", "/etc/systemd/system", "/etc/init.d", "/run/systemd/system", "/run/openrc", "/run/oci-mirror-proxy"} {
				isolated = strings.ReplaceAll(isolated, path, root+path)
			}
			scriptPath := filepath.Join(root, "install.sh")
			write(scriptPath, isolated, 0o755)
			home := filepath.Join(root, "home with spaces")
			configDir := filepath.Join(home, ".config")
			userConfig := filepath.Join(configDir, "oci-mirror-proxy", "config.yaml")
			systemConfig := filepath.Join(root, "etc", "oci-mirror-proxy", "config.yaml")
			customConfig := filepath.Join(root, "custom config", "proxy.yaml")
			if tc.userConfig {
				write(userConfig, "existing user configuration\n", 0o600)
			}
			if tc.systemConfig {
				write(systemConfig, "existing system configuration\n", 0o600)
			}
			if tc.customExists {
				write(customConfig, "existing custom configuration\n", 0o600)
			}
			if tc.service {
				for _, dir := range []string{"run/systemd/system", "etc/systemd/system"} {
					if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
						t.Fatal(err)
					}
				}
			}
			tools := filepath.Join(root, "tools")
			write(filepath.Join(tools, "uname"), `#!/bin/sh
case "$1" in -s) echo Linux ;; -m) echo x86_64 ;; *) exit 1 ;; esac
`, 0o755)
			write(filepath.Join(tools, "id"), "#!/bin/sh\necho 0\n", 0o755)
			write(filepath.Join(tools, "curl"), "#!/bin/sh\ncat \"$TEST_SOURCE\"\n", 0o755)
			write(filepath.Join(tools, "go"), `#!/bin/sh
set -eu
case "$1" in
    env) case "$2" in GOHOSTOS) echo linux ;; GOHOSTARCH) echo amd64 ;; *) exit 1 ;; esac ;;
    build)
        while [ "$#" -gt 0 ]; do
            if [ "$1" = -o ]; then cp "$TEST_BINARY" "$2"; exit 0; fi
            shift
        done
        exit 1
        ;;
    *) exit 1 ;;
esac
`, 0o755)
			write(filepath.Join(tools, "systemctl"), `#!/bin/sh
printf 'systemctl %s\n' "$*" >> "$TEST_LOG"
if [ "$1" = restart ] && [ "$TEST_SERVICE_FAIL" = true ]; then exit 1; fi
`, 0o755)
			binary := filepath.Join(root, "mock-binary")
			write(binary, `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$TEST_LOG"
case "$1" in
    init)
        if [ "$TEST_INIT_FAIL" = true ]; then echo 'mock init failure' >&2; exit 1; fi
        [ "$2" = --config ]
        if [ "$#" -ne 3 ]; then [ "$#" -eq 5 ] && [ "$4" = --admin-socket ]; fi
        mkdir -p "$(dirname "$3")"
        cp "$TEST_CONFIG_TEMPLATE" "$3"
        ;;
    remote)
        [ "$2" = update ] && [ "$3" = --config ] && [ -f "$4" ]
        printf '%s\000' "$@" > "$TEST_REMOTE_ARGS"
        if [ "$TEST_SYNC_FAIL" = true ]; then echo 'mock catalog unavailable' >&2; exit 1; fi
        echo 'mock synchronized'
        ;;
    *) exit 1 ;;
esac
`, 0o755)
			// Use the real init encoder so the script's YAML edit follows its output.
			defaults := config.Default()
			defaults.Registries["ghcr.io"] = config.Registry{}
			defaults.ApplyDefaults()
			template, err := config.Encode(defaults)
			if err != nil {
				t.Fatal(err)
			}
			templatePath := filepath.Join(root, "config-template.yaml")
			write(templatePath, string(template), 0o600)
			var source bytes.Buffer
			gz := gzip.NewWriter(&source)
			tw := tar.NewWriter(gz)
			module := "module example.com/installer-test\n"
			if err := tw.WriteHeader(&tar.Header{Name: "source/go.mod", Mode: 0o644, Size: int64(len(module))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write([]byte(module)); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			sourcePath := filepath.Join(root, "source.tar.gz")
			if err := os.WriteFile(sourcePath, source.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			prefix := filepath.Join(root, "prefix")
			logPath := filepath.Join(root, "commands.log")
			args := []string{scriptPath, "--build", "--version", "vtest", "--prefix", prefix}
			if tc.service {
				args = append(args, "--service")
			}
			if tc.update {
				args = append(args, "--update")
			}
			remoteArgs := make([]string, len(tc.remoteArgs))
			for i, arg := range tc.remoteArgs {
				arg = strings.ReplaceAll(arg, "{config}", customConfig)
				remoteArgs[i] = strings.ReplaceAll(arg, "{socket}", filepath.Join(root, "socket with spaces", "admin.sock"))
			}
			if len(remoteArgs) > 0 {
				args = append(args, "--")
				args = append(args, remoteArgs...)
			}
			remoteArgsPath := filepath.Join(root, "remote-args")
			cmd := exec.CommandContext(t.Context(), "sh", args...)
			cmd.Env = append(os.Environ(), "PATH="+tools+":"+os.Getenv("PATH"), "HOME="+home,
				"XDG_CONFIG_HOME="+configDir, "TMPDIR="+root, "DOWNLOAD_BASE_URL=https://download.example/repo",
				"TEST_SOURCE="+sourcePath, "TEST_BINARY="+binary, "TEST_LOG="+logPath,
				"TEST_CONFIG_TEMPLATE="+templatePath,
				"TEST_REMOTE_ARGS="+remoteArgsPath,
				"TEST_INIT_FAIL="+strconv.FormatBool(tc.initFail), "TEST_SYNC_FAIL="+strconv.FormatBool(tc.syncFail),
				"TEST_SERVICE_FAIL="+strconv.FormatBool(tc.serviceFail))
			out, runErr := cmd.CombinedOutput()
			log, _ := os.ReadFile(logPath)
			if tc.serviceFail {
				if runErr == nil || bytes.Contains(log, []byte("remote update")) || bytes.Contains(out, []byte("installation succeeded")) {
					t.Fatalf("service failure was ignored or attempted sync: err=%v\n%s\n%s", runErr, out, log)
				}
				return
			}
			if runErr != nil {
				t.Fatalf("installer failed: %v\n%s", runErr, out)
			}
			installed, err := os.ReadFile(filepath.Join(prefix, "bin", "oci-mirror-proxy"))
			if err != nil || !bytes.Contains(installed, []byte("mock synchronized")) {
				t.Fatalf("binary was not installed: %v", err)
			}
			wantConfig := userConfig
			if tc.service || (!tc.userConfig && tc.systemConfig) {
				wantConfig = systemConfig
			}
			if tc.customConfig {
				wantConfig = customConfig
			}
			wantArgs := append([]string{"remote", "update", "--config", wantConfig}, remoteArgs...)
			wantSync := strings.Join(wantArgs, " ") + "\n"
			if tc.initFail {
				if bytes.Contains(log, []byte("remote update")) {
					t.Fatalf("sync ran after initialization failed: %s", log)
				}
			} else if strings.Count(string(log), wantSync) != 1 {
				t.Fatalf("expected exactly one official remote sync using %s: %s", wantConfig, log)
			}
			if !tc.initFail {
				actual, err := os.ReadFile(remoteArgsPath)
				if err != nil || string(actual) != strings.Join(wantArgs, "\x00")+"\x00" {
					t.Fatalf("remote arguments were not preserved: got %q, want %q (%v)", actual, wantArgs, err)
				}
			}
			if tc.syncFail || tc.initFail {
				if !bytes.Contains(out, []byte("Warning: remote subscription sync failed; installation succeeded.")) {
					t.Fatalf("missing nonfatal sync warning: %s", out)
				}
			} else if !bytes.Contains(out, []byte("mock synchronized")) || bytes.Contains(out, []byte("Warning:")) {
				t.Fatalf("missing sync result or unexpected warning: %s", out)
			}
			if tc.service && strings.Index(string(log), "systemctl is-active") > strings.Index(string(log), wantSync) {
				t.Fatalf("sync ran before service setup completed: %s", log)
			}
			for path, content := range map[string]string{userConfig: "existing user configuration\n", systemConfig: "existing system configuration\n", customConfig: "existing custom configuration\n"} {
				if (path == userConfig && tc.userConfig) || (path == systemConfig && tc.systemConfig) || (path == customConfig && tc.customExists) {
					data, err := os.ReadFile(path)
					if err != nil || string(data) != content {
						t.Fatalf("existing configuration was changed: %s (%v)", path, err)
					}
				}
			}
			if !tc.initFail {
				for _, path := range []string{wantConfig, systemConfig} {
					if (path == userConfig && tc.userConfig) || (path == systemConfig && (!tc.service || tc.systemConfig)) || (path == customConfig && tc.customExists) {
						continue
					}
					cfg, err := config.Load(path)
					if err != nil {
						t.Fatal(err)
					}
					if !cfg.Registries["docker.io"].Adaptive.Enabled || cfg.Registries["docker.io"].Race.Enabled || cfg.Registries["ghcr.io"].Adaptive.Enabled {
						t.Fatalf("installer must only enable docker.io adaptive: %+v", cfg.Registries)
					}
					info, err := os.Stat(path)
					if err != nil || info.Mode().Perm() != 0o600 {
						t.Fatalf("generated configuration must remain private: %v (%v)", info, err)
					}
				}
			}
		})
	}
}
