package handlertransport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestInternalRoundTripper_BasicResponse(t *testing.T) {
	rt := &internalRoundTripper{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/util/fetch" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "15")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"ok":"boiled"}`))
	})}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://internal.invalid/util/fetch", http.NoBody)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if resp.ContentLength != 15 {
		t.Errorf("ContentLength = %d; want 15 (export cap check reads this)", resp.ContentLength)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"ok":"boiled"}` {
		t.Errorf("body = %q", body)
	}
}

// TestInternalRoundTripper_StreamsLargeBody proves the response flows
// through the pipe without buffering the whole body: the handler
// writes far more than any single buffer while the reader drains
// concurrently.
func TestInternalRoundTripper_StreamsLargeBody(t *testing.T) {
	const chunk = 64 * 1024
	const chunks = 128 // 8 MiB total
	rt := &internalRoundTripper{handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		buf := bytes.Repeat([]byte("x"), chunk)
		for range chunks {
			if _, err := w.Write(buf); err != nil {
				return
			}
		}
	})}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal.invalid/big", http.NoBody)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		t.Fatalf("draining: %v", err)
	}
	if n != chunk*chunks {
		t.Errorf("drained %d bytes; want %d", n, chunk*chunks)
	}
}

func TestInternalRoundTripper_HandlerWritesNothing(t *testing.T) {
	rt := &internalRoundTripper{handler: http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal.invalid/", http.NoBody)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want implicit 200", resp.StatusCode)
	}
	if body, _ := io.ReadAll(resp.Body); len(body) != 0 {
		t.Errorf("body = %q; want empty", body)
	}
	if resp.ContentLength != -1 {
		t.Errorf("ContentLength = %d; want -1 (undeclared)", resp.ContentLength)
	}
}

func TestInternalRoundTripper_HandlerPanic(t *testing.T) {
	rt := &internalRoundTripper{handler: http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal.invalid/", http.NoBody)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d; want 500 from panicking handler", resp.StatusCode)
	}
	if _, rerr := io.ReadAll(resp.Body); rerr == nil {
		t.Error("expected body read error from aborted handler")
	}
}

func TestInternalRoundTripper_ContextCanceledBeforeHeader(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	rt := &internalRoundTripper{handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release // hold the header back until the test releases it
		w.WriteHeader(http.StatusOK)
	})}
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://internal.invalid/", http.NoBody)
	go func() {
		<-started
		cancel()
	}()
	if _, err := rt.RoundTrip(req); !errors.Is(err, context.Canceled) { //nolint:bodyclose // RoundTrip returns a nil response together with the context error
		t.Fatalf("err = %v; want context.Canceled", err)
	}
}

func TestContentLengthFromHeader(t *testing.T) {
	tests := []struct {
		value string
		want  int64
	}{
		{"", -1},
		{"1024", 1024},
		{"0", 0},
		{"-5", -1},
		{"notanumber", -1},
	}
	for _, tt := range tests {
		h := http.Header{}
		if tt.value != "" {
			h.Set("Content-Length", tt.value)
		}
		if got := contentLengthFromHeader(h); got != tt.want {
			t.Errorf("contentLengthFromHeader(%q) = %d; want %d", tt.value, got, tt.want)
		}
	}
}
