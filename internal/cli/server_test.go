package cli

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/admin"
	"github.com/runui/oci-mirror-proxy/internal/app"
	"github.com/runui/oci-mirror-proxy/internal/config"
)

func TestServerInfoQueriesServerWithoutLocalConfig(t *testing.T) {
	svc, err := app.New(t.Context(), app.Options{ConfigPath: writeConfig(t, config.Default()), Version: "server-test-version"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "info-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "a.sock")
	if runtime.GOOS == "windows" {
		socket = `\\.\pipe\oci-info-` + filepath.Base(dir)
	}
	listener, err := admin.Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(admin.NewHTTPServer(svc).Handler)
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()
	code, out, errOut := run(t, "server", "info", "--admin-socket", socket, "--config", "/missing/config.yaml", "--json")
	if code != 0 {
		t.Fatalf("query: %s", errOut)
	}
	var info admin.Info
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatal(err)
	}
	if info.Server.Version != "server-test-version" || info.Client.Version != version || info.SchemaVersion != 1 {
		t.Fatalf("wrong process metadata: %s", out)
	}
	code, out, errOut = run(t, "server", "info", "--admin-socket", socket)
	if code != 0 || !strings.Contains(out, "Server: server-test-version") {
		t.Fatalf("text: %s %s", out, errOut)
	}
	server.Close()
	code, out, _ = run(t, "server", "info", "--admin-socket", socket, "--json")
	if code == 0 || out != "" {
		t.Fatalf("unreachable server produced success: %s", out)
	}
}
