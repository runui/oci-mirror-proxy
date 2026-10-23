// Keep installer tests together in this package. Extend the existing platform
// suites instead of adding a test file for each command, feature or scenario.
package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"github.com/runui/oci-mirror-proxy/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestInstallerSyncsOfficialRemote(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("installer requires a Unix environment")
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name            string
		userConfig      bool
		systemConfig    bool
		service         bool
		update          bool
		syncFail        bool
		initFail        bool
		serviceFail     bool
		remoteArgs      []string
		customConfig    bool
		customExists    bool
		darwin          bool
		sudo            bool
		invalidSudo     bool
		rootSudo        bool
		infoFail        bool
		unprivileged    bool
		denySudo        bool
		rootlessDocker  bool
		existingService bool
		customService   bool
		skipDocker      bool
		dockerFail      bool
	}{
		{name: "first install"},
		{name: "service applies Docker", service: true},
		{name: "user service elevates only system operations", service: true, unprivileged: true},
		{name: "user macOS service retains Desktop identity", service: true, darwin: true, unprivileged: true},
		{name: "denied service elevation preserves installation", service: true, unprivileged: true, denySudo: true},
		{name: "root sudo service applies Docker", service: true, rootSudo: true},
		{name: "root sudo service update maintains Docker", service: true, rootSudo: true, update: true},
		{name: "sudo macOS service applies as user", service: true, darwin: true, sudo: true},
		{name: "service skips Docker", service: true, skipDocker: true},
		{name: "Docker failure preserves service", service: true, dockerFail: true},
		{name: "Docker identity failure is a warning", service: true, sudo: true, invalidSudo: true},
		{name: "Docker information failure is a warning", service: true, darwin: true, sudo: true, infoFail: true},
		{name: "rootless Docker requires manual setup", service: true, unprivileged: true, rootlessDocker: true},
		{name: "service update merges actual Docker configuration", service: true, update: true},
		{name: "macOS first install", darwin: true},
		{name: "macOS service", darwin: true, service: true},
		{name: "macOS service failure", darwin: true, service: true, serviceFail: true},
		{name: "macOS service update", darwin: true, service: true, existingService: true},
		{name: "macOS update rollback", darwin: true, service: true, existingService: true, serviceFail: true},
		{name: "macOS preserves custom service", darwin: true, service: true, customService: true},
		{name: "sudo macOS install", darwin: true, sudo: true},
		{name: "sudo Linux install", sudo: true},
		{name: "unverified sudo account", sudo: true, invalidSudo: true},
		{name: "sudo argument boundaries", darwin: true, sudo: true, remoteArgs: []string{"--region", "", "--url", "https://example.com/a?x=1&y=2"}},
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
			for _, path := range []string{"/etc/oci-mirror-proxy", "/etc/systemd/system", "/etc/init.d", "/run/systemd/system", "/run/openrc", "/run/oci-mirror-proxy", "/Library/LaunchDaemons", "/var/log/oci-mirror-proxy", "/var/run/oci-mirror-proxy"} {
				isolated = strings.ReplaceAll(isolated, path, root+path)
			}
			scriptPath := filepath.Join(root, "install.sh")
			write(scriptPath, isolated, 0o755)
			home := filepath.Join(root, "home with spaces")
			configDir := filepath.Join(home, ".config")
			userConfig := filepath.Join(configDir, "oci-mirror-proxy", "config.yaml")
			if tc.darwin {
				userConfig = filepath.Join(home, "Library", "Application Support", "oci-mirror-proxy", "config.yaml")
			}
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
case "$1" in -s) echo "$TEST_OS" ;; -m) echo x86_64 ;; *) exit 1 ;; esac
`, 0o755)
			write(filepath.Join(tools, "id"), `#!/bin/sh
if [ "$#" -eq 2 ]; then
    if [ "$2" = root ]; then echo 0; else echo 501; fi
elif [ "$1" = -un ]; then echo fixture-user
else echo "$TEST_UID"; fi
`, 0o755)
			write(filepath.Join(tools, "sudo"), `#!/bin/sh
