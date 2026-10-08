package scriptexec

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

type fakeQueueReads struct {
	state scriptstore.QueueState
	due   int64
	age   time.Duration
	err   error
}

func (f fakeQueueReads) RunQueueState(context.Context) (scriptstore.QueueState, error) {
	return f.state, f.err
}

func (f fakeQueueReads) DueScheduleState(context.Context) (int64, time.Duration, error) {
	return f.due, f.age, f.err
}

func scrapeRecorder(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	return string(body)
}

// TestRegisterQueueGauges_ReportsTheRunQueueAndTheDueSchedules: the run
// queue reports under the worker's loop and the due schedules under the
// scheduler's, so a deployment with no scheduler running shows its schedules
// piling up (#1897).
func TestRegisterQueueGauges_ReportsTheRunQueueAndTheDueSchedules(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	defer func() { _ = m.Shutdown(context.Background()) }()
	reads := fakeQueueReads{
		state: scriptstore.QueueState{Pending: 2, Running: 1, OldestDue: 30 * time.Second},
		due:   3, age: 5 * time.Minute,
	}
	registerQueueGauges(m, reads, reads)
	body := scrapeRecorder(t, m)
	assert.Contains(t, body, `background_queue_items{loop="script_worker",state="pending"} 2`)
	assert.Contains(t, body, `background_queue_items{loop="script_worker",state="running"} 1`)
	assert.Contains(t, body, `background_queue_oldest_age_seconds{loop="script_worker"} 30`)
	assert.Contains(t, body, `background_queue_items{loop="script_scheduler",state="pending"} 3`)
	assert.Contains(t, body, `background_queue_oldest_age_seconds{loop="script_scheduler"} 300`)
}

func TestRegisterQueueGauges_AFailingReadReportsNothingAndNoStoreRegistersNothing(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	defer func() { _ = m.Shutdown(context.Background()) }()
	registerQueueGauges(m, fakeQueueReads{err: errors.New("down")}, fakeQueueReads{err: errors.New("down")})
	registerQueueGauges(m, struct{}{}, nil)
	assert.NotContains(t, scrapeRecorder(t, m), "background_queue_items")
}
