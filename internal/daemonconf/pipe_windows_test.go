package daemonconf

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/admin"
)

func TestFetchInfoOverNamedPipe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := admin.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/info" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"OperatingSystem":"Docker Desktop","OSType":"linux","RegistryConfig":{"Mirrors":["http://127.0.0.1:24817"]}}`)
	})}
	go srv.Serve(ln)
	defer srv.Close()
	info, err := FetchInfo(t.Context(), path)
	if err != nil || info.OperatingSystem != "Docker Desktop" || info.OSType != "linux" || info.RegistryConfig == nil || len(info.RegistryConfig.Mirrors) != 1 {
		t.Fatalf("named pipe /info: %+v %v", info, err)
	}
}
