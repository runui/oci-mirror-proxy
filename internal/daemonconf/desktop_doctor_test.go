package daemonconf

import "testing"

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
