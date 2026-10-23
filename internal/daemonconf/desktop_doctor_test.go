package daemonconf

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesktopProbeUsesHostLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/" {
			t.Errorf("unexpected probe path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	proxyURL := strings.Replace(server.URL, "127.0.0.1", "host.docker.internal", 1)
	checks := Diagnose(t.Context(), DiagnoseInput{
		ProxyURL: proxyURL, MirrorSettings: true,
		Info: Info{RegistryConfig: &RegistryConfig{Mirrors: []string{proxyURL}}},
	})
	for _, check := range checks {
		if !check.OK {
			t.Fatalf("Desktop host probe or original mirror comparison failed: %+v", check)
		}
	}
}

func TestMirrorDiagnosticIndependentOfStore(t *testing.T) {
	for _, mirrors := range [][]string{nil, {"http://127.0.0.1:24817/"}} {
		checks := Diagnose(t.Context(), DiagnoseInput{
			ProxyURL:       "http://127.0.0.1:24817",
			MirrorSettings: true,
			Info:           Info{DriverStatus: [][2]string{{"driver-type", "io.containerd.snapshotter.v1"}}, RegistryConfig: &RegistryConfig{Mirrors: mirrors}},
		})
		found := false
		for _, check := range checks {
			if check.Name == "dockerd registry-mirrors" {
				found = true
				if check.OK != (len(mirrors) != 0) {
					t.Fatalf("unexpected check: %+v", check)
				}
			}
		}
		if !found {
			t.Fatal("loaded mirrors were not checked for containerd")
		}
	}
}
