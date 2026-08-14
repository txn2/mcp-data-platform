package notification

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakePrefsStore returns canned preferences per email.
type fakePrefsStore struct {
	prefs map[string]Prefs
	err   error
}

func (f *fakePrefsStore) Get(_ context.Context, email string) (Prefs, error) {
	if f.err != nil {
		return Prefs{}, f.err
	}
	if p, ok := f.prefs[email]; ok {
		return p, nil
	}
	return DefaultPrefs(email), nil
}

func (f *fakePrefsStore) Set(_ context.Context, email string, _ PrefsUpdate) (Prefs, error) {
	return DefaultPrefs(email), f.err
}

// fakeQueueStore records enqueued notifications and serves canned claims.
type fakeQueueStore struct {
	mu       sync.Mutex
	enqueued []Notification
	enqErr   error

	immediate [][]Notification // successive ClaimImmediate results (nil = ErrNoWork)
	digests   [][]Notification // successive ClaimDigest results (nil = ErrNoWork)
	claimErr  error

	sent    [][]int64
	retried [][]int64
	failed  [][]int64
	purges  int
	opErr   error
}

func (f *fakeQueueStore) Enqueue(_ context.Context, n Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.enqErr != nil {
		return f.enqErr
	}
	f.enqueued = append(f.enqueued, n)
	return nil
}

func (f *fakeQueueStore) ClaimImmediate(_ context.Context, _ time.Duration) (*Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	if len(f.immediate) == 0 || f.immediate[0] == nil {
		if len(f.immediate) > 0 {
			f.immediate = f.immediate[1:]
		}
		return nil, ErrNoWork
	}
	batch := f.immediate[0]
	f.immediate = f.immediate[1:]
	return &batch[0], nil
}

func (f *fakeQueueStore) ClaimDigest(_ context.Context, _ time.Duration) ([]Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	if len(f.digests) == 0 || f.digests[0] == nil {
		if len(f.digests) > 0 {
			f.digests = f.digests[1:]
		}
		return nil, ErrNoWork
	}
	batch := f.digests[0]
	f.digests = f.digests[1:]
	return batch, nil
}

func (f *fakeQueueStore) MarkSent(_ context.Context, ids []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, ids)
	return f.opErr
}

func (f *fakeQueueStore) Retry(_ context.Context, ids []int64, _ string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retried = append(f.retried, ids)
	return f.opErr
}

func (f *fakeQueueStore) Fail(_ context.Context, ids []int64, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, ids)
	return f.opErr
}

func (f *fakeQueueStore) PurgeOld(_ context.Context, _, _ time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purges++
	return 0, f.opErr
}

func (f *fakeQueueStore) enqueuedCopy() []Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Notification(nil), f.enqueued...)
}

func TestEnqueuer_Notify_Immediate(t *testing.T) {
	queue := &fakeQueueStore{}
	e := NewEnqueuer(&fakePrefsStore{}, queue, 13)

	_, err := e.Notify(context.Background(), "USER@Example.com", CategoryShare,
		Payload{Kind: KindAsset, Actor: "owner@example.com", ItemTitle: "Report"})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	got := queue.enqueuedCopy()
	if len(got) != 1 {
		t.Fatalf("expected 1 enqueued, got %d", len(got))
	}
	if got[0].Recipient != "user@example.com" {
		t.Errorf("recipient not normalized: %q", got[0].Recipient)
	}
	if got[0].Digest || !got[0].ScheduledFor.IsZero() {
		t.Errorf("immediate row must not be a digest: %+v", got[0])
	}
}

func TestEnqueuer_Notify_DailySchedulesDigest(t *testing.T) {
	queue := &fakeQueueStore{}
	prefs := &fakePrefsStore{prefs: map[string]Prefs{
		"a@b.io": {Email: "a@b.io", Mode: ModeDaily, SharesEnabled: true, CommentsEnabled: true},
	}}
	e := NewEnqueuer(prefs, queue, 13)
	now := time.Date(2026, 7, 19, 14, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }

	if _, err := e.Notify(context.Background(), "a@b.io", CategoryShare, Payload{Actor: "x@y.z"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	got := queue.enqueuedCopy()
	if len(got) != 1 || !got[0].Digest {
		t.Fatalf("expected 1 digest row: %+v", got)
	}
	want := time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC) // 14:00 is past 13:00, so tomorrow
	if !got[0].ScheduledFor.Equal(want) {
		t.Errorf("scheduled_for = %v, want %v", got[0].ScheduledFor, want)
	}
}

func TestEnqueuer_Notify_Drops(t *testing.T) {
	tests := []struct {
		name      string
		recipient string
		category  string
		payload   Payload
		prefs     map[string]Prefs
	}{
		{name: "empty recipient", recipient: "", category: CategoryShare},
		{
			name: "self notification", recipient: "me@b.io", category: CategoryShare,
			payload: Payload{Actor: "ME@b.io"},
		},
		{
			name: "mode off", recipient: "off@b.io", category: CategoryShare,
			prefs: map[string]Prefs{"off@b.io": {Mode: ModeOff, SharesEnabled: true, CommentsEnabled: true}},
		},
		{
			name: "shares disabled", recipient: "a@b.io", category: CategoryShare,
			prefs: map[string]Prefs{"a@b.io": {Mode: ModeImmediate, SharesEnabled: false, CommentsEnabled: true}},
		},
		{
			name: "comments disabled", recipient: "a@b.io", category: CategoryComment,
			prefs: map[string]Prefs{"a@b.io": {Mode: ModeImmediate, SharesEnabled: true, CommentsEnabled: false}},
		},
		{
			name:      "review queue alert to a recipient who muted everything",
			recipient: "off@b.io", category: CategoryReviewQueue,
			prefs: map[string]Prefs{"off@b.io": {Mode: ModeOff}},
		},
		{name: "unknown category", recipient: "a@b.io", category: "carrier-pigeon"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			queue := &fakeQueueStore{}
			e := NewEnqueuer(&fakePrefsStore{prefs: tc.prefs}, queue, 13)
			if _, err := e.Notify(context.Background(), tc.recipient, tc.category, tc.payload); err != nil {
				t.Fatalf("Notify: %v", err)
			}
			if len(queue.enqueuedCopy()) != 0 {
				t.Errorf("expected drop, got enqueue")
			}
		})
	}
}

