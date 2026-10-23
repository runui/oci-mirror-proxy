// Package fsutil holds small filesystem helpers shared by the config and
// daemon integration code.
package fsutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// DurabilityError means the change is already visible to readers, but syncing
// its directory failed. Callers must not treat it as a pre-publication failure:
// a retry, rollback, or reload must account for the changed path on disk.
type DurabilityError struct {
	Path string
	Err  error
}

func (e *DurabilityError) Error() string {
	return fmt.Sprintf("%s changed, but directory durability is unconfirmed: %v", e.Path, e.Err)
}

func (e *DurabilityError) Unwrap() error { return e.Err }

// WriteFileAtomic replaces path with data. The content is written to a temporary
// file in the same directory, synced, and renamed over the target, so readers
// observe either the old file or the new one, never a partial write. A directory
// sync failure after rename returns *DurabilityError, not an unchanged-file claim.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	return writeFileAtomic(path, data, perm, syncDirectory)
}

// WriteConfigAtomic preserves the existing owner's identity before publication.
// Symlinks are refused rather than replaced with an unrelated regular file.
func WriteConfigAtomic(path string, data []byte, perm fs.FileMode) error {
	return writeAtomic(path, data, perm, syncDirectory, preserveOwner)
}

func writeFileAtomic(path string, data []byte, perm fs.FileMode, syncDir func(string) error) error {
	return writeAtomic(path, data, perm, syncDir, func(string, *os.File) error { return nil })
}

func writeAtomic(path string, data []byte, perm fs.FileMode, syncDir func(string) error, prepare func(string, *os.File) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if err := prepare(path, tmp); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := writeAndSync(tmp, data, perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := replaceFile(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	// Persist the directory entry as well as file contents. A completed rename
	// is atomic to readers; syncing its directory also makes it crash-durable.
	if err := syncDir(dir); err != nil {
		return &DurabilityError{Path: path, Err: err}
	}
	return nil
}

// RemoveFile unlinks path and syncs its containing directory. As with atomic
// replacement, a sync failure is reported separately from an unlink failure.
func RemoveFile(path string) error {
	return removeFile(path, syncDirectory)
}

func removeFile(path string, syncDir func(string) error) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return &DurabilityError{Path: path, Err: err}
	}
	return nil
}

func writeAndSync(f *os.File, data []byte, perm fs.FileMode) error {
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	// CreateTemp uses 0600; apply the requested mode before the rename.
	if err := f.Chmod(perm); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	return nil
}

// ReadOptional reads path. A missing file is not an error: it returns nil data
// and exists == false.
func ReadOptional(path string) (data []byte, exists bool, err error) {
	data, err = ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	return data, true, nil
}
