package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestNewPprofServer_ServesProfilesOnPrivateMux verifies the pprof handlers are
// wired onto the returned server's mux (not DefaultServeMux) and respond.
func TestNewPprofServer_ServesProfilesOnPrivateMux(t *testing.T) {
	srv := newPprofServer("127.0.0.1:0")
	if srv.Handler == nil {
		t.Fatal("newPprofServer returned a server with a nil handler")
	}
	if srv.ReadHeaderTimeout == 0 {
		t.Error("pprof server must set ReadHeaderTimeout to bound slow clients")
	}

	cases := []struct {
		name string
		path string
	}{
		{"index", "/debug/pprof/"},
		{"goroutine", "/debug/pprof/goroutine?debug=1"},
		{"heap", "/debug/pprof/heap?debug=1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tc.path, http.NoBody)
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: got status %d, want 200", tc.path, rec.Code)
			}
			if rec.Body.Len() == 0 {
				t.Errorf("%s: empty profile body", tc.path)
			}
		})
	}
}

// TestStartPprofListener_DisabledWhenAddrEmpty confirms the default (off): an
// empty address starts nothing and returns nil.
func TestStartPprofListener_DisabledWhenAddrEmpty(t *testing.T) {
	if srv := startPprofListener(context.Background(), ""); srv != nil {
		t.Fatalf("expected nil server when PPROF_ADDR is empty, got %v", srv)
	}
}

// TestStartPprofListener_ServesAndShutsDownOnCancel starts the listener on an
// ephemeral port, confirms it serves a profile over the network, and confirms
// canceling the context shuts it down.
func TestStartPprofListener_ServesAndShutsDownOnCancel(t *testing.T) {
	addr := freeLoopbackAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	srv := startPprofListener(ctx, addr)
	if srv == nil {
		t.Fatal("expected a running server for a non-empty address")
	}

	url := fmt.Sprintf("http://%s/debug/pprof/goroutine?debug=1", addr)
	body := getWithRetry(t, url)
	if len(body) == 0 {
		t.Fatal("expected a non-empty goroutine profile")
	}

	cancel()
	// After cancellation the listener should stop accepting connections.
	waitForClosed(t, url)
}

// freeLoopbackAddr returns a currently-free loopback host:port. There is an
// inherent race between closing the probe listener and the caller binding it,
// but the window is small and the test tolerates a retry loop on connect.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a free port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func getWithRetry(t *testing.T, url string) []byte {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get(url) //nolint:noctx,gosec // test-local loopback URL
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("pprof endpoint never came up: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForClosed(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get(url) //nolint:noctx,gosec // test-local loopback URL
		if err != nil {
			return // connection refused: listener is down
		}
		_ = resp.Body.Close()
		if time.Now().After(deadline) {
			t.Fatal("pprof listener did not shut down after context cancel")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
