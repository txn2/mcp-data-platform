package notifydelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// memQueue is an in-memory notification.QueueStore honoring the claim
// contract, so the real Enqueuer and real Worker run against it end to end.
type memQueue struct {
	mu   sync.Mutex
	rows []notification.Notification
	next int64
}

func (q *memQueue) Enqueue(_ context.Context, n notification.Notification) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.next++
	n.ID = q.next
	n.Status = notification.StatusPending
	if n.ScheduledFor.IsZero() {
		n.ScheduledFor = time.Now().UTC()
	}
	q.rows = append(q.rows, n)
	return nil
}

func (q *memQueue) ClaimImmediate(_ context.Context, _ time.Duration) (*notification.Notification, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.rows {
		r := &q.rows[i]
		if !r.Digest && r.Status == notification.StatusPending && !r.ScheduledFor.After(time.Now()) {
			r.Status = notification.StatusSending
			r.Attempts++
			clone := *r
			return &clone, nil
		}
	}
	return nil, notification.ErrNoWork
}

func (q *memQueue) ClaimDigest(_ context.Context, _ time.Duration) ([]notification.Notification, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var recipient string
	for i := range q.rows {
		r := &q.rows[i]
		if r.Digest && r.Status == notification.StatusPending && !r.ScheduledFor.After(time.Now()) {
			recipient = r.Recipient
			break
		}
	}
	if recipient == "" {
		return nil, notification.ErrNoWork
	}
	var batch []notification.Notification
	for i := range q.rows {
		r := &q.rows[i]
		if r.Digest && r.Recipient == recipient && r.Status == notification.StatusPending && !r.ScheduledFor.After(time.Now()) {
			r.Status = notification.StatusSending
			r.Attempts++
			batch = append(batch, *r)
		}
	}
	return batch, nil
}

func (q *memQueue) MarkSent(_ context.Context, ids []int64) error {
	q.setStatus(ids, notification.StatusSent)
	return nil
}

func (q *memQueue) Retry(_ context.Context, ids []int64, _ string, _ time.Duration) error {
	q.setStatus(ids, notification.StatusPending)
	return nil
}

func (q *memQueue) Fail(_ context.Context, ids []int64, _ string) error {
	q.setStatus(ids, notification.StatusFailed)
	return nil
}

func (*memQueue) PurgeOld(_ context.Context, _, _ time.Duration) (int64, error) {
	return 0, nil
}

func (q *memQueue) setStatus(ids []int64, status string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.rows {
		for _, id := range ids {
			if q.rows[i].ID == id {
				q.rows[i].Status = status
			}
		}
	}
}

func (q *memQueue) snapshot() []notification.Notification {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]notification.Notification(nil), q.rows...)
}

// dailyPrefs serves daily-mode preferences for one recipient, defaults
// otherwise.
type dailyPrefs struct{ daily string }

func (p *dailyPrefs) Get(_ context.Context, email string) (notification.Prefs, error) {
	if email == p.daily {
		return notification.Prefs{
			Email: email, Mode: notification.ModeDaily,
			SharesEnabled: true, CommentsEnabled: true,
		}, nil
	}
	return notification.DefaultPrefs(email), nil
}

func (*dailyPrefs) Set(_ context.Context, email string, _ notification.PrefsUpdate) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}

// enabledSettings serves enabled SMTP settings to the worker.
type enabledSettings struct{}

func (enabledSettings) GetSMTP(context.Context) (*notification.SMTPSettings, error) {
	return &notification.SMTPSettings{
		Enabled: true, Host: "smtp.example.com", Port: 587,
		From: "platform@example.com", TLSMode: notification.TLSModeStartTLS,
	}, nil
}

func (enabledSettings) SetSMTP(context.Context, notification.SMTPSettings, string) error {
	return nil
}

// smtpSink captures delivered emails in place of a real SMTP server.
type smtpSink struct {
	mu   sync.Mutex
	sent []notification.Email
}

func (s *smtpSink) Send(_ context.Context, _ notification.SMTPSettings, e notification.Email) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, e)
	return nil
}

func (s *smtpSink) emails() []notification.Email {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]notification.Email(nil), s.sent...)
}

// shareInsertOK accepts every share insert.
type shareInsertOK struct{ portal.ShareStore }

func (shareInsertOK) Insert(context.Context, portal.Share) error { return nil }

// userMiddleware injects the authenticated portal user.
func userMiddleware(user *portal.User) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(portal.ContextWithUser(r.Context(), user)))
		})
	}
}

// startTestWorker runs the real send worker over the queue into the sink.
func startTestWorker(t *testing.T, queue notification.QueueStore, sink *smtpSink) {
	t.Helper()
	renderer, err := notification.NewRenderer(notification.Branding{
		Name: "ACME Data Platform", BaseURL: "https://data.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	worker := notification.NewWorker(notification.WorkerConfig{
		Queue: queue, Settings: enabledSettings{}, Renderer: renderer, Sender: sink,
		PollEvery: 10 * time.Millisecond,
	})
	worker.Start(context.Background())
	t.Cleanup(worker.Stop)
}

