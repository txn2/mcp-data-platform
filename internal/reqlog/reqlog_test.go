package reqlog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// captureLog routes the default logger to a buffer at DEBUG for one test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func record(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("one JSON record expected, got %q: %v", buf.String(), err)
	}
	return rec
}

func TestFailed_ACallerStillWaitingIsLoggedAtError(t *testing.T) {
	buf := captureLog(t)
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)
	if gone := Failed(r, "maps: reading an archive", errors.New("no such key"), "region", "sf"); gone {
		t.Error("Failed reported a waiting caller as gone")
	}
	rec := record(t, buf)
	if rec["level"] != "ERROR" || rec["msg"] != "maps: reading an archive" || rec["error"] != "no such key" || rec["region"] != "sf" {
		t.Errorf("record = %v", rec)
	}
	if _, ok := rec["caller_gone"]; ok {
		t.Errorf("a waiting caller was marked gone: %v", rec)
	}
}

func TestFailed_ACallerWhoLeftIsLoggedAtDebug(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := map[string]struct {
		r   *http.Request
		err error
	}{
		"the request's context is done": {
			httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody).WithContext(ctx),
			errors.New("reading map region: something"),
		},
		"the error is the cancellation": {
			httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody),
			fmt.Errorf("reading maps settings: %w", context.Canceled),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			buf := captureLog(t)
			if gone := Failed(tc.r, "maps: reading an archive", tc.err); !gone {
				t.Error("Failed did not report the caller as gone")
			}
			rec := record(t, buf)
			if gone, _ := rec["caller_gone"].(bool); rec["level"] != "DEBUG" || !gone {
				t.Errorf("record = %v; want DEBUG with caller_gone", rec)
			}
		})
	}
}

func TestFailed_TheErrorIsSanitizedForTheLog(t *testing.T) {
	buf := captureLog(t)
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)
	Failed(r, "sites: reading a file failed", errors.New("bad\nFORGED level=ERROR"))
	if got, _ := record(t, buf)["error"].(string); bytes.ContainsAny([]byte(got), "\n\r") {
		t.Errorf("error kept a line break: %q", got)
	}
}

func TestFailed_ANilErrorIsLoggedEmpty(t *testing.T) {
	buf := captureLog(t)
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)
	Failed(r, "m", nil)
	if rec := record(t, buf); rec["error"] != "" || rec["level"] != "ERROR" {
		t.Errorf("record = %v", rec)
	}
}
