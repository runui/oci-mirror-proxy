package autodetect

import (
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration"
	"runtime"
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

func TestLocalNamedPipeEndpoint(t *testing.T) {
	for _, endpoint := range []string{"npipe:////remote/pipe/docker_engine", "npipe:////./pipe/"} {
		if _, err := localSocket(endpoint); err == nil {
			t.Fatalf("accepted %q", endpoint)
		}
	}
	got, err := localSocket("npipe:////./pipe/dockerDesktopLinuxEngine")
	if runtime.GOOS == "windows" {
		if err != nil || got != `\\.\pipe\dockerDesktopLinuxEngine` {
			t.Fatalf("%q %v", got, err)
		}
	} else if err == nil {
		t.Fatal("Windows pipe accepted on a non-Windows host")
	}
}

func TestOfflineOverrides(t *testing.T) {
	t.Setenv("DOCKER_HOST", "ssh://must-not-contact")
	adapter, err := Resolve(t.Context(), Options{Store: "legacy", Overrides: dockerintegration.Paths{DaemonJSON: "/test/daemon.json"}})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.RequiresRestart() != (runtime.GOOS == "windows") || adapter.Paths().DaemonJSON != "/test/daemon.json" {
		t.Fatalf("unexpected adapter: %+v", adapter)
	}
}
