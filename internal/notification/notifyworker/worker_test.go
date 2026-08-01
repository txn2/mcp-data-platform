package notifyworker

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyrender"
	"github.com/txn2/mcp-data-platform/internal/notification/notifysend"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/notification/smtp"
)

// fakeSettingsStore serves canned SMTP settings.
type fakeSettingsStore struct {
	settings *smtp.Settings
	err      error
	setErr   error
}

func (f *fakeSettingsStore) Get(context.Context) (*smtp.Settings, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.settings == nil {
		return nil, smtp.ErrNotFound
	}
	clone := *f.settings
	return &clone, nil
}

func (f *fakeSettingsStore) Set(_ context.Context, s smtp.Settings, _ string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.settings = &s
	return nil
}

// fakeSender captures sent emails or fails on demand.
type fakeSender struct {
	mu    sync.Mutex
	sent  []notifyrender.Email
	fails int // fail this many sends before succeeding
	err   error
}

func (f *fakeSender) Send(_ context.Context, _ smtp.Settings, email notifyrender.Email) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fails > 0 {
		f.fails--
		if f.err != nil {
			return f.err
		}
		return errors.New("smtp unavailable")
	}
	f.sent = append(f.sent, email)
	return nil
}

func (f *fakeSender) sentCopy() []notifyrender.Email {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]notifyrender.Email(nil), f.sent...)
}

func enabledSettings() *smtp.Settings {
	return &smtp.Settings{
		Enabled: true, Host: "smtp.example.com", Port: 587,
		From: "p@example.com", TLSMode: smtp.TLSModeStartTLS,
	}
}

func testWorker(t *testing.T, queue notification.QueueStore, settings smtp.SettingsStore, sender notifysend.Sender) *Worker {
	t.Helper()
	r, err := notifyrender.NewRenderer(notifyrender.Branding{Name: "Test Platform"})
	if err != nil {
		t.Fatal(err)
	}
	return New(Config{Queue: queue, Settings: settings, Renderer: r, Sender: sender})
}

func TestNewWorker_Defaults(t *testing.T) {
	w := New(Config{})
	if w.cfg.PollEvery != DefaultPollEvery || w.cfg.Lease != DefaultLease || w.cfg.MaxAttempts != DefaultMaxAttempts {
		t.Errorf("defaults not applied: %+v", w.cfg)
	}
}

func TestWorker_Drain_SendsImmediate(t *testing.T) {
	queue := &fakeQueueStore{immediate: [][]notification.Notification{
		{{
			ID: 1, Recipient: "a@b.io", Attempts: 1,
			Payload: notification.Payload{Kind: notification.KindAsset, ItemTitle: "R", Actor: "x@y.z"},
		}},
	}}
	sender := &fakeSender{}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, sender)

	w.drain()

	sent := sender.sentCopy()
	if len(sent) != 1 || sent[0].To != "a@b.io" {
		t.Fatalf("expected 1 email to a@b.io, got %+v", sent)
	}
	if len(queue.sent) != 1 || queue.sent[0][0] != 1 {
		t.Errorf("row not marked sent: %+v", queue.sent)
	}
}

func TestWorker_Drain_SendsDigestBatch(t *testing.T) {
	queue := &fakeQueueStore{digests: [][]notification.Notification{
		{
			{
				ID: 1, Recipient: "a@b.io", Digest: true, Attempts: 1,
				Payload: notification.Payload{Kind: notification.KindAsset, ItemTitle: "One", Actor: "x@y.z"},
			},
			{
				ID: 2, Recipient: "a@b.io", Digest: true, Attempts: 1,
				Payload: notification.Payload{Kind: notification.KindComment, ItemTitle: "Two", Actor: "q@y.z"},
			},
		},
	}}
	sender := &fakeSender{}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, sender)

	w.drain()

	sent := sender.sentCopy()
	if len(sent) != 1 {
		t.Fatalf("digest must send one email, got %d", len(sent))
	}
	if len(queue.sent) != 1 || len(queue.sent[0]) != 2 {
		t.Errorf("both digest rows must be marked sent: %+v", queue.sent)
	}
}

func TestWorker_Drain_SMTPUnconfiguredLeavesRowsPending(t *testing.T) {
	queue := &fakeQueueStore{immediate: [][]notification.Notification{{{ID: 1, Recipient: "a@b.io"}}}}
	sender := &fakeSender{}
	w := testWorker(t, queue, &fakeSettingsStore{}, sender)

	w.drain()

	if len(sender.sentCopy()) != 0 || len(queue.sent) != 0 || len(queue.failed) != 0 {
		t.Error("unconfigured SMTP must not touch the queue")
	}
	if len(queue.immediate) != 1 {
		t.Error("row must remain unclaimed")
	}
}

