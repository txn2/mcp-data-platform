// Package dhobs observes a DataHub call the platform makes outside the
// semantic provider's and the DataHub toolkit's own decorators (#1896): the
// apply_knowledge writer and the portal catalog's edits. Each call is one
// datahub_requests_total observation, one datahub_request_duration_seconds
// sample and a datahub.<operation> client span under the caller's span,
// recorded through the platform's one recorder (outbound.Metrics, nil and a
// no-op until the observability layer installs it). The operation is a
// closed name the caller passes; the URNs and values stay off the label and
// the span.
package dhobs

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

const (
	spanPrefix    = "datahub."
	attrOperation = "datahub.operation"
)

// Do runs one DataHub call under its span, records it, and returns what the
// call returned, its error unchanged.
func Do[T any](ctx context.Context, op string, fn func(context.Context) (T, error)) (T, error) {
	ctx, span := observability.ChildSpan(ctx, spanPrefix+op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String(attrOperation, op)))
	start := time.Now()
	v, err := fn(ctx)
	status := observability.UpstreamStatus(err)
	outbound.Metrics().RecordDataHubRequest(ctx, op, status, time.Since(start))
	observability.SetSpanStatus(span, status, err)
	span.End()
	return v, err
}

// Call is Do for a call that returns only an error.
func Call(ctx context.Context, op string, fn func(context.Context) error) error {
	_, err := Do(ctx, op, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return err
}
