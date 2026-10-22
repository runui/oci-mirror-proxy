//go:build !windows

package fsutil

import "os"

func ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
