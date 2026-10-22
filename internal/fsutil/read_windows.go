package fsutil

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// ReadFile permits atomic replacement while this reader keeps its old handle.
// os.ReadFile does not request FILE_SHARE_DELETE on Windows.
func ReadFile(path string) ([]byte, error) {
	name, err := windows.UTF16PtrFromString(longPath(path))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	return io.ReadAll(f)
}
