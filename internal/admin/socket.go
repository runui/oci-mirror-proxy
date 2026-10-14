package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/unixhttp"
)

// maxSocketPath is the portable limit for unix socket paths, which
// macOS enforces at 104 bytes including the terminating NUL.
const maxSocketPath = 103

// ErrAlreadyRunning means a live proxy already owns the socket.
var ErrAlreadyRunning = errors.New("another instance is already serving the admin socket")

// Listen creates the admin socket. The directory is private to its owner, and the
// socket itself is 0600. A stale socket left by a crashed process is replaced,
// but a live one is never removed.
func Listen(path string) (net.Listener, error) {
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("admin socket path is %d bytes, the limit is %d: %s", len(path), maxSocketPath, path)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
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
	return ln, nil
}

// Client talks to a running proxy's admin API over its unix socket.
type Client struct {
	http *http.Client
}

// NewClient returns a client for the admin socket at path.
func NewClient(path string) *Client {
	return &Client{http: unixhttp.Client(path, 30*time.Second)}
}

// Status fetches GET /v1/status.
func (c *Client) Status() (Status, error) {
	var out Status
	err := c.do(http.MethodGet, "/v1/status", nil, &out)
	return out, err
}

// ListUpstreams fetches the upstreams of one registry.
func (c *Client) ListUpstreams(alias string) ([]UpstreamSpec, error) {
	var out []UpstreamSpec
	err := c.do(http.MethodGet, upstreamsPath(alias), nil, &out)
	return out, err
}

// AddUpstream adds an upstream to a registry.
func (c *Client) AddUpstream(alias string, spec UpstreamSpec) error {
	return c.do(http.MethodPost, upstreamsPath(alias), spec, nil)
}

// SetUpstreamDisabled enables or disables an upstream.
func (c *Client) SetUpstreamDisabled(alias, id string, disabled bool) error {
	return c.do(http.MethodPatch, upstreamPath(alias, id), map[string]bool{"disabled": disabled}, nil)
}

// RemoveUpstream deletes an upstream.
func (c *Client) RemoveUpstream(alias, id string) error {
	return c.do(http.MethodDelete, upstreamPath(alias, id), nil, nil)
}

// Reload asks the proxy to re-read its configuration file.
func (c *Client) Reload() error {
	return c.do(http.MethodPost, "/v1/reload", nil, nil)
}

func upstreamsPath(alias string) string {
	return "/v1/registries/" + alias + "/upstreams"
}

func upstreamPath(alias, id string) string {
	return upstreamsPath(alias) + "/" + id
}

func (c *Client) do(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, "http://admin"+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("contact proxy admin socket: %w (is the proxy running?)", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var e errorResponse
		_ = json.NewDecoder(io.LimitReader(resp.Body, maxRequestBody)).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("proxy rejected request: %s", e.Error)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
