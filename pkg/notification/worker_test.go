package notification

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeSettingsStore serves canned SMTP settings.
type fakeSettingsStore struct {
	settings *SMTPSettings
	err      error
	setErr   error
}

func (f *fakeSettingsStore) GetSMTP(context.Context) (*SMTPSettings, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.settings == nil {
		return nil, ErrNotFound
	}
	clone := *f.settings
	return &clone, nil
}

func (f *fakeSettingsStore) SetSMTP(_ context.Context, s SMTPSettings, _ string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.settings = &s
	return nil
}

// fakeSender captures sent emails or fails on demand.
type fakeSender struct {
	mu    sync.Mutex
	sent  []Email
	fails int // fail this many sends before succeeding
	err   error
}

func (f *fakeSender) Send(_ context.Context, _ SMTPSettings, email Email) error {
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

func (f *fakeSender) sentCopy() []Email {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Email(nil), f.sent...)
}

func enabledSettings() *SMTPSettings {
	return &SMTPSettings{
		Enabled: true, Host: "smtp.example.com", Port: 587,
		From: "p@example.com", TLSMode: TLSModeStartTLS,
	}
}

func testWorker(t *testing.T, queue QueueStore, settings SettingsStore, sender Sender) *Worker {
	t.Helper()
	r, err := NewRenderer(Branding{Name: "Test Platform"})
	if err != nil {
		t.Fatal(err)
	}
	return NewWorker(WorkerConfig{Queue: queue, Settings: settings, Renderer: r, Sender: sender})
}

func TestNewWorker_Defaults(t *testing.T) {
	w := NewWorker(WorkerConfig{})
	if w.cfg.PollEvery != DefaultPollEvery || w.cfg.Lease != DefaultLease || w.cfg.MaxAttempts != DefaultMaxAttempts {
		t.Errorf("defaults not applied: %+v", w.cfg)
	}
}

func TestWorker_Drain_SendsImmediate(t *testing.T) {
	queue := &fakeQueueStore{immediate: [][]Notification{
		{{
			ID: 1, Recipient: "a@b.io", Attempts: 1,
			Payload: Payload{Kind: KindAsset, ItemTitle: "R", Actor: "x@y.z"},
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
	queue := &fakeQueueStore{digests: [][]Notification{
		{
			{
				ID: 1, Recipient: "a@b.io", Digest: true, Attempts: 1,
				Payload: Payload{Kind: KindAsset, ItemTitle: "One", Actor: "x@y.z"},
			},
			{
				ID: 2, Recipient: "a@b.io", Digest: true, Attempts: 1,
				Payload: Payload{Kind: KindComment, ItemTitle: "Two", Actor: "q@y.z"},
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
	queue := &fakeQueueStore{immediate: [][]Notification{{{ID: 1, Recipient: "a@b.io"}}}}
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
	queue := &fakeQueueStore{immediate: [][]Notification{{{ID: 1}}}}
	w := testWorker(t, queue, &fakeSettingsStore{settings: disabled}, &fakeSender{})

	w.drain()

	if len(queue.immediate) != 1 {
		t.Error("row must remain unclaimed while SMTP is disabled")
	}
}

func TestWorker_Drain_SettingsErrorLeavesRowsPending(t *testing.T) {
	queue := &fakeQueueStore{immediate: [][]Notification{{{ID: 1}}}}
	w := testWorker(t, queue, &fakeSettingsStore{err: errors.New("db down")}, &fakeSender{})

	w.drain()

	if len(queue.immediate) != 1 {
		t.Error("row must remain unclaimed on settings error")
	}
}

func TestWorker_Deliver_RetryOnSendFailure(t *testing.T) {
	queue := &fakeQueueStore{immediate: [][]Notification{
		{{ID: 5, Recipient: "a@b.io", Attempts: 2, Payload: Payload{Kind: KindAsset, ItemTitle: "R"}}},
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
	queue := &fakeQueueStore{immediate: [][]Notification{
		{{
			ID: 6, Recipient: "a@b.io", Attempts: DefaultMaxAttempts,
			Payload: Payload{Kind: KindAsset, ItemTitle: "R"},
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
	// Claims never return empty batches (ErrNoWork instead); deliver guards
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

	batch := []Notification{{ID: 8, Recipient: "a@b.io", Attempts: 1}}
	w.resolve(context.Background(), batch, errors.New("deterministic"), true)

	if len(queue.failed) != 1 || queue.failed[0][0] != 8 {
		t.Errorf("terminal error must fail the batch: %+v", queue.failed)
	}
}

func TestWorker_Resolve_StoreErrorsLogged(t *testing.T) {
	queue := &fakeQueueStore{opErr: errors.New("store down")}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, &fakeSender{})

	batch := []Notification{{ID: 9, Recipient: "a@b.io", Attempts: 1}}
	w.resolve(context.Background(), batch, errors.New("send failed"), false)
	w.resolve(context.Background(), batch, errors.New("send failed"), true)
	// Must not panic; errors are logged.
}

func TestWorker_Deliver_MarkSentError(t *testing.T) {
	queue := &fakeQueueStore{opErr: errors.New("store down"), immediate: [][]Notification{
		{{ID: 1, Recipient: "a@b.io", Attempts: 1, Payload: Payload{Kind: KindAsset, ItemTitle: "R"}}},
	}}
	sender := &fakeSender{}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, sender)

	w.drain() // must log the MarkSent failure, not panic

	if len(sender.sentCopy()) != 1 {
		t.Fatal("email must still have been sent")
	}
}

func TestWorker_StartStop(t *testing.T) {
	queue := &fakeQueueStore{immediate: [][]Notification{
		{{ID: 1, Recipient: "a@b.io", Attempts: 1, Payload: Payload{Kind: KindAsset, ItemTitle: "R"}}},
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

func TestWorker_ClaimError(t *testing.T) {
	queue := &fakeQueueStore{claimErr: errors.New("claim boom")}
	w := testWorker(t, queue, &fakeSettingsStore{settings: enabledSettings()}, &fakeSender{})

	w.drain() // must not panic or loop forever
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
	batch := []Notification{{Attempts: 1}, {Attempts: 4}, {Attempts: 2}}
	if got := maxAttempts(batch); got != 4 {
		t.Errorf("maxAttempts = %d, want 4", got)
	}
	if got := maxAttempts(nil); got != 0 {
		t.Errorf("maxAttempts(nil) = %d, want 0", got)
	}
}

func TestIDs(t *testing.T) {
	batch := []Notification{{ID: 3}, {ID: 9}}
	got := ids(batch)
	if len(got) != 2 || got[0] != 3 || got[1] != 9 {
		t.Errorf("ids = %v", got)
	}
}
