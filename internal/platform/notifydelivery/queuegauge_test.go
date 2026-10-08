package notifydelivery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyqueue"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

type fakeQueueState struct {
	states []notifyqueue.KindState
	err    error
}

func (f fakeQueueState) QueueState(context.Context) ([]notifyqueue.KindState, error) {
	return f.states, f.err
}

func TestRegisterQueueGauge_ReportsTheQueueByTransport(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()
	registerQueueGauge(m, fakeQueueState{states: []notifyqueue.KindState{
		{Kind: notifyqueue.KindEmail, Pending: 3, Sending: 1, OldestDue: time.Minute},
		{Kind: notifyqueue.KindChannel},
	}})
	registerQueueGauge(m, struct{}{})
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	b, _ := io.ReadAll(rec.Body)
	body := string(b)
	for _, want := range []string{
		`background_queue_items{kind="email",loop="notification_worker",state="pending"} 3`,
		`background_queue_items{kind="email",loop="notification_worker",state="running"} 1`,
		`background_queue_oldest_age_seconds{kind="email",loop="notification_worker"} 60`,
		`background_queue_items{kind="channel",loop="notification_worker",state="pending"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestRegisterQueueGauge_AFailingReadReportsNothing(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()
	registerQueueGauge(m, fakeQueueState{err: errors.New("down")})
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	if strings.Contains(rec.Body.String(), "background_queue_items") {
		t.Error("a failed read reports no queue")
	}
}
