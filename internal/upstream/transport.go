package upstream

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/config"
)

const (
	dialTimeout         = 10 * time.Second
	tlsHandshakeTimeout = 10 * time.Second
	idleConnTimeout     = 90 * time.Second
)

// newTransport builds the HTTP transport for one upstream. Each upstream gets its
// own transport, so TLS settings and connection pools never leak between upstreams.
func newTransport(tlsCfg config.TLS, firstByte time.Duration) (*http.Transport, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if tlsCfg.InsecureSkipVerify {
		// Opt-in per upstream. The configuration documentation warns about it.
		tc.InsecureSkipVerify = true //nolint:gosec // explicit, documented, per-upstream opt-in
	}
	if tlsCfg.CAFile != "" {
		pem, err := os.ReadFile(tlsCfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		// Extend the system roots rather than replacing them, so public endpoints keep working.
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA file %s contains no certificates", tlsCfg.CAFile)
		}
		tc.RootCAs = pool
	}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:       tc,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: firstByte,
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       idleConnTimeout,
		// Bytes are forwarded unchanged. Never negotiate or decode gzip, so the
		// Content-Length and digests seen by the client match the upstream exactly.
		DisableCompression: true,
	}, nil
}
