//go:build !windows

package admin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSystemAdminSocketPolicy(t *testing.T) {
	for _, tc := range []struct {
		path   string
		uid    int
		shared bool
	}{
		{"/run/oci-mirror-proxy/admin.sock", 0, true},
		{"/var/run/oci-mirror-proxy/admin.sock", 0, true},
		{"/run/oci-mirror-proxy/admin.sock", 501, false},
		{"/var/run/custom/admin.sock", 0, false},
		{"/tmp/admin.sock", 0, false},
	} {
		if got := isSystemAdminSocket(tc.path, tc.uid); got != tc.shared {
			t.Errorf("isSystemAdminSocket(%q, %d) = %v, want %v", tc.path, tc.uid, got, tc.shared)
		}
	}
}

func TestSystemSocketRejectsSymlinkDirectory(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "redirect")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := validateSystemSocketDirectory(link); err == nil {
		t.Fatal("accepted redirected system socket directory")
	}
}
