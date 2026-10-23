package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/admin"
	"github.com/runui/oci-mirror-proxy/internal/app"
)

// startTestAPI exercises real IPC without coupling client selection to YAML.
func startTestAPI(t *testing.T, main string) string {
	t.Helper()
	svc, err := app.New(t.Context(), app.Options{ConfigPath: main})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })
	dir, err := os.MkdirTemp("", "omp-api-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "a.sock")
	listener, err := admin.Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	server := admin.NewHTTPServer(svc)
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	t.Setenv("OCI_MIRROR_PROXY_CONFIG_HOME", t.TempDir())
	return socket
}