// TestEnqueuer_Notify_ReviewQueueHasNoPerCategoryToggle pins the one category
// gated by Mode alone (#803): the operator's recipient list decides who gets
// an operator alert, so muting shares, comments, and mentions must not silence
// it. Turning notifications off entirely still does; that case is in
// TestEnqueuer_Notify_Drops.
func TestEnqueuer_Notify_ReviewQueueHasNoPerCategoryToggle(t *testing.T) {
	queue := &fakeQueueStore{}
	e := NewEnqueuer(&fakePrefsStore{prefs: map[string]Prefs{
		"ops@b.io": {Mode: ModeImmediate, SharesEnabled: false, CommentsEnabled: false, MentionsEnabled: false},
	}}, queue, 13)
	defer e.Close()

	queued, err := e.Notify(context.Background(), "ops@b.io", CategoryReviewQueue,
		Payload{Kind: KindReviewQueue, Review: &ReviewQueue{Pending: 9}})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if !queued {
		t.Fatal("the review-queue alert must reach a recipient who muted the item categories")
	}
	rows := queue.enqueuedCopy()
	if len(rows) != 1 || rows[0].Payload.Review == nil || rows[0].Payload.Review.Pending != 9 {
		t.Fatalf("rollup did not survive the enqueue: %+v", rows)
	}
}

func TestEnqueuer_Notify_NilEnqueuer(t *testing.T) {
	var e *Enqueuer
	if _, err := e.Notify(context.Background(), "a@b.io", CategoryShare, Payload{}); err != nil {
		t.Fatalf("nil enqueuer must drop silently: %v", err)
	}
	e.Close() // nil-safe
}

func TestEnqueuer_Notify_PrefsError(t *testing.T) {
	e := NewEnqueuer(&fakePrefsStore{err: errors.New("db down")}, &fakeQueueStore{}, 13)
	if _, err := e.Notify(context.Background(), "a@b.io", CategoryShare, Payload{}); err == nil {
		t.Fatal("expected prefs error")
	}
}

func TestEnqueuer_Notify_EnqueueError(t *testing.T) {
	e := NewEnqueuer(&fakePrefsStore{}, &fakeQueueStore{enqErr: errors.New("insert failed")}, 13)
	if _, err := e.Notify(context.Background(), "a@b.io", CategoryShare, Payload{}); err == nil {
		t.Fatal("expected enqueue error")
	}
}

func TestEnqueuer_Notify_PerActorRateLimit(t *testing.T) {
	queue := &fakeQueueStore{}
	e := NewEnqueuer(&fakePrefsStore{}, queue, 13)
	defer e.Close()

	// Well past the burst allowance: excess events drop without error.
	for i := range actorBurst * 2 {
		_, err := e.Notify(context.Background(), fmt.Sprintf("r%d@b.io", i), CategoryShare,
			Payload{Actor: "spammer@b.io"})
		if err != nil {
			t.Fatalf("Notify %d: %v", i, err)
		}
	}
	got := len(queue.enqueuedCopy())
	if got > actorBurst {
		t.Errorf("enqueued %d, want at most the burst allowance %d", got, actorBurst)
	}
	if got == 0 {
		t.Error("the allowance itself must go through")
	}
}

