package upstream

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/config"
)

type lifetimeTransport struct {
	closed atomic.Int32
	body   *lifetimeBody
}

func (tr *lifetimeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: tr.body}, nil
}

func (tr *lifetimeTransport) CloseIdleConnections() { tr.closed.Add(1) }

type lifetimeBody struct {
	io.Reader
	closed atomic.Int32
}

func (b *lifetimeBody) Close() error { b.closed.Add(1); return nil }

func TestAttemptBodyPinsRetiredEndpointAndClosesOnce(t *testing.T) {
	e := newTestEndpoint(t, config.Upstream{URL: "https://mirror.example", Auth: config.Auth{Type: config.AuthNone}}, 3)
	body := &lifetimeBody{Reader: strings.NewReader("payload")}
	transport := &lifetimeTransport{body: body}
	e.client.Transport = transport
	e.Retain() // Simulate the generation's ownership.
	resp, err := getManifest(t, e, "library/test")
	if err != nil {
		t.Fatal(err)
	}
	e.Release() // The generation retires before this response body closes.
	if e.refs.Load() != 1 || transport.closed.Load() != 0 {
		t.Fatalf("retired live response: refs=%d closes=%d", e.refs.Load(), transport.closed.Load())
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil || string(data) != "payload" {
		t.Fatalf("live response changed: %q %v", data, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			if err := resp.Body.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if e.refs.Load() != 0 || transport.closed.Load() != 1 || body.closed.Load() != 1 {
		t.Fatalf("body cleanup is not exactly once: refs=%d transport=%d body=%d", e.refs.Load(), transport.closed.Load(), body.closed.Load())
	}
}

func TestForkOwnershipSurvivesOldGenerationAndFailedBuild(t *testing.T) {
	reg := config.Registry{
		Breaker:   config.Breaker{Threshold: 3, Cooldown: time.Minute},
		Upstreams: []config.Upstream{{URL: "https://mirror.example", Auth: config.Auth{Type: config.AuthNone}}},
	}
	first, err := NewPool(Env{}).Fork(t.Context(), reg)
	if err != nil {
		t.Fatal(err)
	}
	e := first.endpoints[0]
	second, err := first.Fork(t.Context(), reg)
	if err != nil {
		t.Fatal(err)
	}
	if second.endpoints[0] != e || e.refs.Load() != 2 {
		t.Fatalf("unchanged endpoint was not shared: refs=%d", e.refs.Load())
	}
	first.Release()
	if e.refs.Load() != 1 {
		t.Fatalf("old generation released new ownership: %d", e.refs.Load())
	}
	bad := reg
	bad.Upstreams = append([]config.Upstream(nil), reg.Upstreams...)
	bad.Upstreams = append(bad.Upstreams, config.Upstream{URL: "https://bad.example", Auth: config.Auth{Type: "unsupported"}})
	if _, err := second.Fork(t.Context(), bad); err == nil {
		t.Fatal("accepted failed endpoint construction")
	}
	if e.refs.Load() != 1 {
		t.Fatalf("failed fork leaked or dropped ownership: %d", e.refs.Load())
	}
	second.Release()
	if e.refs.Load() != 0 {
		t.Fatalf("final generation did not release endpoint: %d", e.refs.Load())
	}
}
