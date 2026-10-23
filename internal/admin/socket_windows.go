package admin

import (
	"errors"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/runui/oci-mirror-proxy/internal/unixhttp"
)

// Listen uses a named pipe with a protected DACL. LocalSystem services allow
// local users to manage the service; foreground instances remain private. Unlike
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
	sddl := pipeSecurityDescriptor(user.User.Sid)
	// go-winio also sets FILE_PIPE_REJECT_REMOTE_CLIENTS on server handles.
	ln, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: sddl})
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_PIPE_BUSY) {
		return nil, fmt.Errorf("%w: %s: %v", ErrAlreadyRunning, path, err)
	}
	if err != nil {
		return nil, fmt.Errorf("listen on admin pipe %s: %w", path, err)
	}
	return ln, nil
}

func pipeSecurityDescriptor(owner *windows.SID) string {
	sddl := "D:P(A;;GA;;;" + owner.String() + ")"
	if owner.IsWellKnown(windows.WinLocalSystemSid) {
		// FILE_GENERIC_READ | FILE_GENERIC_WRITE minus FILE_CREATE_PIPE_INSTANCE
		// (the same bit as FILE_APPEND_DATA). Clients may exchange API requests,
		// but must not create server instances, change the DACL or take ownership.
		sddl += "(A;;GA;;;BA)(A;;0x0012019b;;;BU)"
	}
	return sddl
}
