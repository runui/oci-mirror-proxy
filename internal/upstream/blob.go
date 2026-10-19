package upstream

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/scheduler"
)

const delegationTimeout = 5 * time.Second

// blobRequest discovers a download location without changing the shared client
// used by metadata and token exchange. A direct 200 remains a stream: successful
// authenticated access does not imply that the client can fetch the same URL.
func (e *Endpoint) blobRequest(original *http.Request, req scheduler.Request) (*scheduler.Result, error) {
	if e.spec.TLS.InsecureSkipVerify || e.spec.TLS.CAFile != "" {
		scheduler.DebugDelivery(original.Context(), "relay", "proxy_tls_trust")
		return e.relay(original) // client trust cannot be inferred from proxy-only TLS settings
	}
	client := *e.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(original)
	if err != nil {
		return scheduler.Stream(resp), err
	}
	if !downloadRedirect(resp.StatusCode) {
		if resp.StatusCode/100 == 2 {
			scheduler.DebugDelivery(original.Context(), "relay", "upstream_stream")
		}
		return scheduler.Stream(resp), nil
	}
	location, err := resp.Location()
	_ = resp.Body.Close() // never drain an arbitrary redirect body to reuse a connection
	if err == nil {
		if target := e.checkDownload(original.Context(), location, req); target != "" {
			scheduler.DebugDelivery(original.Context(), "redirect", "verified_download")
			return scheduler.BlobRedirect(target), nil
		}
	}
	scheduler.DebugDelivery(original.Context(), "relay", "download_not_delegable")
	// No downstream headers have been sent yet. Reissue through the original
	// authenticated client, retaining its established redirect/auth semantics.
	// Once a 307 has been handed off, this fallback is no longer possible.
	return e.relay(original.Clone(original.Context()))
}

func (e *Endpoint) relay(req *http.Request) (*scheduler.Result, error) {
	resp, err := e.client.Do(req)
	return scheduler.Stream(resp), err
}

func downloadRedirect(code int) bool {
	return code == 301 || code == 302 || code == 303 || code == 307 || code == 308
}

// checkDownload performs an independent GET, not HEAD: signed URLs and access
// policies can be method-specific. Only headers are examined; downloading a
// layer to validate it would defeat delegation. No proxy auth/cookies are copied.
func (e *Endpoint) checkDownload(ctx context.Context, location *url.URL, req scheduler.Request) string {
	ctx, cancel := context.WithTimeout(ctx, delegationTimeout)
	defer cancel()
	validate := e.delegateURL
	if validate == nil {
		validate = publicDownloadURL
	}
	clientHost := req.ClientHost
	if host, _, err := net.SplitHostPort(clientHost); err == nil {
		clientHost = host
	}
	check := func(ctx context.Context, u *url.URL) bool {
		if u == nil || clientHost != "" && strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), strings.TrimSuffix(clientHost, ".")) {
			return false
		}
		return validate(ctx, u)
	}
	if !check(ctx, location) {
		return ""
	}
	probe, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
	if err != nil {
		return ""
	}
	// req.Header is the frontend's allowlist, but internal callers may supply
	// more. Copy explicitly rather than relying on a caller to strip secrets.
	for _, key := range []string{"Range", "Accept", "User-Agent"} {
		if values := req.Header.Values(key); len(values) != 0 {
			probe.Header[key] = append([]string(nil), values...)
		}
	}
	client := http.Client{Transport: e.client.Transport, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !check(ctx, next.URL) {
			return fmt.Errorf("download delegation redirect rejected")
		}
		for _, prev := range via {
			if prev.URL.String() == next.URL.String() {
				return fmt.Errorf("download delegation redirect loop")
			}
		}
		return nil
	}}
	resp, err := client.Do(probe)
	if err != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return ""
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "" || !validDownloadResponse(req.Header.Get("Range"), resp) {
		return ""
	}
	// Preserve escaped paths and the exact signed query. Parsing/re-encoding
	// query parameters can invalidate signatures. Never log this capability URL.
	return resp.Request.URL.String()
}

func validDownloadResponse(ranged string, resp *http.Response) bool {
	if ranged == "" {
		return resp.StatusCode == http.StatusOK
	}
	// Conservative single-range validation. Suffix and multipart ranges can
	// still be relayed; delegation need not support every valid HTTP request.
	spec, ok := strings.CutPrefix(ranged, "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return false
	}
	first, last, ok := strings.Cut(spec, "-")
	start, err := strconv.ParseInt(first, 10, 64)
	if !ok || err != nil || start < 0 || resp.StatusCode != http.StatusPartialContent {
		return false
	}
	var a, b, total int64
	value := resp.Header.Get("Content-Range")
	_, err = fmt.Sscanf(value, "bytes %d-%d/%d", &a, &b, &total)
	if err != nil || value != fmt.Sprintf("bytes %d-%d/%d", a, b, total) || a != start || b < a || total <= b || resp.ContentLength != b-a+1 {
		return false
	}
	if last == "" {
		return b == total-1
	}
	end, err := strconv.ParseInt(last, 10, 64)
	return err == nil && end >= start && b == min(end, total-1)
}

// publicDownloadURL excludes destinations meaningful only inside a client or a
// private network. DNS checks are conservative, not a proof of client reachability
// nor a DNS-rebinding defense; no proxy credentials are sent even if DNS changes.
func publicDownloadURL(ctx context.Context, u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if port := u.Port(); port != "" && port != "443" {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return false
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, addr := range ips {
		ip := addr.IP
		// IsGlobalUnicast includes shared address space (100.64/10), which is
		// not a public CDN destination and can expose client-local services.
		shared := ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xc0 == 64
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || shared {
			return false
		}
	}
	return true
}
