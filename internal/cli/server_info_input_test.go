package cli

import (
	"bytes"
	"context"

	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/admin"
	"github.com/runui/oci-mirror-proxy/internal/daemonconf"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration"
)

type verificationAdapter struct {
	dockerintegration.Adapter
	socket  string
	restart bool
}

func (a verificationAdapter) Paths() dockerintegration.Paths {
	return dockerintegration.Paths{DockerSocket: a.socket}
}
func (a verificationAdapter) RequiresRestart() bool { return a.restart }
func (a verificationAdapter) MergesConfig() bool    { return a.restart }

func TestIntegrationVerificationStates(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer proxy.Close()
	for _, tc := range []struct {
		name            string
		restart, loaded bool
		store           daemonconf.StoreKind
		want            string
	}{
		{"Linux loaded", false, true, daemonconf.StoreLegacy, "active"},
		{"Desktop pending", true, false, daemonconf.StoreDesktop, "pending Docker Desktop restart"},
		{"Desktop already active", true, true, daemonconf.StoreDesktop, "active"},
		{"containerd files", false, false, daemonconf.StoreContainerd, "image pull not tested"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "docker.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Skipf("Unix socket unavailable: %v", err)
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				info := daemonconf.Info{RegistryConfig: &daemonconf.RegistryConfig{}}
				if tc.loaded {
					info.RegistryConfig.Mirrors = []string{proxy.URL}
				}
				if tc.store == daemonconf.StoreContainerd {
					info.DriverStatus = [][2]string{{"driver-type", "io.containerd.snapshotter.v1"}}
				}
				_ = json.NewEncoder(w).Encode(info)
			}))
			server.Listener.Close()
			server.Listener = listener
			server.Start()
			defer server.Close()
			data, err := json.Marshal(admin.Info{ServerInfo: admin.ServerInfo{SchemaVersion: 1, Config: admin.ConfigInfo{ProxyURL: proxy.URL, Registries: []admin.RegistryInfo{{Alias: "docker.io", Origin: "https://registry-1.docker.io"}}}}})
			if err != nil {
				t.Fatal(err)
			}
			infoFile := filepath.Join(t.TempDir(), "info.json")
			if err := os.WriteFile(infoFile, data, 0o600); err != nil {
				t.Fatal(err)
			}
			flags := dockerFlags{serverInfo: infoFile, adapter: verificationAdapter{socket: socket, restart: tc.restart}}
			var out bytes.Buffer
			if err := verifyIntegration(t.Context(), &out, daemonconf.Plan{Store: tc.store}, &flags); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("unexpected result: %s", out.String())
			}
			// A canceled verification must fail, never claim activation from the
			// fact that configuration files were successfully written earlier.
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := verifyIntegration(ctx, &out, daemonconf.Plan{Store: tc.store}, &flags); err == nil {
				t.Fatal("canceled verification succeeded")
			}
		})
	}
}

func TestServerInfoApplyAndRevertedUpdate(t *testing.T) {
	data, err := json.Marshal(admin.Info{ServerInfo: admin.ServerInfo{SchemaVersion: 1, Config: admin.ConfigInfo{ProxyURL: "http://127.0.0.1:24817", Registries: []admin.RegistryInfo{{Alias: "docker.io", Origin: "https://registry-1.docker.io"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	certs := filepath.Join(root, "certs")
	infoFile := filepath.Join(root, "info.json")
	if err := os.WriteFile(infoFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--server-info", infoFile, "--store", "containerd", "--certs-dir", certs, "--state-dir", filepath.Join(root, "state")}
	apply := func(extra ...string) {
		t.Helper()
		command := append([]string{"dockerd", "apply"}, args...)
		command = append(command, extra...)
		if code, _, errOut := run(t, command...); code != 0 {
			t.Fatalf("apply: %s", errOut)
		}
	}
	apply("--if-managed")
	hosts := filepath.Join(certs, "docker.io", "hosts.toml")
	if data, err := os.ReadFile(hosts); err != nil || !strings.Contains(string(data), "24817") {
		t.Fatalf("update did not configure: %s %v", data, err)
	}
	apply()
	if code, _, errOut := run(t, append([]string{"dockerd", "revert"}, args...)...); code != 0 {
		t.Fatalf("revert: %s", errOut)
	}
	if data, err := os.ReadFile(hosts); err != nil || strings.Contains(string(data), "24817") {
		t.Fatalf("remove failed: %s %v", data, err)
	}
	apply("--if-managed")
	if data, err := os.ReadFile(hosts); err != nil || !strings.Contains(string(data), "24817") {
		t.Fatalf("update must inspect actual configuration: %s %v", data, err)
	}

}

func TestServerInfoRejectsUnsafeRoutes(t *testing.T) {
	for _, alias := range []string{"../escape", "..", `a\b`, "C:escape", "a\nb"} {
		t.Run(alias, func(t *testing.T) {
			err := validateDockerTarget(daemonconf.Target{ProxyURL: "http://127.0.0.1:24817", Routes: []daemonconf.Route{{Alias: alias, Origin: "https://registry-1.docker.io"}}})
			if err == nil {
				t.Fatal("unsafe alias accepted")
			}
		})
	}
}

func TestServerInfoInputValidation(t *testing.T) {
	for _, data := range []string{
		`{"schema_version":2,"config":{"proxy_url":"http://localhost","registries":[]}}`,
		`{"schema_version":1,"config":{"proxy_url":"http://localhost","registries":[]}}`,
		`{"schema_version":1,"config":{"proxy_url":"http://user:secret@localhost","registries":[{"alias":"docker.io","origin":"https://registry-1.docker.io"}]}}`,
		`{} {}`,
		"\xef\xbb\xbf{} {}",
		"\xef\xbb\xbf" + `{"schema_version":2}`,
		strings.Repeat(" ", (1<<20)+1),
	} {
		file := filepath.Join(t.TempDir(), "info.json")
		if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readServerTarget(&dockerFlags{serverInfo: file}); err == nil {
			t.Fatal("invalid information accepted")
		}
	}
	if _, err := readServerTarget(&dockerFlags{serverInfo: "unused", configPath: "unused"}); err == nil {
		t.Fatal("ambiguous inputs accepted")
	}
}

func TestServerInfoUTF8BOM(t *testing.T) {
	const data = `{"schema_version":1,"config":{"proxy_url":"http://127.0.0.1:24817","registries":[{"alias":"docker.io","origin":"https://registry-1.docker.io"}]}}`
	for _, prefix := range []string{"", "\xef\xbb\xbf"} {
		file := filepath.Join(t.TempDir(), "info.json")
		if err := os.WriteFile(file, []byte(prefix+data), 0o600); err != nil {
			t.Fatal(err)
		}
		target, err := readServerTarget(&dockerFlags{serverInfo: file})
		if err != nil || target.ProxyURL != "http://127.0.0.1:24817" || len(target.Routes) != 1 {
			t.Fatalf("prefix %q: target = %+v, error = %v", prefix, target, err)
		}
	}
}
