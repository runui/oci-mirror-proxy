package scheduler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdaptiveProbeOffsetReconciliation(t *testing.T) {
	for _, advance := range []int{0, 7, 16, 20} {
		t.Run(fmt.Sprint(advance), func(t *testing.T) {
			old := &trackingBody{Reader: strings.NewReader("old")}
			alternate := &trackingBody{Reader: strings.NewReader("tail")}
			b := &adaptiveBody{body: old, offset: int64(100 + advance), current: "old"}
			r := probeResult{id: "new", offset: 100, sample: []byte("0123456789abcdef"), resp: &http.Response{Body: alternate}, cancel: func() {}}
			adopted := b.adopt(r)
			defer b.Close()
			if adopted != (advance < 16) {
				t.Fatalf("adopted=%v", adopted)
			}
			if adopted {
				got, err := io.ReadAll(b.body)
				if err != nil || !bytes.Equal(got, append(r.sample[advance:], []byte("tail")...)) {
					t.Fatal("offset reconciliation corrupted stream")
				}
			} else if !alternate.closed.Load() || old.closed.Load() {
				t.Fatal("wrong connection closed")
			}
		})
	}
}

type stalledBody struct {
	done chan struct{}
	once sync.Once
}

func (b *stalledBody) Read([]byte) (int, error) { <-b.done; return 0, io.ErrClosedPipe }
func (b *stalledBody) Close() error             { b.once.Do(func() { close(b.done) }); return nil }

func TestAdaptiveRecoversStalledRead(t *testing.T) {
	data := strings.Repeat("x", 1024*1024)
	ctx, cancel := context.WithCancel(t.Context())
	old := &stalledBody{done: make(chan struct{})}
	c := rangeCandidate{"fast", func(context.Context, Request) (*http.Response, error) {
		return &http.Response{StatusCode: 206, ContentLength: int64(len(data)), Header: http.Header{"Content-Range": {"bytes 0-1048575/1048576"}}, Body: io.NopCloser(strings.NewReader(data))}, nil
	}}
	b := &adaptiveBody{ctx: ctx, cancel: cancel, body: old, total: int64(len(data)), current: "stalled", since: time.Now(), tried: map[string]bool{}, candidates: []Candidate{c}, policy: Adaptive{Window: 10 * time.Millisecond, MinBytesPerSecond: 1024, ProbeTimeout: time.Second, MaxSwitches: 1}}
	defer b.Close()
	result := make(chan error, 1)
	go func() {
		got, err := io.ReadAll(b)
		if string(got) != data && err == nil {
			err = io.ErrUnexpectedEOF
		}
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stalled read did not recover")
	}
}

type pacedBody struct {
	remaining int
	reads     atomic.Int64
}

func (b *pacedBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	time.Sleep(2 * time.Millisecond)
	n := min(len(p), 1024, b.remaining)
	clear(p[:n])
	b.remaining -= n
	b.reads.Add(1)
	return n, nil
}
func (b *pacedBody) Close() error { return nil }

func TestAdaptiveProbeDoesNotPauseOriginal(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	old := &pacedBody{remaining: 128 * 1024}
	var progress atomic.Int64
	c := rangeCandidate{"unavailable", func(ctx context.Context, _ Request) (*http.Response, error) {
		before := old.reads.Load()
		<-ctx.Done()
		progress.Store(old.reads.Load() - before)
		return nil, ctx.Err()
	}}
	b := &adaptiveBody{ctx: ctx, cancel: cancel, body: old, total: 128 * 1024, current: "original", since: time.Now(), tried: map[string]bool{}, candidates: []Candidate{c}, policy: Adaptive{Window: 10 * time.Millisecond, MinBytesPerSecond: 1024 * 1024, ProbeTimeout: 60 * time.Millisecond, MaxSwitches: 1}}
	defer b.Close()
	got, err := io.ReadAll(b)
	if err != nil || len(got) != 128*1024 {
		t.Fatalf("bytes=%d err=%v", len(got), err)
	}
	if progress.Load() < 5 {
		t.Fatalf("original stalled during probe: reads=%d", progress.Load())
	}
}

func TestAdaptiveCloseUnblocksRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	b := &adaptiveBody{ctx: ctx, cancel: cancel, body: &stalledBody{done: make(chan struct{})}, since: time.Now(), policy: Adaptive{Window: time.Hour}}
	done := make(chan struct{})
	go func() { defer close(done); b.Read(make([]byte, 1024)) }()
	time.Sleep(10 * time.Millisecond)
	b.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock Read")
	}
}

func TestAdaptiveCloseCancelsProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	started, finished := make(chan struct{}), make(chan struct{})
	c := rangeCandidate{"pending", func(ctx context.Context, _ Request) (*http.Response, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return nil, ctx.Err()
	}}
	b := &adaptiveBody{ctx: ctx, cancel: cancel, body: io.NopCloser(strings.NewReader("")), tried: map[string]bool{}, candidates: []Candidate{c}, policy: Adaptive{ProbeTimeout: time.Hour}}
	b.startProbe(0)
	<-started
	b.Close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("probe survived Close")
	}
}
