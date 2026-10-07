package observability

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// envLogsExporter selects whether the platform's log records are also
// exported over OTLP (#1894): "otlp" sends every record the stderr JSON
// handler accepts to the collector at OTEL_EXPORTER_OTLP_ENDPOINT, carrying
// the resource and the record's trace and span ids; "none" (the default, and
// anything unrecognized) keeps stderr the only sink. The variable name is the
// one the OpenTelemetry specification defines for this choice.
const envLogsExporter = "OTEL_LOGS_EXPORTER"

// LogsExporter is the OTEL_LOGS_EXPORTER choice.
type LogsExporter string

// The LogsExporter values.
const (
	LogsExporterNone LogsExporter = "none"
	LogsExporterOTLP LogsExporter = "otlp"
)

// LogsConfig holds the operator-configurable knobs for log export.
type LogsConfig struct {
	// Exporter is where log records go beside stderr.
	Exporter LogsExporter

	// OTLP is the collector the records are exported to; shared with the
	// tracer and the metrics OTLP reader.
	OTLP OTLPEndpoint
}

// LogsConfigFromEnv reads the log-export configuration. Export is off unless
// OTEL_LOGS_EXPORTER=otlp.
func LogsConfigFromEnv() LogsConfig {
	cfg := LogsConfig{Exporter: LogsExporterNone, OTLP: OTLPEndpointFromEnv()}
	if strings.EqualFold(stringEnvOrDefault(envLogsExporter, ""), string(LogsExporterOTLP)) {
		cfg.Exporter = LogsExporterOTLP
	}
	return cfg
}

// LogProvider owns the OpenTelemetry LoggerProvider and OTLP exporter that
// the slog bridge writes through. A nil *LogProvider is a valid no-op:
// Handler returns nil and Shutdown returns nil, so the composition root
// holds and uses it unconditionally.
type LogProvider struct {
	provider *sdklog.LoggerProvider

	shutdownOnce sync.Once
	shutdownErr  error
}

// NewLogProvider builds a LogProvider from cfg. When cfg.Exporter is not
// otlp it returns (nil, nil), mirroring NewTracer. The exporter connects
// lazily and records are batched, so an unreachable collector never delays
// startup or blocks a log call.
func NewLogProvider(cfg LogsConfig) (*LogProvider, error) {
	if cfg.Exporter != LogsExporterOTLP {
		//nolint:nilnil // intentional disabled-no-op return; see Handler/Shutdown.
		return nil, nil
	}
	exporter, err := otlploggrpc.New(context.Background(),
		otlpOptions(cfg.OTLP, otlploggrpc.WithEndpoint, otlploggrpc.WithEndpointURL, otlploggrpc.WithInsecure)...)
	if err != nil {
		return nil, fmt.Errorf("observability: otlp log exporter: %w", err)
	}
	return NewLogProviderFromSDK(sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		// The one resource every signal carries (#1893).
		sdklog.WithResource(Resource()),
	)), nil
}

// NewLogProviderFromSDK wraps an already-constructed LoggerProvider. It is
// the seam NewLogProvider uses and the one tests use to inject an in-memory
// exporter. Always returns a non-nil *LogProvider.
func NewLogProviderFromSDK(provider *sdklog.LoggerProvider) *LogProvider {
	return &LogProvider{provider: provider}
}

// Handler is the slog.Handler that forwards records to the provider through
// the otelslog bridge: each record becomes a log record carrying the
// resource, the record's attributes, and the trace and span ids of the
// span its context carries. Nil-safe: a disabled provider returns nil, which
// the composition root reads as "no second sink".
func (p *LogProvider) Handler() slog.Handler {
	if p == nil {
		return nil
	}
	return otelslog.NewHandler(InstrumentationScope, otelslog.WithLoggerProvider(p.provider))
}

// Shutdown flushes buffered records and stops the exporter. Safe on a nil
// receiver and idempotent.
func (p *LogProvider) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.shutdownOnce.Do(func() {
		if err := p.provider.Shutdown(ctx); err != nil {
			p.shutdownErr = fmt.Errorf("observability: logger provider shutdown: %w", err)
		}
	})
	return p.shutdownErr
}
