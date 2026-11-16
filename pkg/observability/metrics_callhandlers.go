package observability

import (
	"context"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// The tool-call handlers' own cost (#2653). Exposed name:
//
//   - call_handlers_layer_seconds{matched}   the time the handlers' layer spent on one tools/call,
//     the call itself left out: matched="false" for a call no handler on its connection could
//     apply to (the index lookup alone), "true" for one it did (building the call, the policies,
//     queuing the watches, the transforms).
const (
	instCallHandlersLayer = "call_handlers_layer"
	attrMatched           = "matched"
)

// callHandlersLayerBuckets resolve the layer's sub-millisecond cost, which the
// platform's duration buckets (5ms and up) cannot.
//
//nolint:gochecknoglobals // OTel histogram bounds are config, not state.
var callHandlersLayerBuckets = []float64{
	0.00005, 0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 1,
}

// registerCallHandlers registers the handlers' layer histogram.
func (m *Metrics) registerCallHandlers(meter metric.Meter) error {
	h, err := meter.Float64Histogram(instCallHandlersLayer,
		metric.WithDescription("Time the tool-call handlers' layer spent on one tools/call, the call itself left out"),
		metric.WithUnit(unitSeconds))
	if err != nil {
		return wrapReg(instCallHandlersLayer, err)
	}
	m.callHandlersLayer = h
	return nil
}

// CallHandlersLayer records the handlers' layer's own time on one call.
// Nil-safe.
func (m *Metrics) CallHandlersLayer(ctx context.Context, matched bool, d time.Duration) {
	if m == nil || m.callHandlersLayer == nil {
		return
	}
	m.callHandlersLayer.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String(attrMatched, strconv.FormatBool(matched))))
}
