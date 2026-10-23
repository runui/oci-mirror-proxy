package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFragmentStoreLifecycle(t *testing.T) {
	main := filepath.Join(t.TempDir(), "config.yaml")
	s := FragmentStore{Main: main}
	if err := Save(main, Default()); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(main)
	if files, err := s.List(); err != nil || len(files) != 0 {
		t.Fatalf("list: %v %v", files, err)
	}
	if _, err := os.Stat(DropInDir(main)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created config.d")
	}
	data := []byte("# preserved comment\nregistries: [invalid\n")
	f, err := s.Write(t.Context(), "source@123.yaml", "", data)
	if err != nil || f.Revision == "" || !bytes.Equal(f.Data, data) {
		t.Fatalf("write: %+v %v", f, err)
	}
	if _, err := s.Write(t.Context(), f.Name, "", []byte("overwrite")); !errors.Is(err, ErrFragmentConflict) {
		t.Fatalf("stale create: %v", err)
	}
	if _, err := s.Write(t.Context(), f.Name, "wrong", nil); !errors.Is(err, ErrFragmentConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	next, err := s.Write(t.Context(), f.Name, f.Revision, []byte("# next\n"))
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(filepath.Join(DropInDir(main), f.Name+".bak"))
	if err != nil || !bytes.Equal(backup, data) {
		t.Fatalf("backup: %q %v", backup, err)
	}
	if _, err := s.Write(t.Context(), f.Name, next.Revision, nil); err != nil {
		t.Fatal(err)
	}
	current, _ := os.ReadFile(main)
	if !bytes.Equal(original, current) {
		t.Fatal("main configuration changed")
	}
}

func TestFragmentStoreRejectsEscapes(t *testing.T) {
	dir := t.TempDir()
	s := FragmentStore{Main: filepath.Join(dir, "config.yaml")}
	for _, name := range []string{"../config.yaml", "config.d/a.yaml", `/etc/a.yaml`, `a\b.yaml`, "a.yaml.bak", "a.yaml:stream", "..", ""} {
		if _, err := s.Read(name); err == nil {
			t.Fatalf("read accepted %q", name)
		}
		if _, err := s.Write(t.Context(), name, "", []byte("x")); err == nil {
			t.Fatalf("write accepted %q", name)
		}
	}
	if err := os.MkdirAll(DropInDir(s.Main), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "outside.yaml")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(DropInDir(s.Main), "link.yaml")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := s.Read("link.yaml"); err == nil {
		t.Fatal("read followed symlink")
	}
	if _, err := s.Write(t.Context(), "link.yaml", "", []byte("x")); err == nil {
		t.Fatal("write accepted symlink")
	}
	if _, err := s.List(); err == nil {
		t.Fatal("list accepted symlink")
	}
}

func TestFragmentStoreRejectsSymlinkDirectory(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	s := FragmentStore{Main: filepath.Join(dir, "config.yaml")}
	if err := os.Symlink(outside, DropInDir(s.Main)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := s.Write(t.Context(), "x.yaml", "", []byte("x")); err == nil {
		t.Fatal("accepted symlinked config.d")
	}
}
