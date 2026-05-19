package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// Timeouts on the /metrics listener. Conservative because the
// endpoint is scrape-only: Prometheus scrapers respect their own
// scrape_timeout (default 10s) and a metrics endpoint should never
// need minutes. Bounding these prevents a hung scraper from holding
// a goroutine indefinitely.
const (
	metricsReadHeaderTimeout = 5 * time.Second
	metricsReadTimeout       = 10 * time.Second
	metricsWriteTimeout      = 30 * time.Second
	metricsIdleTimeout       = 60 * time.Second
)

// Listener runs the dedicated HTTP server that exposes /metrics. The
// server is separate from the platform's main HTTP listener so that:
//
//   - scrape traffic does not share the MCP/admin/portal auth path,
//   - the metrics port can sit behind a NetworkPolicy (or be unreachable
//     from outside the cluster) without affecting client-facing routes,
//   - a slow or stuck scraper cannot starve the main listener's
//     accept loop.
//
// Listener is a no-op when the underlying Metrics is nil or the
// listen address is empty; callers can mount it unconditionally.
type Listener struct {
	cfg    Config
	server *http.Server

	mu      sync.Mutex
	started bool
}

// NewListener constructs a Listener for the supplied Metrics. The
// listener serves only /metrics on its mux; all other paths return
// 404. When metrics are disabled NewListener returns nil so callers
// can mount it unconditionally and observe a nil receiver as the
// "disabled" signal.
func NewListener(m *Metrics) *Listener {
	if m == nil || !m.cfg.Enabled || m.cfg.ListenAddr == "" {
		return nil
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", m.Handler())
	return &Listener{
		cfg: m.cfg,
		server: &http.Server{
			Addr:              m.cfg.ListenAddr,
			Handler:           mux,
			ReadHeaderTimeout: metricsReadHeaderTimeout,
			ReadTimeout:       metricsReadTimeout,
			WriteTimeout:      metricsWriteTimeout,
			IdleTimeout:       metricsIdleTimeout,
		},
	}
}

// Start begins serving in a background goroutine. The supplied context
// is observed only for the "address already in use" race during
// startup; long-lived shutdown should go through Shutdown.
func (l *Listener) Start(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.started {
		return errors.New("observability: listener already started")
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", l.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("observability: listen %s: %w", l.cfg.ListenAddr, err)
	}
	slog.Info("observability: metrics listener started", "addr", l.cfg.ListenAddr, "path", "/metrics")

	go func() {
		// Serve returns http.ErrServerClosed when Shutdown is
		// invoked; that is the expected exit, not an error.
		if serveErr := l.server.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			slog.Error("observability: metrics listener exited unexpectedly", "error", serveErr)
		}
	}()
	l.started = true
	return nil
}

// Shutdown gracefully stops the listener. Safe to call on a nil
// receiver or before Start (returns nil).
func (l *Listener) Shutdown(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.started {
		return nil
	}
	if err := l.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("observability: metrics listener shutdown: %w", err)
	}
	return nil
}
