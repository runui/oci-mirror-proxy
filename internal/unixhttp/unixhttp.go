// Package unixhttp builds HTTP clients that talk over a unix domain socket. Both
// the admin client and the dockerd integration use it.
package unixhttp

import (
	"context"
	"net"
	"net/http"
	"time"
)

// Client returns an HTTP client whose every request is sent to the unix socket at
// path. The URL host in requests is ignored, so any placeholder host works.
func Client(path string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		},
	}
}
