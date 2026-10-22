package config

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// Use a stable sidecar, not the YAML file that writers atomically replace.
func Lock(ctx context.Context, main string) (func(), error) {
	f, err := os.OpenFile(main+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return lockFile(ctx, f, windows.LOCKFILE_EXCLUSIVE_LOCK)
}

func ReadLock(ctx context.Context, main string) (func(), error) {
	f, err := os.Open(main + ".lock")
	if errors.Is(err, os.ErrNotExist) {
		return func() {}, nil
	}
	if err != nil {
		return nil, err
	}
	return lockFile(ctx, f, 0)
}

func lockFile(ctx context.Context, f *os.File, flags uint32) (func(), error) {
	var overlapped windows.Overlapped
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		err := windows.LockFileEx(windows.Handle(f.Fd()), flags|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
		if err == nil {
			var once sync.Once
			return func() {
				once.Do(func() {
					_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped)
					_ = f.Close()
				})
			}, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
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
