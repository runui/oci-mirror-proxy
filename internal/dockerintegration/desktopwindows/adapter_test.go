package desktopwindows

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/daemonconf"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration/desktopmac"
	linuxintegration "github.com/runui/oci-mirror-proxy/internal/dockerintegration/linux"
)

func TestDesktopRenderApplyRevert(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "daemon.json")
	original := []byte("{\"debug\":true,\"registry-mirrors\":[\"https://existing.example\"]}\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	a := Adapter{Adapter: desktopmac.Adapter{Locations: dockerintegration.Paths{DaemonJSON: path}}}
	target := daemonconf.Target{ProxyURL: "http://127.0.0.1:24817", Routes: []daemonconf.Route{{Alias: "docker.io"}, {Alias: "ghcr.io"}}}
	plan, err := a.Render(t.Context(), target, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if !a.RequiresRestart() || plan.Store != daemonconf.StoreDesktop || len(plan.Warnings) != 1 || len(plan.Files) != 1 {
		t.Fatalf("unexpected Desktop plan: %+v", plan)
	}
	applier := daemonconf.Applier{BackupDir: filepath.Join(root, "state", "backups")}
	if _, err := applier.Apply(plan); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Debug   bool
		Mirrors []string `json:"registry-mirrors"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil || !cfg.Debug || len(cfg.Mirrors) != 2 || cfg.Mirrors[0] != target.ProxyURL {
		t.Fatalf("lost Engine settings: %s %v", data, err)
	}
	target.Remove = true
	plan, err = a.Render(t.Context(), target, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applier.Apply(plan); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil || !cfg.Debug || len(cfg.Mirrors) != 1 || cfg.Mirrors[0] != "https://existing.example" {
		t.Fatalf("remove lost settings: %s %v", data, err)
	}
	if _, err := a.Render(t.Context(), target, "legacy"); err == nil {
		t.Fatal("Desktop accepted an image-store override")
	}
	if err := a.Activate(t.Context(), false); err == nil {
		t.Fatal("Desktop restart did not require consent")
	}
}

func TestDesktopRejectsUnsafeConnections(t *testing.T) {
	a := Adapter{Adapter: desktopmac.Adapter{Locations: dockerintegration.Paths{DockerSocket: `\\remote\pipe\docker_engine`}}}
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONFIG", "")
	if err := a.Validate(t.Context()); err == nil || !strings.Contains(err.Error(), "not a local") {
		t.Fatalf("remote pipe accepted: %v", err)
	}
	t.Setenv("DOCKER_HOST", "tcp://localhost:2375")
	if err := a.Validate(t.Context()); err == nil || !strings.Contains(err.Error(), "DOCKER_HOST") {
		t.Fatalf("host override accepted: %v", err)
	}
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	if err := a.Validate(t.Context()); err == nil || !strings.Contains(err.Error(), "DOCKER_CONFIG") {
		t.Fatalf("config override accepted: %v", err)
	}
}

func TestWindowsDefaults(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", os.Getenv("USERPROFILE"))
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", os.Getenv("APPDATA"))
	t.Setenv("ProgramData", t.TempDir())
	p, err := Defaults()
	if err != nil || !filepath.IsAbs(p.DaemonJSON) || !filepath.IsAbs(p.StateDir) || p.DockerSocket != `\\.\pipe\dockerDesktopLinuxEngine` {
		t.Fatalf("Desktop defaults: %+v %v", p, err)
	}
	p, err = OfflineDefaults(false)
	if err != nil || p.DaemonJSON != filepath.Join(os.Getenv("ProgramData"), "docker", "config", "daemon.json") {
		t.Fatalf("offline defaults: %+v %v", p, err)
	}
	if _, err := OfflineDefaults(true); err == nil {
		t.Fatal("Windows rootless accepted")
	}
}

func TestOfflinePlanUsesWindowsActivationInstructions(t *testing.T) {
	a := OfflineAdapter{Adapter: linuxintegration.Adapter{Locations: dockerintegration.Paths{DaemonJSON: filepath.Join(t.TempDir(), "daemon.json")}}}
	plan, err := a.Render(t.Context(), daemonconf.Target{ProxyURL: "http://127.0.0.1:24817", Routes: []daemonconf.Route{{Alias: "docker.io"}}}, "legacy")
	if err != nil || !plan.NeedsReload || len(plan.Notes) != 1 || strings.Contains(plan.Notes[0], "SIGHUP") {
		t.Fatalf("Windows activation instructions: %+v %v", plan, err)
	}
}
