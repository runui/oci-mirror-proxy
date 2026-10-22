package admin

import (
	"errors"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/runui/oci-mirror-proxy/internal/unixhttp"
)

// Listen uses a named pipe with a protected DACL granting access only to the
// current user (and elevated administrators for a LocalSystem process). Unlike
// AF_UNIX sockets, pipes do not depend on short filesystem paths or ineffective
// Unix chmod permission bits on Windows.
func Listen(path string) (net.Listener, error) {
	pipe, err := unixhttp.PipePath(path)
	if err != nil {
		return nil, err
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	sddl := "D:P(A;;GA;;;" + user.User.Sid.String() + ")"
	if user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		// The system service is managed by elevated administrators, not by other
		// interactive users. Foreground instances retain their user-only ACL.
		sddl += "(A;;GA;;;BA)"
	}
	ln, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: sddl})
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_PIPE_BUSY) {
		return nil, fmt.Errorf("%w: %s: %v", ErrAlreadyRunning, path, err)
	}
	if err != nil {
		return nil, fmt.Errorf("listen on admin pipe %s: %w", path, err)
	}
	return ln, nil
}
