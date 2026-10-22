//go:build unix

package config

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestLockOpenError(t *testing.T) {
	permission := &os.PathError{Op: "open", Path: "/user/config.yaml.lock", Err: os.ErrPermission}
	err := lockOpenError("/user/config.yaml", permission)
	if !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), "previous sudo invocation") || !strings.Contains(err.Error(), "without deleting the lock file") {
		t.Fatalf("missing permission diagnosis or wrapped cause: %v", err)
	}
	if lockOpenError("/user/config.yaml", os.ErrNotExist) != os.ErrNotExist {
		t.Fatal("unrelated errors must be unchanged")
	}
}
