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

	err := e.Notify(context.Background(), "USER@Example.com", CategoryShare,
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

	if err := e.Notify(context.Background(), "a@b.io", CategoryShare, Payload{Actor: "x@y.z"}); err != nil {
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
		{name: "unknown category", recipient: "a@b.io", category: "carrier-pigeon"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			queue := &fakeQueueStore{}
			e := NewEnqueuer(&fakePrefsStore{prefs: tc.prefs}, queue, 13)
			if err := e.Notify(context.Background(), tc.recipient, tc.category, tc.payload); err != nil {
				t.Fatalf("Notify: %v", err)
			}
			if len(queue.enqueuedCopy()) != 0 {
				t.Errorf("expected drop, got enqueue")
			}
		})
	}
}

func TestEnqueuer_Notify_NilEnqueuer(t *testing.T) {
	var e *Enqueuer
	if err := e.Notify(context.Background(), "a@b.io", CategoryShare, Payload{}); err != nil {
		t.Fatalf("nil enqueuer must drop silently: %v", err)
	}
	e.Close() // nil-safe
}

func TestEnqueuer_Notify_PrefsError(t *testing.T) {
	e := NewEnqueuer(&fakePrefsStore{err: errors.New("db down")}, &fakeQueueStore{}, 13)
	if err := e.Notify(context.Background(), "a@b.io", CategoryShare, Payload{}); err == nil {
		t.Fatal("expected prefs error")
	}
}

func TestEnqueuer_Notify_EnqueueError(t *testing.T) {
	e := NewEnqueuer(&fakePrefsStore{}, &fakeQueueStore{enqErr: errors.New("insert failed")}, 13)
	if err := e.Notify(context.Background(), "a@b.io", CategoryShare, Payload{}); err == nil {
		t.Fatal("expected enqueue error")
	}
}

func TestEnqueuer_Notify_PerActorRateLimit(t *testing.T) {
	queue := &fakeQueueStore{}
	e := NewEnqueuer(&fakePrefsStore{}, queue, 13)
	defer e.Close()

	// Well past the burst allowance: excess events drop without error.
	for i := range actorBurst * 2 {
		err := e.Notify(context.Background(), fmt.Sprintf("r%d@b.io", i), CategoryShare,
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
