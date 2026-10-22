package autodetect

import (
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration"
	"testing"
)

func TestLocalEndpointOnly(t *testing.T) {
	for _, endpoint := range []string{"ssh://host", "tcp://127.0.0.1:2375", "unix://relative", ""} {
		if _, err := localSocket(endpoint); err == nil {
			t.Fatalf("accepted %q", endpoint)
		}
	}
	if got, err := localSocket("unix:///run/docker.sock"); err != nil || got != "/run/docker.sock" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestOfflineOverrides(t *testing.T) {
	t.Setenv("DOCKER_HOST", "ssh://must-not-contact")
	adapter, err := Resolve(t.Context(), Options{Store: "legacy", Overrides: dockerintegration.Paths{DaemonJSON: "/test/daemon.json", StateDir: "/test/state"}})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.RequiresRestart() || adapter.Paths().DaemonJSON != "/test/daemon.json" {
		t.Fatalf("unexpected adapter: %+v", adapter)
	}
}
