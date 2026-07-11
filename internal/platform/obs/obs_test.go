package obs

import (
	"context"
	"net"
	"testing"
)

func TestAssemble_Disabled(t *testing.T) {
	// Metrics are on by default; disable explicitly. Tracing is off by default.
	// Assemble still returns a usable, nil-safe layer.
	t.Setenv("OTEL_METRICS_ENABLED", "false")
	l, err := Assemble()
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if l == nil {
		t.Fatal("Assemble returned nil layer")
	}
	if l.Enabled() {
		t.Error("Enabled() = true with no env; want false")
	}
	if l.Metrics().Enabled() {
		t.Error("Metrics().Enabled() = true when disabled")
	}
	if l.Tracer().Enabled() {
		t.Error("Tracer().Enabled() = true when disabled")
	}
	// A disabled recorder yields a nil, nil-safe listener: Start/Shutdown are
	// no-ops (this is why the platform can call them unconditionally).
	if err := l.Listener().Start(context.Background()); err != nil {
		t.Errorf("disabled Listener().Start = %v", err)
	}
	if err := l.Listener().Shutdown(context.Background()); err != nil {
		t.Errorf("disabled Listener().Shutdown = %v", err)
	}
}

func TestAssemble_MetricsEnabled(t *testing.T) {
	// Reserve an ephemeral port and release it so the listener can bind.
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ephemeral port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	t.Setenv("OTEL_METRICS_ENABLED", "true")
	t.Setenv("OTEL_METRICS_ADDR", addr)

	l, err := Assemble()
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if l.Metrics() == nil {
		t.Fatal("Metrics() = nil with env enabled; want non-nil")
	}
	if !l.Enabled() {
		t.Error("Enabled() = false with metrics enabled")
	}
	if err := l.Listener().Start(context.Background()); err != nil {
		t.Fatalf("Listener().Start: %v", err)
	}
	if err := l.Listener().Shutdown(context.Background()); err != nil {
		t.Fatalf("Listener().Shutdown: %v", err)
	}
}

func TestNew_ExplicitHandles(t *testing.T) {
	// New with nil handles yields a disabled, nil-safe layer with a listener
	// derived from the (nil) recorder.
	l := New(nil, nil)
	if l == nil {
		t.Fatal("New returned nil")
	}
	if l.Enabled() {
		t.Error("New(nil, nil).Enabled() = true")
	}
	// nil recorder → nil-safe no-op listener.
	if err := l.Listener().Start(context.Background()); err != nil {
		t.Errorf("New(nil, nil) Listener().Start = %v", err)
	}
}

func TestLayer_NilSafe(t *testing.T) {
	var l *Layer
	if l.Metrics() != nil {
		t.Error("nil Layer Metrics() != nil")
	}
	if l.Tracer() != nil {
		t.Error("nil Layer Tracer() != nil")
	}
	if l.Enabled() {
		t.Error("nil Layer Enabled() = true")
	}
	if l.Listener() != nil {
		t.Error("nil Layer Listener() != nil")
	}
}
