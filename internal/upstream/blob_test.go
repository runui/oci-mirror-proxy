package upstream

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/frontend"
	"github.com/runui/oci-mirror-proxy/internal/middleware"
	"github.com/runui/oci-mirror-proxy/internal/ociapi"
	"github.com/runui/oci-mirror-proxy/internal/route"
	"github.com/runui/oci-mirror-proxy/internal/scheduler"
)

func delegationRequest() scheduler.Request {
	return scheduler.Request{Kind: ociapi.KindBlob, Method: http.MethodGet, Repo: "library/test",
		Path: "/v2/library/test/blobs/sha256:" + strings.Repeat("a", 64), Header: make(http.Header), AllowBlobRedirect: true}
}

// Local TLS servers stand in for public CDNs. Only tests bypass destination
// validation; their trusted Transport is also used to emulate client trust.
func delegationEndpoint(t *testing.T, registry, cdn *httptest.Server) *Endpoint {
	t.Helper()
	e := newTestEndpoint(t, config.Upstream{ID: "test", URL: registry.URL, Timeouts: config.Timeouts{FirstByte: time.Second}}, 3)
	e.client.Transport = cdn.Client().Transport
	e.delegateURL = func(context.Context, *url.URL) bool { return true }
	t.Cleanup(e.Close)
	return e
}

func TestBlobDelegationChecksWithoutCredentialsAndClosesStream(t *testing.T) {
	var bytesSent atomic.Int64
	stopped := make(chan struct{}, 1)
	cdn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { stopped <- struct{}{} }()
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("download check used the wrong method or leaked credentials")
		}
		if r.URL.RawQuery != "sig=a%2Fb&x=one+two" {
			t.Error("signed query was rewritten")
		}
		w.Header().Set("Content-Length", "1073741824")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		block := make([]byte, 16*1024)
		for {
			n, err := w.Write(block)
			bytesSent.Add(int64(n))
			if err != nil {
				return
			}
		}
	}))
	defer cdn.Close()
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", cdn.URL+"/object?sig=a%2Fb&x=one+two")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer registry.Close()
	e := delegationEndpoint(t, registry, cdn)
	req := delegationRequest()
	req.Header.Set("Authorization", "Bearer private")
	req.Header.Set("Cookie", "session=private")
	resp, err := e.Attempt(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.RedirectLocation() != cdn.URL+"/object?sig=a%2Fb&x=one+two" || resp.Body != http.NoBody {
		t.Fatalf("not a normalized handoff: %+v", resp)
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("delegation left the check downloading in the background")
	}
	if bytesSent.Load() >= 1073741824 {
		t.Fatal("check downloaded the whole layer")
	}
}

func TestBlobDelegationFallbackAndRequiredRelay(t *testing.T) {
	for _, mode := range []string{"requires_auth", "required_relay", "private_tls", "direct_200", "denied_destination", "bad_range"} {
		t.Run(mode, func(t *testing.T) {
			var anonymous, authorized atomic.Int32
			cdn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "Bearer private" {
					authorized.Add(1)
				} else {
					anonymous.Add(1)
					if mode == "requires_auth" {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
				}
				io.WriteString(w, "payload")
			}))
			defer cdn.Close()
			registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "direct_200" {
					io.WriteString(w, "payload")
					return
				}
				// Both servers use the same hostname, so Go's original relay
				// client retains Authorization on this redirect, unlike the check.
				w.Header().Set("Location", cdn.URL+"/blob")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}))
			defer registry.Close()
			e := delegationEndpoint(t, registry, cdn)
			req := delegationRequest()
			req.Header.Set("Authorization", "Bearer private")
			switch mode {
			case "required_relay":
				req.AllowBlobRedirect = false
			case "private_tls":
				e.spec.TLS.CAFile = "proxy-only-ca.pem"
			case "denied_destination":
				e.delegateURL = func(context.Context, *url.URL) bool { return false }
			case "bad_range":
				req.Header.Set("Range", "bytes=1-")
			}
			resp, err := e.Attempt(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || string(body) != "payload" || resp.RedirectLocation() != "" {
				t.Fatalf("fallback failed: body=%q err=%v redirect=%q", body, err, resp.RedirectLocation())
			}
			if (mode == "required_relay" || mode == "private_tls" || mode == "denied_destination") && anonymous.Load() != 0 {
				t.Fatal("ineligible request performed a delegation check")
			}
		})
	}
}

func TestBlobDelegationRelativeRedirectChain(t *testing.T) {
	var base string
	cdn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/first":
			w.Header().Set("Location", "/last?sig=unchanged%2F")
			w.WriteHeader(http.StatusFound)
		default:
			w.Header().Set("Content-Length", "3")
			io.WriteString(w, "abc")
		}
	}))
	defer cdn.Close()
	base = cdn.URL
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", base+"/first")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer registry.Close()
	e := delegationEndpoint(t, registry, cdn)
	resp, err := e.Attempt(t.Context(), delegationRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.RedirectLocation() != base+"/last?sig=unchanged%2F" {
		t.Fatal("relative redirect did not resolve correctly")
	}
}