set -eu
if [ "$1" = -- ]; then
    shift
    printf 'sudo-system\n' >> "$TEST_LOG"
    [ "$TEST_DENY_SUDO" != true ] || exit 1
    exec "$@"
fi
[ "$1" = -H ] && [ "$2" = -u ] && { [ "$3" = fixture-user ] || [ "$3" = root ]; } && [ "$4" = -- ]
shift 4
export HOME="$TEST_USER_HOME"
printf 'sudo-user\n' >> "$TEST_LOG"
exec "$@"
`, 0o755)
			write(filepath.Join(tools, "docker"), `#!/bin/sh
set -eu
[ "$*" = "context inspect --format {{.Endpoints.docker.Host}}" ]
echo unix:///var/run/docker.sock
`, 0o755)
			write(filepath.Join(tools, "launchctl"), `#!/bin/sh
printf 'launchctl %s\n' "$*" >> "$TEST_LOG"
case "$1" in
    print) [ -f "$TEST_LOADED" ] || exit 1; echo 'pid = 123' ;;
    bootout) rm -f "$TEST_LOADED" ;;
    bootstrap)
        if [ "$TEST_SERVICE_FAIL" = true ] && [ ! -f "$TEST_FAILED" ]; then touch "$TEST_FAILED"; exit 1; fi
        touch "$TEST_LOADED"
        ;;
esac
`, 0o755)
			write(filepath.Join(tools, "chown"), "#!/bin/sh\nexit 0\n", 0o755)
			write(filepath.Join(tools, "plutil"), "#!/bin/sh\nexit 0\n", 0o755)
			// Ownership arguments are validated separately on real macOS; the fixture
			// must run unprivileged on Linux too, where the wheel group may not exist.
			write(filepath.Join(tools, "install"), `#!/bin/sh
if [ "$1" = -o ]; then shift 4; fi
exec /usr/bin/install "$@"
`, 0o755)
			write(filepath.Join(tools, "curl"), "#!/bin/sh\ncat \"$TEST_SOURCE\"\n", 0o755)
			write(filepath.Join(tools, "go"), `#!/bin/sh
