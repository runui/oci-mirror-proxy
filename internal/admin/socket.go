package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/unixhttp"
)

// ErrAlreadyRunning means a live proxy already owns the socket.
var ErrAlreadyRunning = errors.New("another instance is already serving the admin socket")

// Client talks to a running proxy's admin API over local IPC.
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
	return upstreamsPath(alias) + "/" + url.PathEscape(id)
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