func TestBlobDelegationRejectsRedirectLoopAndCancellation(t *testing.T) {
	for _, mode := range []string{"loop", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{}, 1)
			cdn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "loop" {
					w.Header().Set("Location", "/again")
					w.WriteHeader(http.StatusFound)
					return
				}
				started <- struct{}{}
				<-r.Context().Done()
			}))
			defer cdn.Close()
			registry := httptest.NewServer(http.NotFoundHandler())
			defer registry.Close()
			e := delegationEndpoint(t, registry, cdn)
			u, _ := url.Parse(cdn.URL + "/again")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan string, 1)
			go func() { done <- e.checkDownload(ctx, u, delegationRequest()) }()
			if mode == "cancel" {
				<-started
				cancel()
			}
			select {
			case result := <-done:
				if result != "" {
					t.Fatal("invalid download was delegated")
				}
			case <-time.After(time.Second):
				t.Fatal("loop or cancellation did not release the check")
			}
		})
	}
}

func TestBlobDelegationRejectsClientFacingHost(t *testing.T) {
	registry := httptest.NewServer(http.NotFoundHandler())
	defer registry.Close()
	e := newTestEndpoint(t, config.Upstream{URL: registry.URL}, 3)
	var validated bool
	e.delegateURL = func(context.Context, *url.URL) bool { validated = true; return true }
	req := delegationRequest()
	req.ClientHost = "proxy.example:24817"
	u, _ := url.Parse("https://proxy.example/blob")
	if e.checkDownload(t.Context(), u, req) != "" || validated {
		t.Fatal("self redirect reached destination validation/network")
	}
}

func TestPublicDownloadURLRejectsUnsafeTargets(t *testing.T) {
	for _, raw := range []string{"http://example.com/blob", "https://user:pass@example.com/blob", "https://localhost/blob", "https://127.0.0.1/blob", "https://[::1]/blob", "https://192.168.2.28/blob", "https://169.254.169.254/blob", "https://100.64.0.1/blob", "https://example.com:8443/blob", "https://example.com/blob#fragment", "file:///blob"} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if publicDownloadURL(t.Context(), u) {
			t.Errorf("unsafe download allowed: %s", raw)
		}
	}
}

func TestValidDownloadResponse(t *testing.T) {
	for _, tc := range []struct {
		rangeValue, contentRange string
		status                   int
		length                   int64
		want                     bool
	}{
		{"", "", 200, 10, true}, {"", "", 206, 10, false},
		{"bytes=5-", "bytes 5-9/10", 206, 5, true},
		{"bytes=5-7", "bytes 5-7/10", 206, 3, true},
		{"bytes=5-99", "bytes 5-9/10", 206, 5, true},
		{"bytes=5-", "bytes 4-9/10", 206, 6, false},
		{"bytes=5-", "bytes 5-9/10", 206, 4, false},
		{"bytes=5-", "", 200, 10, false},
		{"bytes=-5", "bytes 5-9/10", 206, 5, false},
		{"bytes=0-1,4-5", "", 206, 4, false},
	} {
		resp := &http.Response{StatusCode: tc.status, ContentLength: tc.length, Header: make(http.Header)}
		resp.Header.Set("Content-Range", tc.contentRange)
		if got := validDownloadResponse(tc.rangeValue, resp); got != tc.want {
			t.Errorf("%+v: got %v", tc, got)
		}
	}
}

func TestBlobDeliveryPipeline(t *testing.T) {
	for _, adaptive := range []bool{false, true} {
		t.Run(fmt.Sprint("adaptive=", adaptive), func(t *testing.T) {
			data := strings.Repeat("0123456789abcdef", 32768)
			cdn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.ServeContent(w, r, "", time.Time{}, strings.NewReader(data))
			}))
			defer cdn.Close()
			registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", cdn.URL+"/blob?sig=private%2F")
				w.WriteHeader(307)
			}))
			defer registry.Close()
			e := delegationEndpoint(t, registry, cdn)
			var strategy scheduler.Strategy = scheduler.Sequential{}
			if adaptive {
				strategy = scheduler.Adaptive{Base: strategy, Window: time.Hour, MaxSwitches: 1}
			}
			base := route.DispatcherFunc(func(ctx context.Context, req scheduler.Request) (*scheduler.Result, error) {
				return strategy.Execute(ctx, req, []scheduler.Candidate{e, e})
			})
			table := route.NewTable()
			table.Swap(map[string]route.Dispatcher{"docker.io": middleware.Resume(3)(base)})
			proxy := httptest.NewServer(frontend.New(table))
			defer proxy.Close()
			client := *cdn.Client()
			var redirects int
			client.CheckRedirect = func(*http.Request, []*http.Request) error { redirects++; return nil }
			// Distinct client-facing hostname: production deliberately refuses
			// handoffs back to its own host, even on another port.
			resp, err := client.Get(strings.Replace(proxy.URL, "127.0.0.1", "localhost", 1) + delegationRequest().Path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			got, err := io.ReadAll(resp.Body)
			if err != nil || sha256.Sum256(got) != sha256.Sum256([]byte(data)) {
				t.Fatalf("pipeline corrupted data: %v", err)
			}
			if (redirects == 1) == adaptive {
				t.Fatalf("adaptive=%v, client redirects=%d", adaptive, redirects)
			}
		})
	}
}