func TestWorker_Drain_SMTPDisabledLeavesRowsPending(t *testing.T) {
	disabled := enabledSettings()
	disabled.Enabled = false
	queue := &fakeQueueStore{immediate: [][]notification.Notification{{{ID: 1}}}}
	w := testWorker(t, queue, &fakeSettingsStore{settings: disabled}, &fakeSender{})

	w.drain()

	if len(queue.immediate) != 1 {
		t.Error("row must remain unclaimed while SMTP is disabled")
	}
}

func TestWorker_Drain_SettingsErrorLeavesRowsPending(t *testing.T) {
	queue := &fakeQueueStore{immediate: [][]notification.Notification{{{ID: 1}}}}
	w := testWorker(t, queue, &fakeSettingsStore{err: errors.New("db down")}, &fakeSender{})

	w.drain()

	if len(queue.immediate) != 1 {
		t.Error("row must remain unclaimed on settings error")
	}
}

func TestWorker_Deliver_RetryOnSendFailure(t *testing.T) {
	queue := &fakeQueueStore{immediate: [][]notification.Notification{
		{{ID: 5, Recipient: "a@b.io", Attempts: 2, Payload: notification.Payload{Kind: notification.KindAsset, ItemTitle: "R"}}},
	}}
	sender := &fakeSender{fails: 1}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, sender)

	w.drain()

	if len(queue.retried) != 1 || queue.retried[0][0] != 5 {
		t.Errorf("expected retry for row 5: %+v", queue.retried)
	}
	if len(queue.failed) != 0 {
		t.Errorf("must not fail before MaxAttempts: %+v", queue.failed)
	}
}

func TestWorker_Deliver_FailAfterMaxAttempts(t *testing.T) {
	queue := &fakeQueueStore{immediate: [][]notification.Notification{
		{{
			ID: 6, Recipient: "a@b.io", Attempts: DefaultMaxAttempts,
			Payload: notification.Payload{Kind: notification.KindAsset, ItemTitle: "R"},
		}},
	}}
	sender := &fakeSender{fails: 1}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, sender)

	w.drain()

	if len(queue.failed) != 1 || queue.failed[0][0] != 6 {
		t.Errorf("expected permanent failure for row 6: %+v", queue.failed)
	}
	if len(queue.retried) != 0 {
		t.Errorf("exhausted row must not retry: %+v", queue.retried)
	}
}

func TestWorker_Deliver_EmptyBatchIsNoop(t *testing.T) {
	// Claims never return empty batches (notification.ErrNoWork instead); deliver guards
	// the impossible case rather than panicking on batch[0].
	queue := &fakeQueueStore{}
	sender := &fakeSender{}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, sender)

	w.deliver(context.Background(), enabledSettings(), nil)

	if len(queue.failed) != 0 || len(queue.retried) != 0 || len(sender.sentCopy()) != 0 {
		t.Error("empty batch must be a no-op")
	}
}

func TestWorker_Resolve_TerminalError(t *testing.T) {
	queue := &fakeQueueStore{}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, &fakeSender{})

	batch := []notification.Notification{{ID: 8, Recipient: "a@b.io", Attempts: 1}}
	w.resolve(context.Background(), batch, errors.New("deterministic"), true)

	if len(queue.failed) != 1 || queue.failed[0][0] != 8 {
		t.Errorf("terminal error must fail the batch: %+v", queue.failed)
	}
}

// TestWorker_Resolve_StoreErrorsLogged asserts that a failing queue store does
// not change how a send failure is routed: a non-terminal failure below the
// attempt ceiling is still scheduled for retry, and a terminal one is still
// marked failed. Routing either way wrongly costs a dropped notification or an
// endless retry, so the store error must be logged and swallowed rather than
// diverting the decision.
func TestWorker_Resolve_StoreErrorsLogged(t *testing.T) {
	queue := &fakeQueueStore{opErr: errors.New("store down")}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, &fakeSender{})

	batch := []notification.Notification{{ID: 9, Recipient: "a@b.io", Attempts: 1}}
	w.resolve(context.Background(), batch, errors.New("send failed"), false)
	w.resolve(context.Background(), batch, errors.New("send failed"), true)

	if want := [][]int64{{9}}; !reflect.DeepEqual(queue.retried, want) {
		t.Errorf("non-terminal failure: Retry calls = %v, want %v", queue.retried, want)
	}
	if want := [][]int64{{9}}; !reflect.DeepEqual(queue.failed, want) {
		t.Errorf("terminal failure: Fail calls = %v, want %v", queue.failed, want)
	}
}

func TestWorker_Deliver_MarkSentError(t *testing.T) {
	queue := &fakeQueueStore{opErr: errors.New("store down"), immediate: [][]notification.Notification{
		{{ID: 1, Recipient: "a@b.io", Attempts: 1, Payload: notification.Payload{Kind: notification.KindAsset, ItemTitle: "R"}}},
	}}
	sender := &fakeSender{}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, sender)

	w.drain() // must log the MarkSent failure, not panic

	if len(sender.sentCopy()) != 1 {
		t.Fatal("email must still have been sent")
	}
}

