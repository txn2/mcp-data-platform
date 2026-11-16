package observability

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestCallHandlersLayer: the handlers' layer's own time is exported by
// whether a handler applied, in sub-millisecond buckets (#2653).
func TestCallHandlersLayer(t *testing.T) {
	m := newEnabledMetrics(t)
	ctx := context.Background()
	m.CallHandlersLayer(ctx, false, 80*time.Microsecond)
	m.CallHandlersLayer(ctx, true, 3*time.Millisecond)
	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`call_handlers_layer_seconds_bucket{matched="false",le="0.0001"} 1`,
		`call_handlers_layer_seconds_count{matched="true"} 1`,
		`call_handlers_layer_seconds_bucket{matched="true",le="0.0025"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
	(*Metrics)(nil).CallHandlersLayer(ctx, true, time.Second)
}
