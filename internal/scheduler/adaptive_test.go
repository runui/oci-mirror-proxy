package scheduler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/ociapi"
)

type rangeCandidate struct {
	id string
	fn func(context.Context, Request) (*Result, error)
}

func (b *adaptiveBody) trySwitch(rate float64) {
	var candidates []Candidate
	for _, c := range b.candidates {
		if !b.tried[c.ID()] {
			candidates = append(candidates, c)
		}
	}
	b.adopt(b.probeCandidates(rate, b.offset, candidates))
}

func (c rangeCandidate) ID() string { return c.id }
func (c rangeCandidate) Attempt(ctx context.Context, r Request) (*Result, error) {
	return c.fn(ctx, r)
}

func TestAdaptiveSwitchIntegrity(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint("invalid_range=", invalid), func(t *testing.T) {
			data := strings.Repeat("0123456789abcdef", 8192)
			old := &trackingBody{Reader: strings.NewReader(data)}
			primary := rangeCandidate{"slow", func(context.Context, Request) (*Result, error) {
				return Stream(&http.Response{StatusCode: 200, ContentLength: int64(len(data)), Body: old}), nil
			}}
			calls := 0
			alternate := rangeCandidate{"fast", func(ctx context.Context, r Request) (*Result, error) {
				calls++
				if r.Header.Get("Range") != "bytes=1024-" {
					t.Fatalf("range=%s", r.Header.Get("Range"))
				}
				start := 1024
				if invalid {
					start++
				}
				return Stream(&http.Response{StatusCode: 206, ContentLength: int64(len(data) - 1024), Header: http.Header{"Content-Range": {fmt.Sprintf("bytes %d-%d/%d", start, len(data)-1, len(data))}}, Body: io.NopCloser(strings.NewReader(data[1024:]))}), nil
			}}
			a := Adaptive{Base: Sequential{}, Window: time.Second, MinBytesPerSecond: 1 << 30, ProbeTimeout: time.Second, MaxSwitches: 2}
			resp, err := a.Execute(t.Context(), Request{Kind: ociapi.KindBlob, Method: "GET"}, []Candidate{primary, alternate})
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			prefix := make([]byte, 1024)
			if _, err = io.ReadFull(resp.Body, prefix); err != nil {
				t.Fatal(err)
			}
			b := resp.Body.(*adaptiveBody)
			b.since = time.Now().Add(-2 * time.Second)
			b.trySwitch(1)
			b.since = time.Now()
			rest, err := io.ReadAll(resp.Body)
			if err != nil || string(prefix)+string(rest) != data {
				t.Fatalf("corrupted stream: %v", err)
			}
			if calls != 1 {
				t.Fatalf("probe calls=%d", calls)
			}
			if old.closed.Load() == invalid {
				t.Fatalf("old stream closed=%v", old.closed.Load())
			}
		})
	}
}

func TestAdaptiveProbeTimeoutKeepsOldStream(t *testing.T) {
	old := &trackingBody{Reader: strings.NewReader("remaining")}
	b := &adaptiveBody{ctx: t.Context(), req: Request{}, candidates: []Candidate{hang("timeout")}, policy: Adaptive{ProbeTimeout: time.Millisecond}, body: old, total: 100, current: "slow", tried: map[string]bool{}}
	b.trySwitch(1)
	if old.closed.Load() || b.current != "slow" {
		t.Fatal("failed probe replaced old stream")
	}
}

func TestAdaptiveLeavesClientRangesUntouched(t *testing.T) {
	body := io.NopCloser(strings.NewReader("partial"))
	c := rangeCandidate{"range", func(context.Context, Request) (*Result, error) {
		return Stream(&http.Response{StatusCode: 206, ContentLength: 7, Body: body}), nil
	}}
	a := Adaptive{Base: Sequential{}}
	resp, err := a.Execute(t.Context(), Request{Kind: ociapi.KindBlob, Method: "GET", Header: http.Header{"Range": {"bytes=10-"}}}, []Candidate{c})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Body != body {
		t.Fatal("client range was wrapped")
	}
}

func TestAdaptiveChoosesOwnershipBeforeAttempt(t *testing.T) {
	for _, tc := range []struct {
		name         string
		count        int
		ranged       bool
		wantRedirect bool
	}{
		{"whole_with_alternate", 2, false, false},
		{"single_candidate", 1, false, true},
		{"client_range", 2, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := rangeCandidate{"source", func(_ context.Context, req Request) (*Result, error) {
				if req.AllowBlobRedirect != tc.wantRedirect {
					t.Fatalf("allow redirect=%v, want %v", req.AllowBlobRedirect, tc.wantRedirect)
				}
				if req.AllowBlobRedirect {
					return BlobRedirect("https://cdn.example/blob"), nil
				}
				return Stream(&http.Response{StatusCode: 200, Body: http.NoBody}), nil
			}}
			req := Request{Kind: ociapi.KindBlob, Method: "GET", Header: make(http.Header), AllowBlobRedirect: true}
			if tc.ranged {
				req.Header.Set("Range", "bytes=5-")
			}
			cands := []Candidate{c}
			if tc.count > 1 {
				cands = append(cands, skipped("alternate"))
			}
			resp, err := (Adaptive{Base: Sequential{}}).Execute(t.Context(), req, cands)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
		})
	}
}
