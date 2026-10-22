package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsLongPathAtomicReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), strings.Repeat("directory", 15), strings.Repeat("directory", 10), "config.yaml")
	for _, text := range []string{"old", "new"} {
		if err := WriteFileAtomic(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if data, err := ReadFile(path); err != nil || string(data) != text {
			t.Fatalf("long path read: %q %v", data, err)
		}
	}
	if err := RemoveFile(path); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsLockedReplacementPreservesOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteFileAtomic(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	err = WriteFileAtomic(path, []byte("new"), 0o600)
	f.Close()
	if err == nil {
		t.Fatal("replaced a file held without delete sharing")
	}
	if data, err := ReadFile(path); err != nil || string(data) != "old" {
		t.Fatalf("locked original changed: %q %v", data, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("staging files remain: %v %v", entries, err)
	}
}
