package notifydelivery

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/reviewalert"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// The knowledge review-queue alert (#803) is the one producer that enqueues
// without a person having done anything, so its end-to-end proof lives here,
// beside the substrate it feeds and the in-memory queue, worker, and SMTP sink
// the other end-to-end tests already run the real components over.

// staleInsights serves a fixed pending-review rollup through the
// PendingReviewStater fast path knowledge.PendingReviewOf takes.
type staleInsights struct {
	knowledgekit.InsightStore
	review *knowledgekit.PendingReview
}

func (s *staleInsights) PendingReviewStats(context.Context) (*knowledgekit.PendingReview, error) {
	return s.review, nil
}

// alertState is an in-memory reviewalert.StateStore modeling the claim
// contract: one alert per cooldown while the queue stays over threshold.
type alertState struct {
	alerting bool
	lastAt   time.Time
}

func (s *alertState) ClaimAlert(_ context.Context, cooldown time.Duration, now time.Time) (bool, error) {
	if s.alerting && !s.lastAt.IsZero() && s.lastAt.After(now.Add(-cooldown)) {
		return false, nil
	}
	s.alerting, s.lastAt = true, now
	return true, nil
}

func (s *alertState) Clear(context.Context) error {
	s.alerting = false
	return nil
}

// fixedAlertSettings serves a stored alert configuration.
type fixedAlertSettings struct{ settings reviewalert.Settings }

func (f fixedAlertSettings) Get(context.Context) (*reviewalert.Settings, error) {
	return &f.settings, nil
}

func (fixedAlertSettings) Set(context.Context, reviewalert.Settings, string) error { return nil }

// TestReviewQueueAlertToEmailEndToEnd seeds a stale pending review queue and
// runs the real check over the real enqueuer, preference gate, send worker,
// and branded renderer, into a captured SMTP sink. Only the SMTP socket and
// the database are substituted.
func TestReviewQueueAlertToEmailEndToEnd(t *testing.T) {
	queue := &memQueue{}
	enq := notification.NewEnqueuer(&dailyPrefs{}, queue, 13)
	defer enq.Close()

	oldest := time.Now().AddDate(0, 0, -94)
	checker := reviewalert.New(reviewalert.Config{
		Target: reviewalert.KnowledgeTarget(),
		Settings: fixedAlertSettings{settings: reviewalert.Settings{
			Enabled: true, OldestPendingDays: 30, CooldownHours: 24,
			Recipients: []string{"ops@example.com"},
		}},
		State: &alertState{},
		Source: reviewalert.InsightSource{Insights: &staleInsights{review: &knowledgekit.PendingReview{
			TotalPending: 12, OldestPendingAt: &oldest, PendingOver30d: 5,
		}}},
		Enqueuer: enq,
		BaseURL:  "https://data.example.com",
	})
	if checker == nil {
		t.Fatal("checker must be built from a complete configuration")
	}

	// 1. The scheduled check finds the queue over threshold and enqueues.
	if err := checker.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	rows := queue.snapshot()
	if len(rows) != 1 {
		t.Fatalf("expected 1 queued alert, got %d", len(rows))
	}
	if rows[0].Recipient != "ops@example.com" || rows[0].Category != notification.CategoryReviewQueue {
		t.Fatalf("unexpected queue row: %+v", rows[0])
	}

	// 2. The real worker claims, renders, and delivers it.
	sink := &smtpSink{}
	startTestWorker(t, queue, sink)

	emails := awaitEmails(sink, 1)
	if len(emails) != 1 {
		t.Fatalf("expected 1 delivered email, got %d", len(emails))
	}
	email := emails[0]
	if email.To != "ops@example.com" {
		t.Errorf("To = %q", email.To)
	}
	if !strings.Contains(email.Subject, "12 insights awaiting review") {
		t.Errorf("Subject = %q", email.Subject)
	}
	// Every figure the acceptance criteria name, plus the way into the queue.
	for _, want := range []string{
		"12 insights awaiting review",
		"The oldest has been waiting 94 days.",
		"5 insights have been pending for 30 days or more.",
		"https://data.example.com/portal/knowledge#review",
		"ACME Data Platform",
	} {
		if !strings.Contains(email.HTML, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	if email.Text == "" {
		t.Error("plaintext part missing")
	}

	// 3. The row resolved as sent.
	if final := queue.snapshot(); final[0].Status != notification.StatusSent {
		t.Errorf("row status = %q; want sent", final[0].Status)
	}
}

// TestReviewQueueAlertRespectsOptOutEndToEnd: a recipient who turned
// notifications off receives no mail, even though the operator listed them.
func TestReviewQueueAlertRespectsOptOutEndToEnd(t *testing.T) {
	queue := &memQueue{}
	enq := notification.NewEnqueuer(&offPrefs{off: "muted@example.com"}, queue, 13)
	defer enq.Close()

	oldest := time.Now().AddDate(0, 0, -94)
	checker := reviewalert.New(reviewalert.Config{
		Target: reviewalert.KnowledgeTarget(),
		Settings: fixedAlertSettings{settings: reviewalert.Settings{
			Enabled: true, OldestPendingDays: 30, CooldownHours: 24,
			Recipients: []string{"muted@example.com", "ops@example.com"},
		}},
		State: &alertState{},
		Source: reviewalert.InsightSource{Insights: &staleInsights{review: &knowledgekit.PendingReview{
			TotalPending: 12, OldestPendingAt: &oldest,
		}}},
		Enqueuer: enq,
		BaseURL:  "https://data.example.com",
	})
	if err := checker.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	rows := queue.snapshot()
	if len(rows) != 1 || rows[0].Recipient != "ops@example.com" {
		t.Fatalf("only the opted-in recipient may be queued, got %+v", rows)
	}
}

// offPrefs serves ModeOff for one recipient and the defaults for everyone else.
type offPrefs struct{ off string }

func (p *offPrefs) Get(_ context.Context, email string) (notification.Prefs, error) {
	if email == p.off {
		return notification.Prefs{Email: email, Mode: notification.ModeOff}, nil
	}
	return notification.DefaultPrefs(email), nil
}

func (*offPrefs) Set(_ context.Context, email string, _ notification.PrefsUpdate) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}
