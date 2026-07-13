package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/pprof" //nolint:gosec // G108: pprof is mounted only on the private, env-gated mux below; no listener in this binary serves DefaultServeMux (verified: internal/httpserver + observability use explicit muxes), so the package init side effect is inert.
	"time"
)

// pprofEnvAddr is the environment variable that opts a debug pprof HTTP
// listener in. Empty (the default) means no pprof listener starts.
//
// pprof exposes process internals — live heap contents, goroutine stacks, CPU
// profiles — so it is never on by default and never mounted on a client-facing
// listener. It exists to let the load-test harness (test/load) capture CPU,
// heap, and goroutine profiles from a running platform binary, and for ad-hoc
// operator debugging. Bind it to loopback or a debug-only port reachable only
// by the operator/harness, never to a public interface.
const pprofEnvAddr = "PPROF_ADDR"

// pprofShutdownTimeout bounds the graceful shutdown of the pprof listener when
// the process context is canceled. Kept short: the pprof server holds no
// client-facing traffic worth draining.
const pprofShutdownTimeout = 2 * time.Second

// pprofReadHeaderTimeout bounds request header reads on the pprof listener so a
// slow client cannot pin a goroutine. CPU/trace profiles stream for seconds, so
// only the header phase is bounded (no ReadTimeout/WriteTimeout).
const pprofReadHeaderTimeout = 5 * time.Second

// newPprofServer builds the debug pprof HTTP server bound to addr. The pprof
// handlers are registered on a private mux (not http.DefaultServeMux) so the
// profiling surface exists only on this dedicated listener and never leaks onto
// the platform's MCP/admin/portal/metrics listeners.
func newPprofServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: pprofReadHeaderTimeout,
	}
}

// startPprofListener starts the debug pprof server when addr is non-empty and
// returns it so a caller can inspect or stop it. It returns nil when addr is
// empty (the default: pprof disabled). The server is shut down gracefully when
// ctx is canceled.
func startPprofListener(ctx context.Context, addr string) *http.Server {
	if addr == "" {
		return nil
	}
	srv := newPprofServer(addr)
	go func() {
		slog.Warn("pprof debug listener started (exposes process internals; keep it non-public)", "address", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("pprof listener error", logKeyError, err)
		}
	}()
	// #nosec G118 -- this goroutine runs only after the parent ctx is canceled, so a fresh background context is required to bound the graceful shutdown; reusing the canceled ctx would abort Shutdown instantly.
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), pprofShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			slog.Error("pprof listener shutdown error", logKeyError, err)
		}
	}()
	return srv
}