func TestWorker_StartStop(t *testing.T) {
	queue := &fakeQueueStore{immediate: [][]notification.Notification{
		{{ID: 1, Recipient: "a@b.io", Attempts: 1, Payload: notification.Payload{Kind: notification.KindAsset, ItemTitle: "R"}}},
	}}
	sender := &fakeSender{}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, sender)

	w.Start(context.Background())
	w.Start(context.Background()) // idempotent
	w.Notify()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(sender.sentCopy()) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	w.Stop()
	w.Stop() // idempotent

	if len(sender.sentCopy()) != 1 {
		t.Fatalf("worker did not deliver: %+v", sender.sentCopy())
	}
}

func TestWorker_Drain_PurgeRunsAndThrottles(t *testing.T) {
	// The retention pass runs even when SMTP is unconfigured, and at most
	// once per purge interval.
	queue := &fakeQueueStore{}
	w := testWorker(t, queue, &fakeSettingsStore{}, &fakeSender{})

	w.drain()
	w.drain()

	if queue.purges != 1 {
		t.Errorf("purges = %d; want 1 (run once, then throttled)", queue.purges)
	}
}

// TestWorker_ClaimError asserts that a claim failure ends the drain without
// side effects. A claim error is indistinguishable from an empty queue to
// everything downstream, so the worker must neither send nor mark rows: doing
// either on a store it could not read from would act on notifications it never
// claimed.
func TestWorker_ClaimError(t *testing.T) {
	queue := &fakeQueueStore{claimErr: errors.New("claim boom")}
	sender := &fakeSender{}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, sender)

	w.drain()

	if got := sender.sentCopy(); len(got) != 0 {
		t.Errorf("claim failure still sent %d email(s)", len(got))
	}
	if len(queue.sent)+len(queue.retried)+len(queue.failed) != 0 {
		t.Errorf("claim failure still marked rows: sent=%v retried=%v failed=%v",
			queue.sent, queue.retried, queue.failed)
	}
}

func TestComputeBackoff(t *testing.T) {
	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 30 * time.Second},
		{1, 30 * time.Second},
		{2, time.Minute},
		{3, 2 * time.Minute},
		{7, 32 * time.Minute},
		{50, 32 * time.Minute}, // capped
	}
	for _, tc := range tests {
		if got := computeBackoff(tc.attempts); got != tc.want {
			t.Errorf("computeBackoff(%d) = %v, want %v", tc.attempts, got, tc.want)
		}
	}
}

func TestMaxAttempts(t *testing.T) {
	batch := []notification.Notification{{Attempts: 1}, {Attempts: 4}, {Attempts: 2}}
	if got := maxAttempts(batch); got != 4 {
		t.Errorf("maxAttempts = %d, want 4", got)
	}
	if got := maxAttempts(nil); got != 0 {
		t.Errorf("maxAttempts(nil) = %d, want 0", got)
	}
}

func TestIDs(t *testing.T) {
	batch := []notification.Notification{{ID: 3}, {ID: 9}}
	got := ids(batch)
	if len(got) != 2 || got[0] != 3 || got[1] != 9 {
		t.Errorf("ids = %v", got)
	}
}

// TestWorker_Drain_FooterAndReplyToFromBranding proves the implementor
// branding (#1023) flows through the worker's claim-render-send chain: the
// delivered email carries the about/support footer in both body parts and
// the Reply-To for the sender to emit.
func TestWorker_Drain_FooterAndReplyToFromBranding(t *testing.T) {
	queue := &fakeQueueStore{immediate: [][]notification.Notification{
		{{
			ID: 1, Recipient: "a@b.io", Attempts: 1,
			Payload: notification.Payload{Kind: notification.KindAsset, ItemTitle: "R", Actor: "x@y.z"},
		}},
	}}
	r, err := notifyrender.NewRenderer(notifyrender.Branding{
		Name:           "Test Platform",
		AboutText:      "The ACME data portal delivers curated datasets.",
		SupportContact: "help@example.com",
		ReplyTo:        "support@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{}
	w := New(Config{
		Queue: queue, Settings: &fakeSettingsStore{settings: enabledSettings()},
		Renderer: r, Sender: sender,
	})

	w.drain()

	sent := sender.sentCopy()
	if len(sent) != 1 {
		t.Fatalf("expected 1 email, got %d", len(sent))
	}
	for _, body := range []string{sent[0].HTML, sent[0].Text} {
		for _, want := range []string{"The ACME data portal delivers curated datasets.", "help@example.com"} {
			if !strings.Contains(body, want) {
				t.Errorf("delivered email missing footer content %q", want)
			}
		}
	}
	if sent[0].ReplyTo != "support@example.com" {
		t.Errorf("delivered email ReplyTo = %q; want the branding address", sent[0].ReplyTo)
	}
}
