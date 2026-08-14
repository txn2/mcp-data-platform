// Package main provides the entry point for the mcp-data-platform server.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	// The IANA timezone database, compiled into the binary. The release image
	// is built FROM scratch and carries no /usr/share/zoneinfo, so without this
	// every named zone a managed script's schedule can be written in
	// ("America/Los_Angeles") would resolve on a developer's machine and fail
	// on the deployed one.
	_ "time/tzdata"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/httpserver"
	mcpserver "github.com/txn2/mcp-data-platform/internal/server"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

const (
	transportHTTP = "http"
	// lifecycleStopTimeout bounds how long Platform.Stop is allowed to
	// run before Close proceeds anyway. Sized so the full shutdown
	// budget (preDelay + httpGrace + lifecycleStop + close overhead)
	// fits inside a 60s terminationGracePeriodSeconds with headroom.
	lifecycleStopTimeout = 10 * time.Second
)

// logKeyError is the structured-log key for an error value.
const logKeyError = "error"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate-config" {
		if err := runMigrateConfig(os.Args[2:]); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

type serverOptions struct {
	configPath  string
	transport   string
	address     string
	showVersion bool
}

func parseFlags() serverOptions {
	opts := serverOptions{}
	flag.StringVar(&opts.configPath, "config", "", "Path to configuration file")
	flag.StringVar(&opts.transport, "transport", "stdio", "Transport type: stdio, http")
	flag.StringVar(&opts.address, "address", ":8080", "Server address for HTTP transports")
	flag.BoolVar(&opts.showVersion, "version", false, "Show version and exit")
	flag.Parse() //nolint:revive // flag.Parse in main-called function is intentional
	return opts
}

func setupSignalHandler() context.Context {
	ctx, cancel := context.WithCancel(context.Background()) // #nosec G118 -- root process context; cancel is called in the goroutine below
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		slog.Info("received shutdown signal", "signal", sig)
		cancel()
	}()
	return ctx
}

type serverResult struct {
	mcpServer *mcp.Server
	platform  *platform.Platform
}

func createServer(opts serverOptions) (*serverResult, error) {
	result := &serverResult{}
	var err error

	if opts.configPath != "" {
		result.mcpServer, result.platform, err = mcpserver.NewWithConfig(opts.configPath)
		if err != nil {
			return nil, fmt.Errorf("creating server with config: %w", err)
		}
		return result, nil
	}

	result.mcpServer, err = mcpserver.NewWithDefaults()
	if err != nil {
		return nil, fmt.Errorf("creating server with defaults: %w", err)
	}
	return result, nil
}

// initLogging configures slog from the LOG_LEVEL environment variable.
// Supported values: debug, info, warn, error. Defaults to info.
func initLogging() {
	level := slog.LevelInfo
	switch os.Getenv("LOG_LEVEL") {
	case "debug", "DEBUG":
		level = slog.LevelDebug
	case "warn", "WARN":
		level = slog.LevelWarn
	case "error", "ERROR":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})))
}

func run() error {
	initLogging()
	opts := parseFlags()

	if opts.showVersion {
		fmt.Printf("mcp-data-platform version %s (commit: %s, built: %s)\n",
			mcpserver.Version, mcpserver.Commit, mcpserver.Date)
		return nil
	}

	ctx := setupSignalHandler()

	result, err := createServer(opts)
	if err != nil {
		return fmt.Errorf("creating server: %w", err)
	}
	defer closeServer(result)

	applyConfigOverrides(result.platform, &opts)

	return startServer(ctx, result.mcpServer, result.platform, opts)
}

