package scheduler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/ociapi"
)

// Adaptive retains the current stream until an alternate proves faster at the
// exact downstream offset. It never changes routing for metadata or client ranges.
type Adaptive struct {
	Base              Strategy
	Window            time.Duration
	MinBytesPerSecond int64
	ProbeTimeout      time.Duration
	MaxSwitches       int
	Logger            *slog.Logger
}

func (a Adaptive) Execute(ctx context.Context, req Request, cands []Candidate) (*Result, error) {
	// Decide ownership before contacting an endpoint. Client ranges cannot be
	// switched by Adaptive, and a lone candidate offers no alternate to probe.
	if req.Kind == ociapi.KindBlob && req.Method == http.MethodGet && req.Header.Get("Range") == "" && len(cands) > 1 {
		req.AllowBlobRedirect = false
		DebugDelivery(ctx, "relay", "adaptive_ownership")
	}
	var selected string
	tracked := make([]Candidate, len(cands))
	for i, c := range cands {
		tracked[i] = &trackedCandidate{Candidate: c}
	}
	resp, err := a.Base.Execute(ctx, req, tracked)
	if err != nil {
		return resp, err
	}
	for _, c := range tracked {
		t := c.(*trackedCandidate)
		t.mu.Lock()
		if t.response == resp {
			selected = t.ID()
		}
		t.mu.Unlock()
	}
	if req.Kind != ociapi.KindBlob || req.Method != http.MethodGet || req.Header.Get("Range") != "" || resp.RedirectLocation() != "" || resp.StatusCode != http.StatusOK || resp.ContentLength <= 0 || resp.Header.Get("Content-Encoding") != "" {
		return resp, nil
	}
	if len(cands) < 2 {
		return resp, nil // no alternate exists; do not add a pump to a relay fallback
	}
	streamCtx, cancel := context.WithCancel(ctx)
	resp.Body = &adaptiveBody{ctx: streamCtx, cancel: cancel, req: req, candidates: cands, policy: a, body: resp.Body, total: resp.ContentLength, current: selected, tried: map[string]bool{selected: true}, since: time.Now()}
	return resp, nil
}

// Hedged losers may still be exiting when Execute returns.
type trackedCandidate struct {
	Candidate
	mu       sync.Mutex
	response *Result
}

func (c *trackedCandidate) Retain() {
	if owner, ok := c.Candidate.(interface{ Retain() }); ok {
		owner.Retain()
	}
}
func (c *trackedCandidate) Release() {
	if owner, ok := c.Candidate.(interface{ Release() }); ok {
		owner.Release()
	}
}

func (c *trackedCandidate) Attempt(ctx context.Context, req Request) (*Result, error) {
	r, err := c.Candidate.Attempt(ctx, req)
	c.mu.Lock()
	c.response = r
	c.mu.Unlock()
	return r, err
}

type adaptiveBody struct {
	ctx                        context.Context
	cancel                     context.CancelFunc
	req                        Request
	candidates                 []Candidate
	policy                     Adaptive
	body                       io.ReadCloser
	total, offset, windowBytes int64
	since                      time.Time
	current                    string
	tried                      map[string]bool
	checks                     int
	mu                         sync.Mutex // protects body against concurrent Close
	closed                     bool
	probe                      <-chan probeResult
	pump                       *bodyPump
	timer                      *time.Timer
}

type readResult struct {
	n   int
	err error
}

// A pump reads into a borrowed caller buffer only on demand. Before abandoning
// a read, Close/adopt must close the underlying body and join done: returning
// while an old read still writes into that buffer would corrupt a new stream.
// Downstream backpressure never creates unbounded prefetch or extra copies.
type bodyPump struct {
	requests chan []byte
	results  chan readResult
	stop     chan struct{}
	done     chan struct{}
}

func newBodyPump(body io.Reader) *bodyPump {
	p := &bodyPump{requests: make(chan []byte), results: make(chan readResult), stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		for {
			select {
			case buf := <-p.requests:
				n, err := body.Read(buf)
				select {
				case p.results <- readResult{n, err}:
				case <-p.stop:
					return
				}
			case <-p.stop:
				return
			}
		}
	}()
	return p
}

func (b *adaptiveBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	if b.pump == nil {
		b.pump = newBodyPump(b.body)
	}
	if b.timer == nil {
		b.timer = time.NewTimer(max(time.Until(b.since.Add(b.policy.Window)), time.Millisecond))
	}
	pump := b.pump
	timer := b.timer
	b.mu.Unlock()
	select {
	case pump.requests <- p:
	case <-pump.stop:
		<-pump.done
		return 0, io.ErrClosedPipe
	case <-b.ctx.Done():
		_ = b.Close()
		return 0, b.ctx.Err()
	}
	// The window timer survives successful reads. Resetting/stopping it for
	// every TLS record adds hot-path work without changing the sampling window.
	for {
		select {
		case r := <-pump.results:
			n := r.n
			b.offset += int64(n)
			b.windowBytes += int64(n)
			return n, r.err
		case <-timer.C:
			elapsed := time.Since(b.since)
			rate := float64(b.windowBytes) / elapsed.Seconds()
			if b.probe == nil && b.offset < b.total && b.checks < b.policy.MaxSwitches && rate < float64(b.policy.MinBytesPerSecond) {
				b.checks++
				b.startProbe(rate)
			}
			b.since, b.windowBytes = time.Now(), 0
			timer.Reset(b.policy.Window)
		case result := <-b.probe:
			b.probe = nil
			if b.adopt(result) {
				b.since, b.windowBytes = time.Now(), 0
				timer.Reset(b.policy.Window)
				return b.Read(p)
			}
		case <-pump.stop:
			<-pump.done
			return 0, io.ErrClosedPipe
		case <-b.ctx.Done():
			_ = b.Close()
			return 0, b.ctx.Err()
		}
	}
}

