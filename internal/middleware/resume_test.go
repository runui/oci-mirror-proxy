package middleware

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/ociapi"
	"github.com/runui/oci-mirror-proxy/internal/route"
	"github.com/runui/oci-mirror-proxy/internal/scheduler"
)

// brokenBody delivers its data and then fails, like a connection dropped mid-transfer.
type brokenBody struct {
	r io.Reader
}

func (b *brokenBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if errors.Is(err, io.EOF) {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

func (b *brokenBody) Close() error { return nil }

// scripted returns a dispatcher that answers each call with the next scripted response.
func scripted(responses ...func(req scheduler.Request) *http.Response) (route.Dispatcher, *[]scheduler.Request) {
	var seen []scheduler.Request
	i := 0
	d := route.DispatcherFunc(func(_ context.Context, req scheduler.Request) (*scheduler.Result, error) {
		seen = append(seen, req)
		resp := responses[i](req)
		i++
		return scheduler.Stream(resp), nil
	})
	return d, &seen
}

func blobRequest() scheduler.Request {
	return scheduler.Request{
		Kind:   ociapi.KindBlob,
		Method: http.MethodGet,
		Repo:   "library/nginx",
		Path:   "/v2/library/nginx/blobs/sha256:abc",
		Header: http.Header{},
	}
}

func partial(start, body string, total string) func(scheduler.Request) *http.Response {
	return func(scheduler.Request) *http.Response {
		h := http.Header{}
		h.Set("Content-Range", "bytes "+start+"-"+total)
		return &http.Response{StatusCode: http.StatusPartialContent, Header: h, Body: io.NopCloser(strings.NewReader(body))}
	}
}

func broken(data string) func(scheduler.Request) *http.Response {
	return func(scheduler.Request) *http.Response {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: &brokenBody{r: strings.NewReader(data)}}
	}
}

// brokenPartial is a range response that also drops mid-transfer.
func brokenPartial(start, data, total string) func(scheduler.Request) *http.Response {
	return func(scheduler.Request) *http.Response {
		h := http.Header{}
		h.Set("Content-Range", "bytes "+start+"-"+total)
		return &http.Response{StatusCode: http.StatusPartialContent, Header: h, Body: &brokenBody{r: strings.NewReader(data)}}
	}
}

func resumed(next route.Dispatcher, maxResumes int, req scheduler.Request) (*scheduler.Result, error) {
	return Resume(maxResumes)(next).Dispatch(context.Background(), req)
}

func TestResumeContinuesFromCurrentOffset(t *testing.T) {
	next, seen := scripted(
		broken("01234"),
		func(req scheduler.Request) *http.Response {
			if req.AllowBlobRedirect {
				t.Error("internal resume must require a stream after bytes were committed")
			}
			if got := req.Header.Get("Range"); got != "bytes=5-" {
				t.Errorf("resume Range = %q, want bytes=5-", got)
			}
			return partial("5", "56789", "9/10")(req)
		},
	)
	req := blobRequest()
	req.AllowBlobRedirect = true
	resp, err := resumed(next, 3, req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("resumed read failed: %v", err)
	}
	if string(got) != "0123456789" {
		t.Fatalf("resumed body = %q, want the complete blob", got)
	}
	if len(*seen) != 2 {
		t.Fatalf("dispatched %d requests, want 2", len(*seen))
	}
}

func TestResumeRejectsUpstreamIgnoringRange(t *testing.T) {
	// This upstream resends the whole blob with 200. Splicing it in would corrupt the file.
	next, _ := scripted(
		broken("01234"),
		func(scheduler.Request) *http.Response {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("0123456789"))}
		},
	)
	resp, err := resumed(next, 3, blobRequest())
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(resp.Body)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read error = %v, want the original ErrUnexpectedEOF", err)
	}
}

func TestResumeRejectsMismatchedContentRange(t *testing.T) {
	// The range starts at the wrong offset, so the bytes would be spliced in misaligned.
	next, _ := scripted(
		broken("01234"),
		partial("2", "23456789", "9/10"),
	)
	resp, err := resumed(next, 3, blobRequest())
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(resp.Body)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read error = %v, want the original ErrUnexpectedEOF", err)
	}
}

func TestResumeLeavesRangeRequestsAlone(t *testing.T) {
	body := io.NopCloser(strings.NewReader("partial"))
	next := route.DispatcherFunc(func(context.Context, scheduler.Request) (*scheduler.Result, error) {
		return scheduler.Stream(&http.Response{StatusCode: http.StatusPartialContent, Header: http.Header{}, Body: body}), nil
	})
	req := blobRequest()
	req.Header.Set("Range", "bytes=10-")

	resp, err := resumed(next, 3, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != body {
		t.Fatal("client range requests must not be wrapped")
	}
}

func TestResumeStopsAfterMaxResumes(t *testing.T) {
	// One resume is allowed. The resumed body also breaks, so the error must surface.
	next, seen := scripted(
		broken("x"),
		brokenPartial("1", "y", "1/2"),
	)
	resp, err := resumed(next, 1, blobRequest())
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read error = %v, want ErrUnexpectedEOF after the resume budget is spent", err)
	}
	if string(got) != "xy" {
		t.Fatalf("delivered %q before failing, want %q", got, "xy")
	}
	if len(*seen) != 2 {
		t.Fatalf("dispatched %d requests, want exactly 2 (initial + one resume)", len(*seen))
	}
}

func TestNonBlobRequestsAreNotWrapped(t *testing.T) {
	body := io.NopCloser(strings.NewReader("{}"))
	next := route.DispatcherFunc(func(context.Context, scheduler.Request) (*scheduler.Result, error) {
		return scheduler.Stream(&http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body}), nil
	})
	req := blobRequest()
	req.Kind = ociapi.KindManifest
	resp, err := resumed(next, 3, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != body {
		t.Fatal("manifest responses must not be wrapped")
	}
}

func TestContentRangeStart(t *testing.T) {
	tests := map[string]struct {
		in    string
		start int64
		ok    bool
	}{
		"normal":    {"bytes 5-9/10", 5, true},
		"unknown":   {"bytes 5-9/*", 5, true},
		"empty":     {"", 0, false},
		"wrong":     {"items 5-9/10", 0, false},
		"malformed": {"bytes x-9/10", 0, false},
	}
	for name, tc := range tests {
		start, ok := contentRangeStart(tc.in)
		if start != tc.start || ok != tc.ok {
			t.Errorf("%s: contentRangeStart(%q) = %d, %v", name, tc.in, start, ok)
		}
	}
}
