package daemonconf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConcurrentEditPreserved(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "daemon.json")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := Applier{StatePath: filepath.Join(root, "state.json"), BackupDir: filepath.Join(root, "backups")}
	plan := Plan{Files: []File{{Path: path, Content: []byte("managed")}}}
	if _, err := a.Apply(plan, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(plan, true); err == nil || !strings.Contains(err.Error(), "externally") {
		t.Fatalf("apply lost conflict: %v", err)
	}
	if _, err := a.Revert(); err == nil || !strings.Contains(err.Error(), "externally") {
		t.Fatalf("revert lost conflict: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "external" {
		t.Fatalf("external edit lost: %q, %v", data, err)
	}
	if err := os.WriteFile(path, []byte("managed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Revert(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatalf("original not restored: %q, %v", data, err)
	}
}

func TestIntegrationLock(t *testing.T) {
	root := t.TempDir()
	a := Applier{StatePath: filepath.Join(root, "state.json")}
	unlock, err := a.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := a.Apply(Plan{}, false); err == nil {
		t.Fatal("accepted concurrent apply")
	}
	if _, err := a.Revert(); err == nil {
		t.Fatal("accepted concurrent revert")
	}
}

func TestPlanSnapshotConflict(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "daemon.json")
	if err := os.WriteFile(path, []byte("concurrent edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := Applier{StatePath: filepath.Join(root, "state.json")}
	_, err := a.Apply(Plan{Files: []File{{Path: path, Content: []byte("merged"), CheckBefore: new("original")}}}, true)
	if err == nil || !strings.Contains(err.Error(), "changed while planning") {
		t.Fatalf("expected snapshot conflict, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "concurrent edit" {
		t.Fatalf("lost edit: %q, %v", data, err)
	}
}
