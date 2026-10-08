// Package opsobs holds the recorder the platform's own operations report
// through (#1898): authentication attempts, configuration warnings and the
// domain operations a tool call sets off (knowledge, memory, search, the call
// catalog, the config store, managed resources, table registration, prompts).
// Those sites sit in a dozen packages, several constructed with no handle to
// the observability layer, so they read the one recorder the layer installs
// here instead of having it threaded through every constructor -- the shape
// internal/outbound uses for the HTTP clients.
package opsobs

import (
	"context"
	"sync/atomic"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// current is the platform's one Metrics, installed by the observability layer
// before any operation runs; nil records nothing.
//
//nolint:gochecknoglobals // the one process-wide recorder, like outbound's and the global tracer provider
var current atomic.Pointer[observability.Metrics]

// SetMetrics installs the recorder. Nil-safe; a nil recorder records nothing.
func SetMetrics(m *observability.Metrics) {
	current.Store(m)
}

// Metrics is the installed recorder, nil (and nil-safe) until one is set.
func Metrics() *observability.Metrics {
	return current.Load()
}

// Start opens operation name on the installed recorder: a span under ctx's
// trace and the start of its duration. The caller ends it with End or
// EndResult.
func Start(ctx context.Context, name string) (context.Context, *observability.Op) {
	return Metrics().StartOp(ctx, name)
}
