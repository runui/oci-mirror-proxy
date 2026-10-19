package scheduler

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRoutingDebugLogs(t *testing.T) {
	for name, strategy := range map[string]Strategy{"sequential": Sequential{}, "hedged": Hedged{Delay: time.Hour, MaxParallel: 2}} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			ctx := WithLogger(t.Context(), logger.With("request_id", 123))
			resp, err := strategy.Execute(ctx, Request{}, []Candidate{skipped("open"), answer("missing", http.StatusNotFound), answer("winner", http.StatusOK)})
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			for _, want := range []string{"request_id=123", `msg="route selected"`, `msg="upstream skipped"`, `msg="upstream attempt failed"`, "status=404", `msg="upstream hit"`, `msg="upstream selected" request_id=123 upstream=winner`} {
				if !strings.Contains(buf.String(), want) {
					t.Fatalf("missing %q in logs:\n%s", want, buf.String())
				}
			}
		})
	}
}