func awaitEmails(sink *smtpSink, want int) []notification.Email {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(sink.emails()) < want {
		time.Sleep(5 * time.Millisecond)
	}
	return sink.emails()
}

// TestShareToEmailEndToEnd exercises the full notification path with the
// real components wired together: an authenticated POST to the real portal
// share handler fires the real bridge and Enqueuer (consulting real default
// preferences), the queued row is claimed by the real send Worker, rendered
// by the real branded Renderer, and delivered to a captured SMTP sink. Only
// the SMTP socket and the database are substituted (in-memory queue
// honoring the claim contract).
func TestShareToEmailEndToEnd(t *testing.T) {
	queue := &memQueue{}
	enq := notification.NewEnqueuer(&dailyPrefs{}, queue, 13)
	defer enq.Close()

	owner := &portal.User{UserID: "u-owner", Email: "owner@example.com"}
	assets := &fakeAssets{asset: &portal.Asset{
		ID: "a1", Name: "Quarterly Revenue", OwnerID: "u-owner", OwnerEmail: owner.Email,
	}}
	bridge := NewPortalNotifier(enq, PortalStores{Assets: assets}, "https://data.example.com")
	h := portal.NewHandler(portal.Deps{
		AssetStore:    assets,
		ShareStore:    shareInsertOK{},
		PublicBaseURL: "https://data.example.com",
		Notifier:      bridge,
	}, userMiddleware(owner))

	// 1. The real share handler receives a direct share request.
	body, err := json.Marshal(map[string]any{"shared_with_email": "teammate@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/portal/assets/a1/shares", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("share request = %d (%s)", rec.Code, rec.Body.String())
	}

	// 2. The enqueue path queued exactly one pending row for the recipient.
	rows := queue.snapshot()
	if len(rows) != 1 {
		t.Fatalf("expected 1 queued notification, got %d", len(rows))
	}
	if rows[0].Recipient != "teammate@example.com" || rows[0].Status != notification.StatusPending {
		t.Fatalf("unexpected queue row: %+v", rows[0])
	}

	// 3. The real worker claims, renders, and sends it.
	sink := &smtpSink{}
	startTestWorker(t, queue, sink)

	// 4. The sink received one branded email with a working deep link.
	emails := awaitEmails(sink, 1)
	if len(emails) != 1 {
		t.Fatalf("expected 1 delivered email, got %d", len(emails))
	}
	email := emails[0]
	if email.To != "teammate@example.com" {
		t.Errorf("To = %q", email.To)
	}
	if !strings.Contains(email.Subject, "owner@example.com") || !strings.Contains(email.Subject, "Quarterly Revenue") {
		t.Errorf("Subject = %q", email.Subject)
	}
	if !strings.Contains(email.HTML, "https://data.example.com/portal/view/") {
		t.Error("HTML missing the share deep link")
	}
	if !strings.Contains(email.HTML, "ACME Data Platform") {
		t.Error("HTML missing branding")
	}
	if email.Text == "" {
		t.Error("plaintext part missing")
	}

	// 5. The queue row transitioned to sent.
	final := queue.snapshot()
	if final[0].Status != notification.StatusSent {
		t.Errorf("row status = %q; want sent", final[0].Status)
	}
}

// TestDigestEndToEnd verifies daily-mode batching: two events for a daily
// recipient produce one digest email covering both, once the window is due.
func TestDigestEndToEnd(t *testing.T) {
	queue := &memQueue{}
	enq := notification.NewEnqueuer(&dailyPrefs{daily: "teammate@example.com"}, queue, 13)
	defer enq.Close()

	for _, title := range []string{"One", "Two"} {
		_, err := enq.Notify(context.Background(), "teammate@example.com", notification.CategoryShare,
			notification.Payload{Kind: notification.KindAsset, ItemTitle: title, Actor: "owner@example.com"})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Force both digest rows due now (the real scheduler stamps the next
	// window; the worker only claims rows whose time has come).
	queue.mu.Lock()
	for i := range queue.rows {
		if !queue.rows[i].Digest {
			t.Fatal("daily mode must queue digest rows")
		}
		queue.rows[i].ScheduledFor = time.Now().Add(-time.Second)
	}
	queue.mu.Unlock()

	sink := &smtpSink{}
	startTestWorker(t, queue, sink)

	emails := awaitEmails(sink, 1)
	if len(emails) != 1 {
		t.Fatalf("digest must produce exactly 1 email, got %d", len(emails))
	}
	if !strings.Contains(emails[0].Subject, "2 updates") {
		t.Errorf("digest subject = %q", emails[0].Subject)
	}
	for _, want := range []string{"One", "Two"} {
		if !strings.Contains(emails[0].HTML, want) {
			t.Errorf("digest HTML missing %q", want)
		}
	}
}