func TestNextDigestTime(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		hour int
		want time.Time
	}{
		{
			name: "before hour same day",
			now:  time.Date(2026, 7, 19, 8, 30, 0, 0, time.UTC),
			hour: 13,
			want: time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC),
		},
		{
			name: "after hour next day",
			now:  time.Date(2026, 7, 19, 14, 0, 0, 0, time.UTC),
			hour: 13,
			want: time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC),
		},
		{
			name: "exactly at hour rolls to next day",
			now:  time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC),
			hour: 13,
			want: time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC),
		},
		{
			name: "midnight hour",
			now:  time.Date(2026, 7, 19, 0, 0, 1, 0, time.UTC),
			hour: 0,
			want: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NextDigestTime(tc.now, tc.hour); !got.Equal(tc.want) {
				t.Errorf("NextDigestTime(%v, %d) = %v, want %v", tc.now, tc.hour, got, tc.want)
			}
		})
	}
}

func TestNotifyFanout_ChargesOneTokenAndReportsQueued(t *testing.T) {
	queue := &fakeQueueStore{}
	e := NewEnqueuer(&fakePrefsStore{}, queue, 13)
	defer e.Close()

	// More recipients than the per-actor burst: a fan-out the actor did not
	// choose costs one token, so all of them are queued.
	recipients := make([]string, 0, actorBurst+10)
	for i := range actorBurst + 10 {
		recipients = append(recipients, fmt.Sprintf("r%02d@b.io", i))
	}
	sent := e.NotifyFanout(context.Background(), recipients, CategoryComment, Payload{Actor: "a@b.io"})

	if len(sent) != len(recipients) {
		t.Fatalf("expected every recipient queued, got %d of %d", len(sent), len(recipients))
	}
	if len(queue.enqueued) != len(recipients) {
		t.Fatalf("expected %d rows, got %d", len(recipients), len(queue.enqueued))
	}
	// The actor's budget survives, so an address they DO choose still goes out.
	queued, err := e.Notify(context.Background(), "chosen@b.io", CategoryShare, Payload{Actor: "a@b.io"})
	if err != nil || !queued {
		t.Fatalf("the fan-out spent the actor's rate limit: queued=%v err=%v", queued, err)
	}
}

func TestNotifyFanout_TruncatesAtTheCap(t *testing.T) {
	queue := &fakeQueueStore{}
	e := NewEnqueuer(&fakePrefsStore{}, queue, 13)
	defer e.Close()

	recipients := make([]string, 0, maxFanout+5)
	for i := range maxFanout + 5 {
		recipients = append(recipients, fmt.Sprintf("r%03d@b.io", i))
	}
	sent := e.NotifyFanout(context.Background(), recipients, CategoryComment, Payload{Actor: "a@b.io"})

	if len(sent) != maxFanout {
		t.Fatalf("expected the fan-out capped at %d, got %d", maxFanout, len(sent))
	}
}

func TestNotifyFanout_SkipsRecipientsWhoOptedOut(t *testing.T) {
	queue := &fakeQueueStore{}
	e := NewEnqueuer(&fakePrefsStore{prefs: map[string]Prefs{
		"a@b.io": {Email: "a@b.io", Mode: ModeOff},
		"c@b.io": {Email: "c@b.io", Mode: ModeOff},
	}}, queue, 13)
	defer e.Close()

	sent := e.NotifyFanout(context.Background(), []string{"a@b.io", "c@b.io"}, CategoryComment, Payload{Actor: "x@b.io"})
	if len(sent) != 0 || len(queue.enqueued) != 0 {
		t.Fatalf("opted-out recipients must not be queued or reported: %+v %+v", sent, queue.enqueued)
	}
}

func TestNotifyFanout_NilAndEmpty(_ *testing.T) {
	var nilEnqueuer *Enqueuer
	nilEnqueuer.NotifyFanout(context.Background(), []string{"a@b.io"}, CategoryComment, Payload{})

	e := NewEnqueuer(&fakePrefsStore{}, &fakeQueueStore{}, 13)
	defer e.Close()
	e.NotifyFanout(context.Background(), nil, CategoryComment, Payload{})
}

// TestEnqueuer_Notify_ScriptRunHasNoPerCategoryToggle pins the second category
// gated by Mode alone (#1286). Its recipients are the people accountable for
// an automation — its owner and the administrator who approved the version —
// so muting shares, comments, and mentions must not silence the news that a
// scheduled script has stopped producing. Turning notifications off entirely
// still does.
func TestEnqueuer_Notify_ScriptRunHasNoPerCategoryToggle(t *testing.T) {
	queue := &fakeQueueStore{}
	e := NewEnqueuer(&fakePrefsStore{prefs: map[string]Prefs{
		"jane@b.io": {Mode: ModeImmediate, SharesEnabled: false, CommentsEnabled: false, MentionsEnabled: false},
	}}, queue, 13)
	defer e.Close()

	queued, err := e.Notify(context.Background(), "jane@b.io", CategoryScriptRun,
		Payload{Kind: KindScriptRun, ItemID: "dpx_1", ItemTitle: "daily-sales", Message: "boom"})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if !queued {
		t.Fatal("a failed scheduled run must reach the people accountable for it")
	}
	rows := queue.enqueuedCopy()
	if len(rows) != 1 || rows[0].Payload.ItemID != "dpx_1" {
		t.Fatalf("the run reference did not survive the enqueue: %+v", rows)
	}
}