set -eu
case "$1" in
    env) case "$2" in GOHOSTOS) echo linux ;; GOHOSTARCH) echo amd64 ;; *) exit 1 ;; esac ;;
    build)
        printf 'build-user\n' >> "$TEST_LOG"
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
        if [ "$#" -ne 3 ]; then
            [ "$4" = --admin-socket ]
            if [ "$TEST_OS" = Darwin ]; then
                [ "$#" -eq 9 ] && [ "$6" = --listen ] && [ "$7" = 0.0.0.0:24817 ] &&
                    [ "$8" = --advertise ] && [ "$9" = host.docker.internal:24817 ]
            else [ "$#" -eq 5 ]; fi
        fi
        mkdir -p "$(dirname "$3")"
        cp "$TEST_CONFIG_TEMPLATE" "$3"
        ;;
    remote)
        [ "$2" = update ]
        printf '%s\000' "$@" > "$TEST_REMOTE_ARGS"
        if [ "$TEST_SYNC_FAIL" = true ]; then echo 'mock catalog unavailable' >&2; exit 1; fi
        echo 'mock synchronized'
        ;;
    status) exit 0 ;;
    server)
        [ "$TEST_INFO_FAIL" != true ] || exit 1
        echo '{"schema_version":1,"config":{"proxy_url":"http://127.0.0.1:24817","registries":[{"alias":"docker.io","origin":"https://registry-1.docker.io"}]}}'
        ;;
    dockerd)
        case "$2" in
            apply)
                [ "$TEST_DOCKER_FAIL" != true ] || exit 1
                echo 'Docker integration: active'
                ;;
            *) exit 1 ;;
        esac
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
			servicePath := filepath.Join(root, "Library", "LaunchDaemons", "com.runui.oci-mirror-proxy.plist")
			loadedPath := filepath.Join(root, "loaded")
			if tc.customService {
				write(servicePath, "custom service\n", 0o644)
			}
			logPath := filepath.Join(root, "commands.log")
			args := []string{scriptPath, "--build", "--version", "vtest", "--prefix", prefix}
			if tc.service {
				args = append(args, "--service")
			}
			if tc.skipDocker || (tc.darwin && tc.service && !tc.sudo && !tc.unprivileged) {
				args = append(args, "--skip-docker")
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
			platform := "Linux"
			if tc.darwin {
				platform = "Darwin"
			}
			uid := "501"
			if (tc.service && !tc.unprivileged) || tc.sudo {
				uid = "0"
			}
			cmd.Env = append(os.Environ(), "PATH="+tools+":"+os.Getenv("PATH"), "HOME="+home,
				"TEST_OS="+platform, "TEST_UID="+uid, "SUDO_USER=", "SUDO_UID=", "SUDO_GID=",
				"TEST_LOADED="+loadedPath, "TEST_FAILED="+filepath.Join(root, "failed"),
				"XDG_CONFIG_HOME="+configDir, "TMPDIR="+root, "DOWNLOAD_BASE_URL=https://download.example/repo",
				"TEST_SOURCE="+sourcePath, "TEST_BINARY="+binary, "TEST_LOG="+logPath,
				"TEST_CONFIG_TEMPLATE="+templatePath,
				"TEST_REMOTE_ARGS="+remoteArgsPath,
				"TEST_INIT_FAIL="+strconv.FormatBool(tc.initFail), "TEST_SYNC_FAIL="+strconv.FormatBool(tc.syncFail),
				"TEST_SERVICE_FAIL="+strconv.FormatBool(tc.serviceFail))
			cmd.Env = append(cmd.Env, "TEST_DOCKER_FAIL="+strconv.FormatBool(tc.dockerFail), "TEST_INFO_FAIL="+strconv.FormatBool(tc.infoFail), "TEST_DENY_SUDO="+strconv.FormatBool(tc.denySudo))
			if tc.rootSudo {
				cmd.Env = append(cmd.Env, "SUDO_USER=root", "SUDO_UID=0", "SUDO_GID=0", "TEST_USER_HOME="+home,
					"DOCKER_HOST=", "DOCKER_CONTEXT=")
			}
			if tc.rootlessDocker {
				cmd.Env = append(cmd.Env, "DOCKER_HOST=unix:///run/user/501/docker.sock", "DOCKER_CONTEXT=")
			}
			if tc.sudo {
				sudoUID := "501"
				if tc.invalidSudo {
					sudoUID = "999"
				}
				cmd.Env = append(cmd.Env, "SUDO_USER=fixture-user", "SUDO_UID="+sudoUID, "SUDO_GID=501", "TEST_USER_HOME="+home,
					"HOME="+filepath.Join(root, "wrong inherited home"))
				if !tc.darwin {
					userConfig = filepath.Join(home, ".config", "oci-mirror-proxy", "config.yaml")
				}
			}
			if tc.existingService {
				// First create the exact generated definition, then exercise replacement
				// or recovery of an already loaded service with a distinct executable.
				first := exec.CommandContext(t.Context(), "sh", args...)
				first.Env = append(cmd.Environ(), "TEST_SERVICE_FAIL=false")
				if out, err := first.CombinedOutput(); err != nil {
					t.Fatalf("initial service installation: %v\n%s", err, out)
				}
				write(filepath.Join(prefix, "bin", "oci-mirror-proxy"), "previous executable\n", 0o755)
				write(logPath, "", 0o600)
			}
			out, runErr := cmd.CombinedOutput()
			log, _ := os.ReadFile(logPath)
			if tc.unprivileged {
				built := bytes.Index(log, []byte("build-user\n"))
				elevated := bytes.Index(log, []byte("sudo-system\n"))
				if built < 0 || elevated < built {
					t.Fatalf("build did not precede elevation: %s", log)
				}
			}
			if tc.denySudo {
				if runErr == nil || bytes.Contains(log, []byte("systemctl restart")) {
					t.Fatalf("denied elevation changed the service: %v\n%s", runErr, log)
				}
				if _, err := os.Stat(filepath.Join(prefix, "bin", "oci-mirror-proxy")); !os.IsNotExist(err) {
					t.Fatalf("denied elevation published a binary: %v", err)
				}
				return
			}
			if tc.customService {
				data, _ := os.ReadFile(servicePath)
				if runErr == nil || string(data) != "custom service\n" || bytes.Contains(log, []byte("launchctl bootstrap")) {
					t.Fatalf("custom service overwritten: %v\n%s\n%s", runErr, out, log)
				}
				return
			}
			if tc.serviceFail {
				if runErr == nil || bytes.Contains(log, []byte("remote update")) || bytes.Contains(out, []byte("installation succeeded")) {
					t.Fatalf("service failure was ignored or attempted sync: err=%v\n%s\n%s", runErr, out, log)
				}
				if tc.existingService {
					data, _ := os.ReadFile(filepath.Join(prefix, "bin", "oci-mirror-proxy"))
					if string(data) != "previous executable\n" || !bytes.Contains(out, []byte("Restoring the previous")) {
						t.Fatalf("previous binary not restored: %s\n%s", data, out)
					}
					if _, err := os.Stat(loadedPath); err != nil {
						t.Fatalf("previous service not loaded: %v", err)
					}
				}
				return
			}
			if tc.dockerFail || tc.infoFail || tc.rootlessDocker || (tc.service && tc.invalidSudo) {
				if runErr != nil || !bytes.Contains(out, []byte("Warning: proxy installation succeeded, but Docker integration failed")) || !bytes.Contains(out, []byte("dockerd apply --server-info server-info.json --verify")) || !bytes.Contains(out, []byte("server info --admin-socket")) {
					t.Fatalf("Docker failure not reported: %v\n%s", runErr, out)
				}
				if _, err := os.Stat(filepath.Join(prefix, "bin", "oci-mirror-proxy")); err != nil {
					t.Fatal(err)
				}
				if tc.rootlessDocker && bytes.Contains(log, []byte("dockerd apply")) {
					t.Fatalf("rootless Docker was enrolled as system Docker: %s", log)
				}
				return
			}
			if runErr != nil {
				t.Fatalf("installer failed: %v\n%s", runErr, out)
			}
			if tc.invalidSudo {
				if bytes.Contains(log, []byte("remote update")) || bytes.Contains(log, []byte("init --config")) || !bytes.Contains(out, []byte("Cannot validate the sudo account")) {
					t.Fatalf("unverified account touched configuration: %s\n%s", out, log)
				}
				return
			}
			applied := bytes.Contains(log, []byte("dockerd apply"))
			if applied != (tc.service && !tc.skipDocker && (!tc.darwin || tc.sudo || tc.unprivileged)) {
				t.Fatalf("unexpected Docker enrollment: %s", log)
			}
			if !tc.service && !tc.sudo && bytes.Contains(log, []byte("sudo-system\n")) {
				t.Fatalf("user-writable binary installation requested sudo: %s", log)
			}
			if applied {
				if bytes.Contains(log, []byte("--restart")) || bytes.Contains(log, []byte("--force")) || bytes.Contains(log, []byte("--no-reload")) {
					t.Fatalf("unsafe Docker flags: %s", log)
				}
				if bytes.Contains(log, []byte("--if-managed")) {
					t.Fatalf("update depends on historical state: %s", log)
				}
				if strings.Index(string(log), "status --admin-socket") > strings.Index(string(log), "dockerd apply") {
					t.Fatalf("applied before ready: %s", log)
				}
			}
			if tc.darwin && tc.service {
				definition, err := os.ReadFile(servicePath)
				if err != nil || !bytes.Contains(definition, []byte("<key>ProgramArguments</key>")) || !bytes.Contains(definition, []byte("<key>XDG_RUNTIME_DIR</key>")) {
					t.Fatalf("missing launchd arguments or runtime environment: %v\n%s", err, definition)
				}
				if runtime.GOOS == "darwin" {
					lint := exec.CommandContext(t.Context(), "/usr/bin/plutil", "-lint", servicePath)
					if out, err := lint.CombinedOutput(); err != nil {
						t.Fatalf("invalid generated plist: %v\n%s", err, out)
					}
				}
			}
			if tc.sudo && !tc.service && bytes.Count(log, []byte("sudo-user\n")) < 2 {
				t.Fatalf("configuration operations did not drop privileges: %s", log)
			}
			installed, err := os.ReadFile(filepath.Join(prefix, "bin", "oci-mirror-proxy"))
			if err != nil || !bytes.Contains(installed, []byte("mock synchronized")) {
				t.Fatalf("binary was not installed: %v", err)
			}
			wantArgs := append([]string{"remote", "update"}, remoteArgs...)
			if tc.service {
				endpoint := filepath.Join(root, "run", "oci-mirror-proxy", "admin.sock")
				// The fixture rewrites /run paths, including the /run suffix of /var/run.
				if tc.darwin {
					endpoint = "/var" + endpoint
				}
				wantArgs = append(wantArgs, "--admin-socket", endpoint)
			}
			wantSync := strings.Join(wantArgs, " ") + "\n"
			if strings.Count(string(log), wantSync) != 1 {
				t.Fatalf("expected one API-based sync: %s", log)
			}
			actual, err := os.ReadFile(remoteArgsPath)
			if err != nil || string(actual) != strings.Join(wantArgs, "\x00")+"\x00" {
				t.Fatalf("arguments: %q want %q (%v)", actual, wantArgs, err)
			}
			if tc.syncFail {
				if !bytes.Contains(out, []byte("Warning: remote subscription sync failed; installation succeeded.")) {
					t.Fatalf("missing warning: %s", out)
				}
			} else if !bytes.Contains(out, []byte("mock synchronized")) || bytes.Contains(out, []byte("Warning:")) {
				t.Fatalf("unexpected result: %s", out)
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
			if tc.service && !tc.systemConfig {
				cfg, err := config.Load(systemConfig)
				if err != nil {
					t.Fatal(err)
				}
				if !cfg.Registries["docker.io"].Adaptive.Enabled {
					t.Fatal("missing docker.io adaptive default")
				}
				info, err := os.Stat(systemConfig)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("configuration permissions: %v %v", info, err)
				}
			}

		})
	}
}

