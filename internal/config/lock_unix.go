//go:build unix

package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// Lock uses a stable sidecar inode, never the atomically replaced YAML inode.
// Do not unlink the lock on release: another process may already be waiting on it.
func Lock(ctx context.Context, main string) (func(), error) {
	f, err := os.OpenFile(main+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, lockOpenError(main, err)
	}
	return lockFile(ctx, f, syscall.LOCK_EX)
}

// ReadLock never creates files and works with a read-only configuration mount.
// Writers create the sidecar before changing any document in the collection.
func ReadLock(ctx context.Context, main string) (func(), error) {
	f, err := os.Open(main + ".lock")
	if errors.Is(err, os.ErrNotExist) {
		return func() {}, nil
	}
	if err != nil {
		return nil, lockOpenError(main, err)
	}
	return lockFile(ctx, f, syscall.LOCK_SH)
}

func lockOpenError(main string, err error) error {
	if errors.Is(err, os.ErrPermission) {
		// Keep the underlying PathError for callers. Do not unlink the sidecar or
		// relax its permissions: both would undermine configuration isolation.
		return fmt.Errorf("%w; check ownership and permissions of the configuration directory and %s.lock (a previous sudo invocation may have created root-owned files); repair ownership only for your own user configuration, without deleting the lock file", err, main)
	}
	return err
}

func lockFile(ctx context.Context, f *os.File, mode int) (func(), error) {
	for {
		err := syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
