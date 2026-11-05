package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/config"
)

// These are characterization tests: error policy changes should update the
// expected results deliberately, rather than accidentally changing failover.
func TestUpstreamFailureMatrix(t *testing.T) {
	tests := []struct {
		name   string
		status []int // zero simulates a first-byte timeout
		want   int
	}{
		{"missing", []int{404}, 404},
		{"all_missing", []int{404, 404}, 404},
		{"unauthorized", []int{401}, 502},
		{"forbidden", []int{403}, 403},
		{"all_forbidden", []int{403, 403}, 403},
		{"rate_limited", []int{429}, 429},
		{"all_rate_limited", []int{429, 429}, 429},
		{"bad_gateway", []int{502}, 502},
		{"unavailable", []int{503}, 503},
		{"all_unavailable", []int{503, 503}, 503},
		{"gateway_timeout", []int{504}, 504},
		{"all_gateway_timeout", []int{504, 504}, 504},
		{"different_server_errors", []int{503, 504}, 502},
		{"all_unauthorized", []int{401, 401}, 502},
		{"transport_timeout", []int{0}, 502},
		{"missing_and_unauthorized", []int{404, 401}, 502},
		{"missing_and_forbidden", []int{404, 403}, 502},
		{"missing_and_rate_limited", []int{404, 429}, 502},
		{"missing_and_unavailable", []int{404, 503}, 502},
		{"missing_and_timeout", []int{404, 0}, 502},
		{"missing_then_success", []int{404, 200}, 200},
		{"unauthorized_then_success", []int{401, 200}, 200},
		{"rate_limited_then_success", []int{429, 200}, 200},
		{"unavailable_then_success", []int{503, 200}, 200},
		{"timeout_then_success", []int{0, 200}, 200},
	}
	for _, racing := range []bool{false, true} {
		mode := "sequential"
		if racing {
			mode = "race"
		}
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, tc := range tests {
				t.Run(mode+"/"+method+"/"+tc.name, func(t *testing.T) {
					var upstreams []config.Upstream
					for i, status := range tc.status {
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							if status == 0 {
								<-r.Context().Done()
								return
							}
							w.Header().Set("Content-Type", "application/json")
							if status == 429 {
								w.Header().Set("Retry-After", "60")
							}
							w.WriteHeader(status)
							if r.Method == http.MethodGet {
								_, _ = io.WriteString(w, `{"errors":[{"code":"UPSTREAM_TEST","message":"upstream response"}]}`)
							}
						}))
						t.Cleanup(server.Close)
						up := upstreamFor(string(rune('a'+i)), server.URL)
						up.Timeouts.FirstByte = 50 * time.Millisecond
						upstreams = append(upstreams, up)
					}
					cfg := baseConfig(upstreams...)
					reg := cfg.Registries["docker.io"]
					reg.Race.Enabled = racing
					reg.Adaptive.Enabled = false
					cfg.Registries["docker.io"] = reg
					svc, _ := newService(t, cfg)
					base := proxyFor(t, svc)
					req, err := http.NewRequestWithContext(t.Context(), method, base+manifestPath, nil)
					if err != nil {
						t.Fatal(err)
					}
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					body, err := io.ReadAll(resp.Body)
					if err != nil {
						t.Fatal(err)
					}
					if resp.StatusCode != tc.want {
						t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, tc.want, body)
					}
					if method == http.MethodHead && len(body) != 0 {
						t.Fatalf("HEAD returned body %q", body)
					}
					if method == http.MethodGet && tc.want != http.StatusOK {
						wantCode := "UNAVAILABLE"
						if tc.want == http.StatusNotFound {
							wantCode = "MANIFEST_UNKNOWN"
						}
						if tc.want == http.StatusForbidden {
							wantCode = "DENIED"
						}
						if tc.want == http.StatusTooManyRequests {
							wantCode = "TOOMANYREQUESTS"
						}
						if got := errorCode(t, body); got != wantCode {
							t.Fatalf("error code = %s, want %s", got, wantCode)
						}
					}
					if tc.want == http.StatusBadGateway && resp.Header.Get("Retry-After") != "" {
						t.Fatal("unexpected forwarding of a discarded upstream Retry-After")
					}
				})
			}
		}
	}
}
