package middleware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/runui/oci-mirror-proxy/internal/ociapi"
	"github.com/runui/oci-mirror-proxy/internal/route"
	"github.com/runui/oci-mirror-proxy/internal/scheduler"
)

// ErrRangeIgnored means a resumed request was answered with the whole blob
// instead of the requested range. Continuing would corrupt the stream, so the
// resume is abandoned.
var ErrRangeIgnored = errors.New("upstream ignored the Range request")

// Resume makes blob downloads survive a transient mid-stream failure. When the
// body of a whole-blob response breaks, the download continues from the current
// offset through the dispatcher below this middleware. That dispatcher may pick a
// different upstream.
//
// This is safe because a blob is addressed by its digest, so every upstream must
// return identical bytes. The daemon verifies the digest at the end, so a faulty
// upstream can only cause a failed pull, never a corrupted image.
func Resume(maxResumes int) route.Middleware {
	return func(next route.Dispatcher) route.Dispatcher {
		return route.DispatcherFunc(func(ctx context.Context, req scheduler.Request) (*scheduler.Result, error) {
			resp, err := next.Dispatch(ctx, req)
			if err != nil || !resumable(req, resp) {
				return resp, err
			}
			resp.Body = &resumeBody{
				ctx:         ctx,
				next:        next,
				req:         req,
				body:        resp.Body,
				resumesLeft: maxResumes,
			}
			return resp, nil
		})
	}
}

// resumable reports whether a response is a whole-blob download that can be
// continued. Client range requests are passed through untouched.
func resumable(req scheduler.Request, resp *scheduler.Result) bool {
	return req.Kind == ociapi.KindBlob &&
		req.Method == http.MethodGet &&
		req.Header.Get("Range") == "" &&
		resp.StatusCode == http.StatusOK
}

// resumeBody reads a blob and transparently reopens it after a failure.
type resumeBody struct {
	ctx         context.Context
	next        route.Dispatcher // the layer below Resume, so reopening does not nest wrappers
	req         scheduler.Request
	body        io.ReadCloser
	offset      int64 // bytes already delivered to the caller
	resumesLeft int
}

func (b *resumeBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	b.offset += int64(n)
	// A clean EOF ends the stream. A broken connection surfaces as an error
	// (io.ErrUnexpectedEOF when Content-Length is known). When the length is
	// unknown, a dropped connection can look like EOF, and the daemon's digest
	// check is the final guard against that.
	if err == nil || errors.Is(err, io.EOF) {
		return n, err
	}
	if b.resumesLeft == 0 || b.ctx.Err() != nil {
		return n, err
	}
	b.resumesLeft--
	if rerr := b.reopen(); rerr != nil {
		return n, err // report the original failure, which is the root cause
	}
	return n, nil
}

func (b *resumeBody) Close() error {
	return b.body.Close()
}

// reopen continues the download at b.offset, possibly from a different upstream.
func (b *resumeBody) reopen() error {
	_ = b.body.Close()

	req := b.req
	// The original response is already committed. A resumed request must return
	// bytes even when its upstream offers a perfectly valid download redirect.
	req.AllowBlobRedirect = false
	req.Header = b.req.Header.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-", b.offset))

	resp, err := b.next.Dispatch(b.ctx, req)
	if err != nil {
		return err
	}
	// Accept only a genuine partial response that starts exactly where we stopped.
	start, ok := contentRangeStart(resp.Header.Get("Content-Range"))
	if resp.StatusCode != http.StatusPartialContent || !ok || start != b.offset {
		_ = resp.Body.Close()
		return ErrRangeIgnored
	}
	b.body = resp.Body
	return nil
}

// contentRangeStart parses the first byte offset from "bytes START-END/TOTAL".
func contentRangeStart(v string) (int64, bool) {
	unit, spec, ok := strings.Cut(strings.TrimSpace(v), " ")
	if !ok || unit != "bytes" {
		return 0, false
	}
	first, _, ok := strings.Cut(spec, "-")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(first, 10, 64)
	return n, err == nil
}
