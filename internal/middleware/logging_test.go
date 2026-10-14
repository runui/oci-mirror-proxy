package middleware

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/route"
	"github.com/runui/oci-mirror-proxy/internal/scheduler"
)

func logged(t *testing.T, err error) string {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	next := route.DispatcherFunc(func(context.Context, scheduler.Request) (*http.Response, error) {
		return nil, err
	})
	_, _ = Logging(logger)(next).Dispatch(t.Context(), scheduler.Request{Registry: "docker.io"})
	return buf.String()
}

func TestContentMissIsInfoNotWarning(t *testing.T) {
	out := logged(t, &scheduler.Exhausted{Failures: []scheduler.Failure{{Candidate: "a", Status: http.StatusNotFound}}})
	if !strings.Contains(out, "level=INFO") || strings.Contains(out, "level=WARN") {
		t.Fatalf("a content miss must log at info level:\n%s", out)
	}
}

func TestRealFailureIsWarning(t *testing.T) {
	out := logged(t, &scheduler.Exhausted{Failures: []scheduler.Failure{{Candidate: "a", Status: http.StatusBadGateway}}})
	if !strings.Contains(out, "level=WARN") {
		t.Fatalf("an upstream failure must log at warn level:\n%s", out)
	}
}
