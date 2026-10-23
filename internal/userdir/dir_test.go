package userdir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDir(t *testing.T) {
	t.Setenv("OCI_MIRROR_PROXY_CONFIG_HOME", "")
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Dir()
	if err != nil || got != filepath.Join(base, "oci-mirror-proxy") {
		t.Fatalf("%s %v", got, err)
	}
	override := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("OCI_MIRROR_PROXY_CONFIG_HOME", override)
	got, err = Dir()
	if err != nil || got != override {
		t.Fatalf("%s %v", got, err)
	}
	if _, err := os.Stat(override); !os.IsNotExist(err) {
		t.Fatal("resolver created directory")
	}
	t.Setenv("OCI_MIRROR_PROXY_CONFIG_HOME", "relative")
	if _, err := Dir(); err == nil {
		t.Fatal("accepted relative override")
	}
}
