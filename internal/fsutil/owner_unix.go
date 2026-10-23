//go:build !windows

package fsutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

func preserveOwner(path string, tmp *os.File) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing non-regular file %s", path)
	}
	owner := info.Sys().(*syscall.Stat_t)
	current, err := tmp.Stat()
	if err != nil {
		return err
	}
	actual := current.Sys().(*syscall.Stat_t)
	if owner.Uid == actual.Uid && owner.Gid == actual.Gid {
		return nil
	}
	return tmp.Chown(int(owner.Uid), int(owner.Gid))
}