func (b *adaptiveBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	if b.pump != nil {
		close(b.pump.stop)
	}
	if b.timer != nil {
		b.timer.Stop()
	}
	if b.cancel != nil {
		b.cancel()
	}
	err := b.body.Close()
	if b.pump != nil {
		<-b.pump.done
		b.pump = nil
	}
	return err
}

type probeResult struct {
	id                 string
	offset             int64
	sample             []byte
	resp               *Result
	cancel             context.CancelFunc
	rate, originalRate float64
}

func (b *adaptiveBody) startProbe(rate float64) {
	// Snapshot reader-owned state before the goroutine starts.
	var candidates []Candidate
	for _, c := range b.candidates {
		if !b.tried[c.ID()] {
			b.tried[c.ID()] = true
			candidates = append(candidates, c)
			break // retain other alternates if a replacement later slows
		}
	}
	result := make(chan probeResult)
	b.probe = result
	offset := b.offset
	var releases []func()
	for _, c := range candidates {
		releases = append(releases, retainCandidate(c))
	}
	go func() {
		defer func() {
			for _, release := range releases {
				release()
			}
		}()
		r := b.probeCandidates(rate, offset, candidates)
		select {
		case result <- r:
		case <-b.ctx.Done():
			if r.cancel != nil {
				r.cancel()
				discard(r.resp)
			}
		}
	}()
}

func (b *adaptiveBody) probeCandidates(rate float64, offset int64, candidates []Candidate) probeResult {
	for _, c := range candidates {
		if b.ctx.Err() != nil {
			break
		}
		req := b.req
		// Probes supply bytes to an already-open stream, never a client handoff.
		req.AllowBlobRedirect = false
		req.Header = b.req.Header.Clone()
		if req.Header == nil {
			req.Header = make(http.Header)
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		ctx, cancel := context.WithCancel(b.ctx)
		timer := time.AfterFunc(b.policy.ProbeTimeout, cancel)
		start := time.Now()
		resp, err := c.Attempt(ctx, req)
		if err != nil {
			b.logProbe(c.ID(), offset, "request failed", err)
			timer.Stop()
			cancel()
			discard(resp)
			continue
		}
		var first, last, total int64
		_, scanErr := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &first, &last, &total)
		valid := resp.StatusCode == http.StatusPartialContent && scanErr == nil && resp.Header.Get("Content-Range") == fmt.Sprintf("bytes %d-%d/%d", first, last, total) && first == offset && total == b.total && last == total-1 && resp.ContentLength == total-first && resp.Header.Get("Content-Encoding") == ""
		if !valid {
			b.logProbe(c.ID(), offset, "invalid range", nil)
			timer.Stop()
			cancel()
			discard(resp)
			continue
		}
		// A larger sample reduces handshake/burst bias and gives the original
		// stream room to advance while this probe runs in the background.
		sample := make([]byte, min(int64(512*1024), b.total-offset))
		_, err = io.ReadFull(resp.Body, sample)
		probeRate := float64(len(sample)) / time.Since(start).Seconds()
		stopped := timer.Stop()
		if err != nil || !stopped || ctx.Err() != nil || probeRate <= rate*1.25 {
			b.logProbe(c.ID(), offset, "sample failed or not faster", err)
			cancel()
			discard(resp)
			continue
		}
		return probeResult{c.ID(), offset, sample, resp, cancel, probeRate, rate}
	}
	return probeResult{}
}

func (b *adaptiveBody) adopt(r probeResult) bool {
	if r.resp == nil {
		return false
	}
	skipped := b.offset - r.offset
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || skipped < 0 || skipped >= int64(len(r.sample)) {
		r.cancel()
		discard(r.resp)
		b.logProbe(r.id, b.offset, "original overtook probe sample", nil)
		return false
	}
	old := b.body
	if b.pump != nil {
		close(b.pump.stop)
		_ = old.Close()
		<-b.pump.done // old Read must release the caller's buffer before reuse
		b.pump = nil
	} else {
		_ = old.Close()
	}
	b.body = &sampleBody{Reader: io.MultiReader(bytes.NewReader(r.sample[skipped:]), r.resp.Body), closer: r.resp.Body, cancel: r.cancel}
	if b.policy.Logger != nil {
		b.policy.Logger.Info("slow blob upstream switched", "from", b.current, "to", r.id, "offset", b.offset, "probe_offset", r.offset, "bytes_per_second", int64(r.originalRate), "probe_bytes_per_second", int64(r.rate))
	}
	b.current = r.id
	return true
}

func (b *adaptiveBody) logProbe(id string, offset int64, reason string, err error) {
	if b.policy.Logger != nil {
		b.policy.Logger.Debug("blob alternate rejected", "upstream", id, "offset", offset, "reason", reason, "error", withoutURL(err))
	}
}

type sampleBody struct {
	io.Reader
	closer io.Closer
	cancel context.CancelFunc
}

func (b *sampleBody) Close() error { b.cancel(); return b.closer.Close() }