func TestInstallerReleasePlatforms(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("installer requires a Unix environment")
	}
	for _, tc := range []struct {
		os, machine, platform, member string
		badChecksum                   bool
	}{
		{"Linux", "x86_64", "linux_amd64", "./oci-mirror-proxy", false},
		{"Linux", "aarch64", "linux_arm64", "oci-mirror-proxy", false},
		{"Linux", "armv7l", "linux_armv7", "oci-mirror-proxy", false},
		{"Linux", "riscv64", "linux_riscv64", "oci-mirror-proxy", false},
		{"Darwin", "x86_64", "darwin_amd64", "oci-mirror-proxy", false},
		{"Darwin", "arm64", "darwin_arm64", "oci-mirror-proxy", false},
		{"FreeBSD", "amd64", "freebsd_amd64", "oci-mirror-proxy", false},
		{"FreeBSD", "arm64", "freebsd_arm64", "oci-mirror-proxy", false},
		{"Darwin", "arm64", "darwin_arm64", "oci-mirror-proxy", true},
	} {
		t.Run(fmt.Sprintf("%s/corrupt=%t", tc.platform, tc.badChecksum), func(t *testing.T) {
			root := t.TempDir()
			tools := filepath.Join(root, "tools")
			if err := os.Mkdir(tools, 0o755); err != nil {
				t.Fatal(err)
			}
			uname := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in -s) echo %s ;; -m) echo %s ;; esac\n", tc.os, tc.machine)
			if err := os.WriteFile(filepath.Join(tools, "uname"), []byte(uname), 0o755); err != nil {
				t.Fatal(err)
			}
			binary := []byte("#!/bin/sh\nexit 0\n")
			var archive bytes.Buffer
			gz := gzip.NewWriter(&archive)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(&tar.Header{Name: tc.member, Mode: 0o755, Size: int64(len(binary))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(binary); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			asset := "oci-mirror-proxy_v1.2.3_" + tc.platform + ".tar.gz"
			sum := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
			if tc.badChecksum {
				sum = strings.Repeat("0", 64)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/releases/download/v1.2.3/" + asset:
					_, _ = w.Write(archive.Bytes())
				case "/releases/download/v1.2.3/SHA256SUMS":
					_, _ = fmt.Fprintf(w, "%s  %s\n", sum, asset)
				default:
					http.Error(w, "unexpected request", http.StatusBadRequest)
				}
			}))
			defer server.Close()
			prefix := filepath.Join(root, "prefix")
			// Exercise the explicit and legacy entry points against real archives.
			entry := []string{"install"}
			switch tc.machine {
			case "aarch64", "arm64":
				entry = []string{"update"}
			case "armv7l":
				entry = []string{"--update"}
			case "riscv64":
				entry = nil
			}
			args := append([]string{"../../../install.sh"}, entry...)
			args = append(args, "--version", "v1.2.3", "--prefix", prefix)
			cmd := exec.CommandContext(t.Context(), "sh", args...)
			cmd.Env = append(os.Environ(), "PATH="+tools+":"+os.Getenv("PATH"), "DOWNLOAD_BASE_URL="+server.URL, "HOME="+root, "XDG_CONFIG_HOME="+root, "TMPDIR="+root)
			out, err := cmd.CombinedOutput()
			installed, readErr := os.ReadFile(filepath.Join(prefix, "bin", "oci-mirror-proxy"))
			if tc.badChecksum {
				if err == nil || !bytes.Contains(out, []byte("SHA256 verification failed")) || !os.IsNotExist(readErr) {
					t.Fatalf("corrupt archive accepted: %v, %v\n%s", err, readErr, out)
				}
				return
			}
			if err != nil || readErr != nil || !bytes.Equal(installed, binary) {
				t.Fatalf("install failed: %v, %v\n%s", err, readErr, out)
			}
		})
	}
}

func TestInstallerCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}
	for _, args := range [][]string{
		{"install", "--update"}, {"uninstall", "--update"},
		{"uninstall", "--build"}, {"uninstall", "--version", "v1"},
		{"uninstall", "--", "--config", "other"}, {"update", "uninstall"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "sh", append([]string{"../../../install.sh"}, args...)...)
			if out, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("accepted invalid arguments: %s", out)
			}
		})
	}
	for _, action := range []string{"install", "update", "uninstall"} {
		cmd := exec.CommandContext(t.Context(), "sh", "../../../install.sh", action, "--help")
		if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "uninstall") {
			t.Fatalf("help failed: %v: %s", err, out)
		}
	}
}

func TestInstallerUninstallPreservesData(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(bin, "oci-mirror-proxy")
	for _, path := range []string{destination, filepath.Join(bin, "other-tool"), filepath.Join(root, "config.yaml")} {
		if err := os.WriteFile(path, []byte("preserve"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// No valid release URL or version is needed for offline removal.
	for range 2 {
		cmd := exec.CommandContext(t.Context(), "sh", "../../../install.sh", "uninstall", "--prefix", root, "--skip-docker")
		cmd.Env = append(os.Environ(), "DOWNLOAD_BASE_URL=invalid", "VERSION=invalid/tag")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("uninstall: %v: %s", err, out)
		}
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("binary remains: %v", err)
	}
	for _, path := range []string{filepath.Join(bin, "other-tool"), filepath.Join(root, "config.yaml")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInstallerUninstallRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "keep")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(bin, "oci-mirror-proxy")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "sh", "../../../install.sh", "uninstall", "--prefix", root, "--skip-docker")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "symlink") {
		t.Fatalf("symlink accepted: %v: %s", err, out)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "keep" {
		t.Fatalf("target changed: %v", err)
	}
}
