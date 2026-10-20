package fsutil

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAtomicReplaceReportsPostRenameSyncFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("directory sync failed")
	err := writeFileAtomic(path, []byte("new"), 0o600, func(dir string) error {
		if dir != filepath.Dir(path) {
			t.Fatalf("sync directory = %s", dir)
		}
		return failure
	})
	published, ok := errors.AsType[*DurabilityError](err)
	if !ok || published.Path != path || !errors.Is(err, failure) {
		t.Fatalf("post-rename failure lost its state: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("rename did not happen: %q %v", data, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary file left behind: %v %v", entries, err)
	}
}

func TestAtomicReplaceFailureBeforeRenameKeepsTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	synced := false
	err := writeFileAtomic(path, []byte("new"), 0o600, func(string) error { synced = true; return nil })
	_, published := errors.AsType[*DurabilityError](err)
	if err == nil || published || synced {
		t.Fatalf("pre-rename failure misreported: %v (sync=%v)", err, synced)
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Fatalf("target changed: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary file left behind: %v %v", entries, err)
	}
}

func TestRemoveReportsPostUnlinkSyncFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("directory sync failed")
	err := removeFile(path, func(string) error { return failure })
	published, ok := errors.AsType[*DurabilityError](err)
	if !ok || published.Path != path || !errors.Is(err, failure) {
		t.Fatalf("post-unlink failure lost its state: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unlink did not happen: %v", err)
	}
}

func TestAtomicReadersNeverObservePartialReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	old, next := bytes.Repeat([]byte("a"), 64<<10), bytes.Repeat([]byte("b"), 128<<10)
	if err := WriteFileAtomic(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	failures := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			data, err := os.ReadFile(path)
			if err != nil || (!bytes.Equal(data, old) && !bytes.Equal(data, next)) {
				failures <- errors.New("reader observed a partial or missing file")
				return
			}
			select {
			case <-done:
				return
			default:
			}
		}
	})
	stopped := false
	defer func() {
		if !stopped {
			close(done)
			wg.Wait()
		}
	}()
	for i := 0; i < 50; i++ {
		data := old
		if i%2 == 0 {
			data = next
		}
		if err := WriteFileAtomic(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	close(done)
	wg.Wait()
	stopped = true
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file permissions: %v %v", info, err)
	}
}
