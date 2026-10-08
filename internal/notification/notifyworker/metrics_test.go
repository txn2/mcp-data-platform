package notifyworker

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func installMetrics(t *testing.T) func() string {
	t.Helper()
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	if err != nil {
		t.Fatal(err)
	}
	bgloop.SetDefaultMetrics(m)
	t.Cleanup(func() {
		bgloop.SetDefaultMetrics(nil)
		_ = m.Shutdown(context.Background())
	})
	return func() string {
		rec := httptest.NewRecorder()
		m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
		b, _ := io.ReadAll(rec.Body)
		return string(b)
	}
}

// TestWorker_CountsEveryDeliveryAttemptByResult: a send that succeeds, one
// that is retried and one that fails for good are each counted, by the
// transport's kind (#1897). An unreachable mail server is a failed attempt an
// operator sees on the counter, not only in the log.
func TestWorker_CountsEveryDeliveryAttemptByResult(t *testing.T) {
	scrape := installMetrics(t)
	queue := &fakeQueueStore{immediate: [][]notification.Notification{
		{{ID: 1, Recipient: "a@b.io", Attempts: DefaultMaxAttempts, Payload: notification.Payload{Kind: notification.KindAsset, ItemTitle: "R"}}},
		{{ID: 2, Recipient: "a@b.io", Attempts: 1, Payload: notification.Payload{Kind: notification.KindAsset, ItemTitle: "R"}}},
		{{ID: 3, Recipient: "a@b.io", Payload: notification.Payload{Kind: notification.KindAsset, ItemTitle: "R"}}},
	}}
	sender := &fakeSender{fails: 2}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, sender)

	if err := w.drain(context.Background()); err != nil {
		t.Fatal(err)
	}

	body := scrape()
	for _, want := range []string{
		`notification_delivery_attempts_total{kind="email",result="delivered"} 1`,
		`notification_delivery_attempts_total{kind="email",result="retry"} 1`,
		`notification_delivery_attempts_total{kind="email",result="failed"} 1`,
		`background_loop_iterations_total{loop="notification_delivery",result="error"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in\n%s", want, body)
		}
	}
}
