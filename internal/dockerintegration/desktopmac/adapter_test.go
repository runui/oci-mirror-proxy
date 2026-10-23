package desktopmac

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/daemonconf"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration"
)

func TestRenderDesktop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	if err := os.WriteFile(path, []byte(`{"experimental":false,"builder":{"gc":{"enabled":true}},"registry-mirrors":["https://existing.example"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a := Adapter{Locations: dockerintegration.Paths{DaemonJSON: path}}
	target := daemonconf.Target{ProxyURL: "http://127.0.0.1:24817", Routes: []daemonconf.Route{{Alias: "docker.io"}, {Alias: "ghcr.io"}}}
	p, err := a.Render(t.Context(), target, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if p.Store != daemonconf.StoreDesktop || !p.NeedsReload || len(p.Warnings) != 1 || !strings.Contains(p.Notes[0], "restart") {
		t.Fatalf("unexpected plan: %+v", p)
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(p.Files[0].Content, &cfg); err != nil {
		t.Fatal(err)
	}
	var mirrors []string
	if err := json.Unmarshal(cfg["registry-mirrors"], &mirrors); err != nil {
		t.Fatal(err)
	}
	if len(mirrors) != 2 || mirrors[0] != target.ProxyURL || mirrors[1] != "https://existing.example" || string(cfg["experimental"]) != "false" || cfg["builder"] == nil {
		t.Fatalf("unexpected config: %s", p.Files[0].Content)
	}
	if err := os.WriteFile(path, p.Files[0].Content, 0o600); err != nil {
		t.Fatal(err)
	}
	next, err := a.Render(t.Context(), target, "auto")
	if err != nil || string(next.Files[0].Content) != string(p.Files[0].Content) {
		t.Fatalf("not idempotent: %v", err)
	}
	if _, err := a.Render(t.Context(), target, "containerd"); err == nil {
		t.Fatal("accepted Linux store override")
	}
}

func TestInvalidDesktopConfig(t *testing.T) {
	for _, data := range []string{"null", "[]", "broken", `{"registry-mirrors":123}`} {
		t.Run(data, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "daemon.json")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			a := Adapter{Locations: dockerintegration.Paths{DaemonJSON: path}}
			if _, err := a.Render(t.Context(), daemonconf.Target{Routes: []daemonconf.Route{{Alias: "docker.io"}}}, "auto"); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
}

func TestRestartRequiresConsent(t *testing.T) {
	if err := (Adapter{Docker: "must-not-execute"}).Activate(t.Context(), false); err == nil {
		t.Fatal("restart without consent")
	}
}

func TestDesktopReplacesOldLoopbackMirrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	data := []byte(`{"experimental":false,"registry-mirrors":["http://127.0.0.1:24817/","http://localhost:24817","http://[::1]:24817","http://localhost:5000","https://mirror.example","http://localhost:24817/custom"]}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	a := Adapter{Locations: dockerintegration.Paths{DaemonJSON: path}}
	target := daemonconf.Target{ProxyURL: "http://host.docker.internal:24817", Routes: []daemonconf.Route{{Alias: "docker.io"}}}
	plan, err := a.Render(t.Context(), target, "auto")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Mirrors []string `json:"registry-mirrors"`
	}
	if err := json.Unmarshal(plan.Files[0].Content, &cfg); err != nil {
		t.Fatal(err)
	}
	want := []string{target.ProxyURL, "http://localhost:5000", "https://mirror.example", "http://localhost:24817/custom"}
	if strings.Join(cfg.Mirrors, "|") != strings.Join(want, "|") {
		t.Fatalf("unexpected mirrors: %v", cfg.Mirrors)
	}
	if string(*plan.Files[0].CheckBefore) != string(data) {
		t.Fatal("migration lost original bytes for backup/concurrent-write detection")
	}
}

func TestRejectDockerHost(t *testing.T) {
	t.Setenv("DOCKER_HOST", "ssh://remote")
	if err := (Adapter{Docker: "must-not-execute"}).Validate(t.Context()); err == nil {
		t.Fatal("accepted remote target")
	}
}
