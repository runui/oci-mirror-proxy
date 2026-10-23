//go:build !windows

package admin

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// macOS enforces 104 bytes including the terminating NUL.
const maxSocketPath = 103

// Listen creates a Unix socket, refusing to replace a live listener. Standard
// root-owned system endpoints are available to all local users; other endpoints
// remain private to the service account.
func Listen(path string) (net.Listener, error) {
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("admin socket path is %d bytes, the limit is %d: %s", len(path), maxSocketPath, path)
	}
	dir := filepath.Dir(path)
	shared := isSystemAdminSocket(path, os.Geteuid())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}
	if shared {
		if err := validateSystemSocketDirectory(dir); err != nil {
			return nil, err
		}
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("restrict socket directory: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		conn, dialErr := net.DialTimeout("unix", path, time.Second)
		if dialErr == nil {
			_ = conn.Close()
			return nil, ErrAlreadyRunning
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on admin socket: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("restrict admin socket: %w", err)
	}
	if shared {
		if err := os.Chmod(path, 0o666); err != nil {
			_ = ln.Close()
			return nil, fmt.Errorf("allow local admin socket access: %w", err)
		}
		// Publish access last, after the socket is ready. Local users can connect
		// but cannot replace the root-owned directory or its contents.
		if err := os.Chmod(dir, 0o711); err != nil {
			_ = ln.Close()
			return nil, fmt.Errorf("allow local admin directory traversal: %w", err)
		}
	}
	return ln, nil
}

func isSystemAdminSocket(path string, uid int) bool {
	return uid == 0 && (path == "/run/oci-mirror-proxy/admin.sock" || path == "/var/run/oci-mirror-proxy/admin.sock")
}

func validateSystemSocketDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("system admin socket directory must not be a symlink: %s", dir)
	}
	// Resolve the root-owned /var alias used by macOS before checking parents.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	for parent := resolved; ; parent = filepath.Dir(parent) {
		info, err := os.Stat(parent)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		// macOS ships /private/var/run as root:daemon 0775. This trusted
		// system parent is distinct from our private, root-only writable child.
		trustedRuntime := ok && runtime.GOOS == "darwin" && parent == "/private/var/run" && stat.Gid == 1
		if !ok || stat.Uid != 0 || info.Mode().Perm()&0o002 != 0 || (info.Mode().Perm()&0o020 != 0 && !trustedRuntime) {
			return fmt.Errorf("system admin socket parent must be root-owned and not group/world writable: %s", parent)
		}
		if parent == filepath.Dir(parent) {
			return nil
		}
	}
}