func closeServer(result *serverResult) {
	if result.platform != nil {
		// Stop runs every Lifecycle OnStop callback (background workers,
		// reapers, listeners). Bounded by lifecycleStopTimeout so a
		// hung worker cannot exceed the K8s termination grace period;
		// abandoned work is safe because PostgreSQL leases expire and
		// another replica reclaims it.
		stopCtx, cancel := context.WithTimeout(context.Background(), lifecycleStopTimeout)
		if err := result.platform.Stop(stopCtx); err != nil {
			slog.Error("shutdown: platform stop error", logKeyError, err)
		}
		cancel()

		if err := result.platform.Close(); err != nil {
			slog.Error("shutdown: platform close error", logKeyError, err)
		}
	}
	slog.Info("shutdown: complete")
}

func applyConfigOverrides(p *platform.Platform, opts *serverOptions) {
	if p == nil {
		return
	}
	if p.Config().Server.Transport != "" {
		opts.transport = p.Config().Server.Transport
	}
	if p.Config().Server.Address != "" {
		opts.address = p.Config().Server.Address
	}
}

func startServer(ctx context.Context, mcpServer *mcp.Server, p *platform.Platform, opts serverOptions) error {
	// Start the /metrics listener for BOTH transports so operators get
	// the same observability surface whether the platform is running
	// in stdio (one-off CLI / Claude Desktop) or HTTP mode. Then run the
	// transport-aware runtime wiring in one call: WireRuntime owns the
	// ordering of the api-gateway metrics/mem-budget, gateway integrations,
	// and admin self-connection wiring so main.go no longer encodes it
	// (#854). All of it is nil-safe and no-op when the relevant subsystems
	// are disabled.
	if p != nil {
		if err := p.StartMetricsListener(ctx); err != nil {
			return fmt.Errorf("starting metrics listener: %w", err)
		}
		p.WireRuntime(platform.RuntimeConfig{Transport: opts.transport, Address: opts.address})
	}

	// Optional debug pprof listener (off unless PPROF_ADDR is set). Used by the
	// load-test harness (test/load) to capture CPU/heap/goroutine profiles; it
	// exposes process internals, so it is never on by default. Shut down when
	// ctx is canceled.
	startPprofListener(ctx, os.Getenv(pprofEnvAddr))

	switch opts.transport {
	case "stdio":
		if err := mcpServer.Run(ctx, &mcp.StdioTransport{}); err != nil {
			return fmt.Errorf("running stdio server: %w", err)
		}
		return nil
	case transportHTTP, "sse":
		// HTTP serves both SSE (/sse, /message) and Streamable HTTP (/mcp).
		// "sse" is accepted for backward compatibility. The HTTP mux
		// assembly and drain/shutdown sequencing live in internal/httpserver.
		if err := httpserver.Serve(ctx, mcpServer, p, opts.address); err != nil {
			return fmt.Errorf("running http server: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown transport: %s", opts.transport)
	}
}

// stdioMarker is the conventional marker for stdin/stdout in CLI tools.
const stdioMarker = "-"

func runMigrateConfig(args []string) error {
	fs := flag.NewFlagSet("migrate-config", flag.ExitOnError)
	configPath := fs.String("config", stdioMarker, "Config file path (- for stdin)")
	outputPath := fs.String("output", stdioMarker, "Output file path (- for stdout)")
	targetVersion := fs.String("target-version", "", "Target config version (default: latest)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parsing flags: %w", err)
	}

	var r io.Reader
	if *configPath == stdioMarker {
		r = os.Stdin
	} else {
		// #nosec G304 -- path is from CLI args, controlled by admin
		f, err := os.Open(*configPath)
		if err != nil {
			return fmt.Errorf("opening config: %w", err)
		}
		defer func() { _ = f.Close() }()
		r = f
	}

	var w io.Writer
	if *outputPath == stdioMarker {
		w = os.Stdout
	} else {
		// #nosec G304 -- path is from CLI args, controlled by admin
		f, err := os.Create(*outputPath)
		if err != nil {
			return fmt.Errorf("creating output: %w", err)
		}
		defer func() { _ = f.Close() }()
		w = f
	}

	if err := platform.MigrateConfig(r, w, *targetVersion); err != nil {
		return fmt.Errorf("migrating config: %w", err)
	}
	return nil
}
