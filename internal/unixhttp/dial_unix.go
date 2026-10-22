//go:build !windows

package unixhttp

import (
	"context"
	"net"
)

func dialer(path string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}
}
