package config

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCollectionLockAcrossProcesses(t *testing.T) {
	if main := os.Getenv("OMP_LOCK_TEST_CHILD"); main != "" {
		ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
		defer cancel()
		_, err := Lock(ctx, main)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("writer did not wait for another process: %v", err)
		}
		return
	}
	main := filepath.Join(t.TempDir(), "config.yaml")
	unlock, err := Lock(t.Context(), main)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCollectionLockAcrossProcesses$")
	cmd.Env = append(os.Environ(), "OMP_LOCK_TEST_CHILD="+main)
	out, childErr := cmd.CombinedOutput()
	unlock()
	if childErr != nil {
		t.Fatalf("child: %v\n%s", childErr, out)
	}
	unlock, err = Lock(t.Context(), main)
	if err != nil {
		t.Fatalf("released lock not reusable: %v", err)
	}
	unlock()
}

func TestSharedCollectionReadersExcludeWriter(t *testing.T) {
	main := filepath.Join(t.TempDir(), "config.yaml")
	unlock, err := Lock(t.Context(), main)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	first, err := ReadLock(t.Context(), main)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	second, err := ReadLock(t.Context(), main)
	if err != nil {
		t.Fatal(err)
	}
	defer second()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	if _, err := Lock(ctx, main); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("writer did not wait for readers: %v", err)
	}
}
