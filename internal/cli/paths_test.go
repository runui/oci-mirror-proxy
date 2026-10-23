package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/config"
)

func TestDiscoverConfigPath(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.yaml")
	system := filepath.Join(dir, "system.yaml")
	check := func(want string) {
		t.Helper()
		got, err := discoverConfigPath(user, system)
		if err != nil || got != want {
			t.Fatalf("got %q, %v; want %q", got, err, want)
		}
	}
	check(user)
	if err := config.Save(system, config.Default()); err != nil {
		t.Fatal(err)
	}
	check(system)
	if err := config.Save(user, config.Default()); err != nil {
		t.Fatal(err)
	}
	check(user)
}

func TestSystemAdminSocket(t *testing.T) {
	for _, tc := range []struct{ os, want string }{
		{"darwin", "/var/run/oci-mirror-proxy/admin.sock"},
		{"linux", "/run/oci-mirror-proxy/admin.sock"},
	} {
		if got := systemAdminSocket(tc.os); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.os, got, tc.want)
		}
	}
}

func TestResolveAdminSocket(t *testing.T) {
	cfg := config.Default()
	cfg.Admin.Socket = filepath.Join(t.TempDir(), "configured.sock")
	path := writeConfig(t, cfg)
	fallback, err := config.DefaultAdminSocket()
	if err != nil {
		t.Fatal(err)
	}
	defaultPath := writeConfig(t, config.Default())
	for _, tc := range []struct {
		name, explicit, path, want string
		wantErr                    bool
	}{
		{name: "configuration ignored", path: path, want: fallback},
		{name: "empty socket uses user default", path: defaultPath, want: fallback},
		{name: "flag overrides configuration", explicit: "/override.sock", path: path, want: "/override.sock"},
		{name: "flag bypasses missing configuration", explicit: "/override.sock", path: path + ".missing", want: "/override.sock"},
		{name: "missing configuration ignored", path: path + ".missing", want: fallback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveAdminSocket(tc.explicit, tc.path)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("got %q, %v; want %q, error=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
	if err := os.WriteFile(path, []byte("invalid: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveAdminSocket("", path); err != nil || got != fallback {
		t.Fatal("client must not read invalid service configuration")
	}
}

func TestInitAdminSocketAndClients(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	socket := filepath.Join(t.TempDir(), "configured.sock")
	if code, _, errOut := run(t, "init", "--config", path, "--admin-socket", socket); code != 0 {
		t.Fatalf("init: %d: %s", code, errOut)
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.Admin.Socket != socket {
		t.Fatalf("init configuration: %+v, %v", cfg, err)
	}
	if got, err := resolveServiceSocket("", path); err != nil || got != socket {
		t.Fatalf("service must retain configured listener: %q %v", got, err)
	}
	for _, args := range [][]string{
		{"status"}, {"reload"}, {"upstream", "list"},
		{"upstream", "add", "--id", "test", "--url", "https://mirror.example.com"},
		{"upstream", "enable", "--id", "test"},
		{"upstream", "disable", "--id", "test"},
		{"upstream", "remove", "--id", "test"},
	} {
		args = append(args, "--config", path, "--admin-socket", socket)
		code, _, errOut := run(t, args...)
		if code != 1 || !strings.Contains(errOut, socket) {
			t.Fatalf("%v: code %d, stderr %q; want configured socket", args, code, errOut)
		}
	}
}
