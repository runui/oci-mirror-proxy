package scheduler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/ociapi"
)

func BenchmarkAdaptiveDownload(b *testing.B) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 65536)
	for _, enabled := range []bool{false, true} {
		name := "baseline"
		if enabled {
			name = "adaptive"
		}
		b.Run(name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			candidate := rangeCandidate{"source", func(context.Context, Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data))}, nil
			}}
			var strategy Strategy = Sequential{}
			if enabled {
				strategy = Adaptive{Base: strategy, Window: time.Hour, MinBytesPerSecond: 1, ProbeTimeout: time.Second, MaxSwitches: 1}
			}
			buf := make([]byte, 32*1024)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resp, err := strategy.Execute(b.Context(), Request{Kind: ociapi.KindBlob, Method: "GET"}, []Candidate{candidate})
				if err != nil {
					b.Fatal(err)
				}
				// Hide WriterTo to measure the same read/copy path for both modes.
				n, err := io.CopyBuffer(io.Discard, struct{ io.Reader }{resp.Body}, buf)
				resp.Body.Close()
				if err != nil || n != int64(len(data)) {
					b.Fatalf("bytes=%d err=%v", n, err)
				}
			}
		})
	}
}
