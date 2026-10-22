//go:build !windows

package fsutil

import (
	"fmt"
	"os"
)

func replaceFile(source, destination string) error { return os.Rename(source, destination) }

func syncDirectory(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer d.Close()
	return d.Sync()
}
