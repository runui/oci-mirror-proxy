package app

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/config"
)

func TestInfoUsesAppliedConfiguration(t *testing.T) {
	cfg := baseConfig()
	svc, path := newService(t, cfg)
	before := svc.Info()
	// A disk edit is not a runtime change until Reload accepts the generation.
	next := cfg.Clone()
	next.Registries["example.com"] = config.Registry{Origin: "https://example.com"}
	if err := config.Save(path, next); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, svc.Info()) {
		t.Fatal("info read unapplied disk edits")
	}
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	after := svc.Info()
	if len(after.Config.Registries) != len(before.Config.Registries)+1 {
		t.Fatal("info did not follow reload")
	}
	if err := os.WriteFile(path, []byte("listen: [broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(); err == nil {
		t.Fatal("invalid reload accepted")
	}
	if !reflect.DeepEqual(after, svc.Info()) {
		t.Fatal("failed reload changed information")
	}
	data, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"upstreams", "password", "remotes", "admin", path} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("private field in info: %s", data)
		}
	}
}
