package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func replaceFile(source, destination string) error {
	from, err := windows.UTF16PtrFromString(longPath(source))
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(longPath(destination))
	if err != nil {
		return err
	}
	// POSIX replacement semantics keep already-open readers on the old inode
	// while new readers open the replacement, without a delete-pending gap.
	// Windows readers (and virus scanners) can briefly hold handles without
	// FILE_SHARE_DELETE. Retry only sharing errors; never unlink the old file.
	deadline := time.Now().Add(time.Second)
	posix := true
	for {
		if posix {
			err = renamePOSIX(source, destination)
			if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
				posix = false
				continue
			}
		} else {
			err = windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		}
		if err == nil {
			return nil
		}
		if (!errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_ACCESS_DENIED)) || time.Now().After(deadline) {
			return &os.LinkError{Op: "replace", Old: source, New: destination, Err: err}
		}
		time.Sleep(time.Millisecond)
	}
}

func longPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil || strings.HasPrefix(abs, `\\?\`) {
		return path
	}
	if strings.HasPrefix(abs, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(abs, `\\`)
	}
	return `\\?\` + abs
}

func renamePOSIX(source, destination string) error {
	abs, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	name, err := windows.UTF16FromString(filepath.Base(abs))
	if err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(longPath(source))
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(from, windows.DELETE|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	dir, err := windows.UTF16PtrFromString(longPath(filepath.Dir(abs)))
	if err != nil {
		return err
	}
	root, err := windows.CreateFile(dir, windows.FILE_LIST_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(root)
	type renameInformation struct {
		Flags  uint32
		Root   windows.Handle
		Length uint32
		Name   [1]uint16
	}
	var info renameInformation
	size := int(unsafe.Offsetof(info.Name)) + (len(name)-1)*2
	buffer := make([]byte, size)
	header := (*renameInformation)(unsafe.Pointer(&buffer[0]))
	header.Flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	header.Root = root
	header.Length = uint32((len(name) - 1) * 2)
	copy(unsafe.Slice(&header.Name[0], len(name)-1), name[:len(name)-1])
	return windows.SetFileInformationByHandle(h, windows.FileRenameInfoEx, &buffer[0], uint32(size))
}

// FlushFileBuffers on a directory opened by os.Open is not supported on
// Windows. File contents are flushed before replacement and MoveFileEx requests
// write-through; do not claim Unix directory-fsync durability on this platform.
func syncDirectory(string) error { return nil }
