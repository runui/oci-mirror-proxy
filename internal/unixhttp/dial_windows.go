package unixhttp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// PipePath accepts local named pipes, Unix sockets (for explicit test/engine
// endpoints), and legacy filesystem admin paths. Filesystem paths map to stable
// pipe names without creating files or imposing Unix socket path limits.
func PipePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("local IPC path is empty")
	}
	normalized := strings.ReplaceAll(path, "/", `\`)
	if strings.HasPrefix(normalized, `\\`) {
		if !strings.HasPrefix(strings.ToLower(normalized), `\\.\pipe\`) || len(normalized) <= len(`\\.\pipe\`) {
			return "", fmt.Errorf("only local named pipes are supported: %q", path)
		}
		return normalized, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(abs))))
	return fmt.Sprintf(`\\.\pipe\oci-mirror-proxy-%x`, sum[:16]), nil
}

func dialer(path string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		// Keep explicit AF_UNIX endpoints usable (including Docker test fixtures).
		if !strings.HasPrefix(strings.ReplaceAll(path, "/", `\`), `\\`) {
			var d net.Dialer
			if conn, err := d.DialContext(ctx, "unix", path); err == nil {
				return conn, nil
			}
		}
		pipe, err := PipePath(path)
		if err != nil {
			return nil, err
		}
		// GENERIC_WRITE includes FILE_CREATE_PIPE_INSTANCE for named pipes. Only
		// request client I/O rights, so shared service pipes need not grant it.
		access := uint32(windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE) &^ uint32(windows.FILE_APPEND_DATA)
		conn, err := winio.DialPipeAccess(ctx, pipe, access)
		if err != nil {
			return nil, fmt.Errorf("dial local endpoint %s: %w", path, err)
		}
		return conn, nil
	}
}
