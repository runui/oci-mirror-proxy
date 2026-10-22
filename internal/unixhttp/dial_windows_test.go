package unixhttp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPipePath(t *testing.T) {
	for _, path := range []string{`\\.\pipe\engine`, `//./pipe/engine`} {
		if got, err := PipePath(path); err != nil || got != `\\.\pipe\engine` {
			t.Fatalf("%q: %q %v", path, got, err)
		}
	}
	for _, path := range []string{"", `\\remote\pipe\engine`, `\\.\pipe\`} {
		if _, err := PipePath(path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	path := filepath.Join(t.TempDir(), "Admin.sock")
	a, err := PipePath(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PipePath(strings.ToLower(path))
	if err != nil || a != b {
		t.Fatalf("case aliases must select the same pipe: %q %q %v", a, b, err)
	}
}
