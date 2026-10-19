package scheduler

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/ociapi"
)

func TestAdaptiveHTTPFailover(t *testing.T) {
	data := strings.Repeat("blob-content-", 32768)
	for _, mode := range []string{"valid", "ignored-range", "wrong-total", "truncated", "slow-probe"} {
		t.Run(mode, func(t *testing.T) {
			var requestedOffset int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				start, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.Header.Get("Range"), "bytes="), "-"))
				if err != nil {
					t.Error(err)
					return
				}
				requestedOffset = start
				if mode == "slow-probe" {
					<-r.Context().Done()
					return
				}
				if mode == "ignored-range" {
					io.WriteString(w, data)
					return
				}
				total := len(data)
				if mode == "wrong-total" {
					total++
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, total-1, total))
				w.Header().Set("Content-Length", strconv.Itoa(len(data)-start))
				w.WriteHeader(http.StatusPartialContent)
				if mode == "truncated" {
					io.WriteString(w, "short")
					return
				}
				io.WriteString(w, data[start:])
			}))
			defer server.Close()
			candidate := rangeCandidate{"alternate", func(ctx context.Context, req Request) (*Result, error) {
				if req.AllowBlobRedirect {
					t.Error("adaptive probe allowed a redirect into an open download")
				}
				r, err := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
				if err != nil {
					return nil, err
				}
				r.Header = req.Header.Clone()
				resp, err := server.Client().Do(r)
				return Stream(resp), err
			}}
			old := &trackingBody{Reader: strings.NewReader(data[4096:])}
			b := &adaptiveBody{ctx: t.Context(), req: Request{Kind: ociapi.KindBlob, AllowBlobRedirect: true}, candidates: []Candidate{candidate}, policy: Adaptive{ProbeTimeout: time.Second}, body: old, total: int64(len(data)), offset: 4096, current: "original", tried: map[string]bool{}}
			defer b.Close()
			b.trySwitch(1)
			rest, err := io.ReadAll(b)
			if err != nil {
				t.Fatal(err)
			}
			if sha256.Sum256([]byte(data[:4096]+string(rest))) != sha256.Sum256([]byte(data)) {
				t.Fatal("digest mismatch")
			}
			if mode != "slow-probe" && requestedOffset != 4096 {
				t.Fatalf("offset=%d", requestedOffset)
			}
			if old.closed.Load() != (mode == "valid") {
				t.Fatalf("old closed=%v", old.closed.Load())
			}
		})
	}
}
