// Package tracelog is the platform's log handler (#1894): JSON records to
// stderr, each stamped with the trace_id and span_id of the span its context
// carries, so a stderr line and the span of the call that wrote it are joined
// by one id; and, when the deployment exports log records over OTLP, the
// same records fanned out to the export handler at the same level.
//
// Build assembles it. Handler is the stamp alone, for a sink of the caller's
// choosing. The package reads the span off the context and knows nothing
// about the platform, which is what makes it usable before the platform
// exists: the composition root installs it as the default logger's handler
// first, and every slog.*Context call after that is correlated.
package tracelog

import (
	"context"
	"io"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// Build assembles the default logger's handler: a JSON handler on w at level,
// under the trace-id stamp, and, when export is not nil, the export handler
// beside it under the same level. The OTLP bridge sets the trace ids on its
// own records from the context, so it is not under the stamp; it is under
// the level filter so the export follows LOG_LEVEL as stderr does.
func Build(w io.Writer, level slog.Level, export slog.Handler) slog.Handler {
	stderr := New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
	if export == nil {
		return stderr
	}
	return tee(stderr, levelFilter(level, export))
}

// The attribute keys, the names the OpenTelemetry log data model and the
// collector's trace-to-log correlation read.
const (
	KeyTraceID = "trace_id"
	KeySpanID  = "span_id"
)

// Handler is an slog.Handler that stamps trace_id and span_id onto a record
// logged with a context carrying a valid span, then hands it to the handler
// it wraps. A record logged without a span passes through unchanged.
type Handler struct {
	next slog.Handler
}

// New wraps next.
func New(next slog.Handler) *Handler {
	return &Handler{next: next}
}

// Enabled defers to the wrapped handler.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle adds the span's ids when ctx carries one.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String(KeyTraceID, sc.TraceID().String()), slog.String(KeySpanID, sc.SpanID().String()))
	}
	return h.next.Handle(ctx, r) //nolint:wrapcheck // a handler returns the sink's error as it is
}

// WithAttrs wraps the wrapped handler's WithAttrs.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{next: h.next.WithAttrs(attrs)}
}

// WithGroup wraps the wrapped handler's WithGroup. The trace ids are added at
// Handle time, after the group is open, so they land inside it with the
// record's other attributes, as the slog contract for a group says.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(name)}
}

// tee delivers every record to each handler that is enabled for its level.
func tee(handlers ...slog.Handler) slog.Handler {
	return multiHandler(handlers)
}

type multiHandler []slog.Handler

// Enabled reports whether any handler wants the level.
func (m multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle delivers the record to each enabled handler; the first error is
// returned after every handler has been given the record, so one failing
// sink never starves another.
func (m multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var first error
	for _, h := range m {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// WithAttrs applies the attributes to each handler.
func (m multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

// WithGroup opens the group on each handler.
func (m multiHandler) WithGroup(name string) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithGroup(name)
	}
	return out
}

// levelFilter holds next to records at or above min. The otelslog bridge
// accepts every level; this is what makes the export follow LOG_LEVEL.
func levelFilter(minLevel slog.Leveler, next slog.Handler) slog.Handler {
	return &levelHandler{min: minLevel, next: next}
}

type levelHandler struct {
	min  slog.Leveler
	next slog.Handler
}

// Enabled holds the level to the minimum and then defers to the sink.
func (l *levelHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= l.min.Level() && l.next.Enabled(ctx, level)
}

// Handle hands the record to the sink; Enabled has already filtered it.
func (l *levelHandler) Handle(ctx context.Context, r slog.Record) error {
	return l.next.Handle(ctx, r) //nolint:wrapcheck // a handler returns the sink's error as it is
}

// WithAttrs wraps the sink's WithAttrs under the same level.
func (l *levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelHandler{min: l.min, next: l.next.WithAttrs(attrs)}
}

// WithGroup wraps the sink's WithGroup under the same level.
func (l *levelHandler) WithGroup(name string) slog.Handler {
	return &levelHandler{min: l.min, next: l.next.WithGroup(name)}
}
