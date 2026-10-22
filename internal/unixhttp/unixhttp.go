// Package unixhttp builds HTTP clients over local Unix sockets or Windows named
// pipes. Both the admin client and the dockerd integration use it.
package unixhttp

import (
	"net/http"
	"time"
)

// Client returns an HTTP client whose every request is sent to the local endpoint at
// path. The URL host in requests is ignored, so any placeholder host works.
func Client(path string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: dialer(path),
		},
	}
}
