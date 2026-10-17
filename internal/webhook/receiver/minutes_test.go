package receiver

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whspool"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// sumMinutes adds every recorded minute together.
func sumMinutes(ms []whstore.ReplicaMinute) whstore.ReplicaMinute {
	var out whstore.ReplicaMinute
	for _, m := range ms {
		out.ReceivedEvents += m.ReceivedEvents
		out.DrainedEvents += m.DrainedEvents
		out.DrainedBytes += m.DrainedBytes
		out.SpoolBytes = max(out.SpoolBytes, m.SpoolBytes)
		out.SpoolEvents = max(out.SpoolEvents, m.SpoolEvents)
	}
	return out
}

// TestTelemetryOfADelivery covers what #2150 records of a delivery: the
// status each request was answered with, the events received and written on
// this replica, the segment's bytes against its window, and the series the
// metrics carry.
func TestTelemetryOfADelivery(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	require.Equal(t, http.StatusAccepted, h.signed(t, "esp", `{"id":"e1"}`).StatusCode)
	require.Equal(t, http.StatusAccepted, h.signed(t, "esp", `{"id":"e2"}`).StatusCode)
	require.Equal(t, http.StatusUnauthorized, h.post(t, "/hooks/esp", "application/json", `{"id":"x"}`, "X-Sig", "bad").StatusCode)
	require.NoError(t, h.r.flushStats(context.Background()))

	h.recorder.mu.Lock()
	defer h.recorder.mu.Unlock()
	assert.Equal(t, int64(2), h.recorder.statuses["esp/202"])
	assert.Equal(t, int64(1), h.recorder.statuses["esp/401"])
	got := sumMinutes(h.recorder.minutes)
	assert.Equal(t, int64(2), got.ReceivedEvents)
	assert.Equal(t, int64(2), got.DrainedEvents, "without a spool the buffer's write is the drain")
	assert.Positive(t, got.DrainedBytes)
	assert.Equal(t, got.DrainedBytes, h.recorder.bytes, "the segment's bytes are counted against its window")
	for _, m := range h.recorder.minutes {
		assert.Equal(t, "host-a:8080", m.Replica)
	}
	h.metrics.mu.Lock()
	defer h.metrics.mu.Unlock()
	assert.Equal(t, 2, h.metrics.responses[http.StatusAccepted])
	assert.Equal(t, 1, h.metrics.responses[http.StatusUnauthorized])
	assert.Equal(t, 2, h.metrics.written)
	assert.Equal(t, 2, h.metrics.stages)
}

// TestTelemetryOfASpool: a spooling receiver reports what its spool holds on
// the minute, and the events it received before they are drained.
func TestTelemetryOfASpool(t *testing.T) {
	objects := newObjects()
	objects.stall = make(chan struct{})
	h := newSpoolHarness(t, t.TempDir(), objects, whspool.Options{MaxBytes: 1 << 20, MaxAge: time.Hour}, hmacSource("esp"))
	require.Equal(t, http.StatusAccepted, h.signed(t, "esp", body("1")).StatusCode)
	require.NoError(t, h.r.flushStats(context.Background()))
	h.recorder.mu.Lock()
	got := sumMinutes(h.recorder.minutes)
	h.recorder.mu.Unlock()
	assert.Equal(t, int64(1), got.ReceivedEvents)
	assert.Zero(t, got.DrainedEvents, "nothing is drained while object storage stalls")
	assert.Equal(t, int64(1), got.SpoolEvents)
	assert.Positive(t, got.SpoolBytes)
	close(objects.stall)
}

// TestMinutesAreKeptThroughAFailedWrite: minutes a write did not store are
// written with the next flush.
func TestMinutesAreKeptThroughAFailedWrite(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	h.r.tallyReceived(3)
	h.r.tallyWritten(3, 100)
	h.recorder.minutesErr = errors.New("db down")
	require.Error(t, h.r.flushStats(context.Background()))
	h.recorder.minutesErr = nil
	h.r.tallyReceived(1)
	require.NoError(t, h.r.flushStats(context.Background()))
	got := sumMinutes(h.recorder.minutes)
	assert.Equal(t, int64(4), got.ReceivedEvents)
	assert.Equal(t, int64(100), got.DrainedBytes)
}
