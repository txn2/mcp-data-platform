package postgres

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/session"
)

// newTestBroadcaster builds a Broadcaster without opening a real
// pq.Listener — the listener-driven goroutine is not exercised by
// these tests; we drive dispatchPayload directly to validate the
// decode + local-fan-out path without a live postgres.
func newTestBroadcaster(t *testing.T) (*Broadcaster, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	logger := slog.Default()
	b := &Broadcaster{
		// listener is intentionally nil — these tests never touch it.
		db:      db,
		channel: DefaultNotifyChannel,
		local:   session.NewMemoryBroadcaster(logger),
		done:    make(chan struct{}),
		logger:  logger,
	}
	cleanup := func() {
		// Mark closed so Publish returns ErrBroadcasterClosed if any
		// straggler call is made post-cleanup, then release subs.
		b.closed.Store(true)
		_ = b.local.Close()
		_ = db.Close()
	}
	return b, mock, cleanup
}

func TestBroadcaster_Publish_IssuesPgNotify(t *testing.T) {
	b, mock, cleanup := newTestBroadcaster(t)
	defer cleanup()

	expected := regexp.QuoteMeta("SELECT pg_notify($1, $2)")
	mock.ExpectExec(expected).
		WithArgs(DefaultNotifyChannel, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := b.Publish(context.Background(), session.Event{
		Method: "notifications/tools/list_changed",
	})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBroadcaster_Publish_PgNotifyError(t *testing.T) {
	b, mock, cleanup := newTestBroadcaster(t)
	defer cleanup()

	mock.ExpectExec("SELECT pg_notify").
		WillReturnError(errors.New("connection lost"))

	err := b.Publish(context.Background(), session.Event{Method: "x"})
	assert.Error(t, err)
}

func TestBroadcaster_Publish_RejectsOversizedPayload(t *testing.T) {
	b, _, cleanup := newTestBroadcaster(t)
	defer cleanup()

	// Build params that serialize to >7500 bytes so the payload-too-large
	// guard fires before any DB call is attempted.
	big := make([]byte, 9000)
	for i := range big {
		big[i] = 'x'
	}
	err := b.Publish(context.Background(), session.Event{
		Method: "x",
		Params: map[string]any{"blob": string(big)},
	})
	assert.ErrorContains(t, err, "payload too large")
}

func TestBroadcaster_Publish_AfterCloseReturnsClosed(t *testing.T) {
	b, _, cleanup := newTestBroadcaster(t)
	defer cleanup()

	b.closed.Store(true)
	err := b.Publish(context.Background(), session.Event{Method: "x"})
	assert.ErrorIs(t, err, session.ErrBroadcasterClosed)
}

func TestBroadcaster_DispatchPayload_FansOutToLocalSubscribers(t *testing.T) {
	b, _, cleanup := newTestBroadcaster(t)
	defer cleanup()

	sub := b.Subscribe(context.Background(), "session-x")
	defer sub.Close()

	b.dispatchPayload(`{"method":"notifications/tools/list_changed","params":{"k":"v"}}`)

	select {
	case ev := <-sub.Events():
		assert.Equal(t, "notifications/tools/list_changed", ev.Method)
		assert.Equal(t, "v", ev.Params["k"])
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive event")
	}
}

func TestBroadcaster_DispatchPayload_IgnoresInvalidJSON(t *testing.T) {
	b, _, cleanup := newTestBroadcaster(t)
	defer cleanup()

	sub := b.Subscribe(context.Background(), "session-x")
	defer sub.Close()

	b.dispatchPayload("not-json")

	select {
	case ev := <-sub.Events():
		t.Fatalf("expected no event for invalid payload, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// Expected: invalid payloads are dropped silently (with warn log).
	}
}

func TestBroadcaster_SubscriberCount(t *testing.T) {
	b, _, cleanup := newTestBroadcaster(t)
	defer cleanup()

	assert.Equal(t, 0, b.SubscriberCount())
	sub := b.Subscribe(context.Background(), "s")
	assert.Equal(t, 1, b.SubscriberCount())
	sub.Close()
	assert.Equal(t, 0, b.SubscriberCount())
}

func TestListenerEventName(t *testing.T) {
	cases := []struct {
		ev   pq.ListenerEventType
		want string
	}{
		{pq.ListenerEventConnected, "connected"},
		{pq.ListenerEventDisconnected, "disconnected"},
		{pq.ListenerEventReconnected, "reconnected"},
		{pq.ListenerEventConnectionAttemptFailed, "connection_attempt_failed"},
		{pq.ListenerEventType(99), "unknown(99)"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, listenerEventName(tc.ev))
	}
}

// TestBroadcaster_Close_RealPath exercises the post-listener portion
// of the production Close(): CAS → (listener.Close skipped via nil
// guard) → drain `<-b.done` (returns immediately because we
// pre-closed it) → local.Close. It does NOT exercise the real
// listener.Close path or the 2-second drain-timeout-with-warning
// branch (broadcast.go's `case <-time.After(2 * time.Second)` arm)
// — those require a fake pq.Listener that is not implemented here.
// What this test DOES prove: Close is idempotent, post-Close Publish
// returns ErrBroadcasterClosed, and post-Close Subscribe returns a
// closed-immediate subscription — the same guarantees the
// MemoryBroadcaster paired backend makes.
func TestBroadcaster_Close_RealPath(t *testing.T) {
	b, _, _ := newTestBroadcaster(t)
	// Simulate run() having exited so Close's `<-b.done` returns
	// immediately. listener is nil; production Close()'s nil-listener
	// guard inside Close lets us drive the rest of the sequence
	// without faking the full pq surface.
	close(b.done)

	// Real Close — exercises the full sequence.
	require.NoError(t, b.Close())

	// Idempotent: second Close returns nil, no panic.
	require.NoError(t, b.Close())

	// Publish after Close returns ErrBroadcasterClosed.
	err := b.Publish(context.Background(), session.Event{Method: "x"})
	assert.ErrorIs(t, err, session.ErrBroadcasterClosed)

	// Subscribe after Close returns a closed-immediate sub.
	sub := b.Subscribe(context.Background(), "s")
	if _, ok := <-sub.Events(); ok {
		t.Error("Subscribe after Close: expected closed channel")
	}
}

// TestBroadcaster_DispatchPayload_NilParams covers the parse path:
// dispatchPayload must tolerate a NOTIFY payload whose JSON omits
// the optional `params` field entirely, forwarding an Event whose
// Params is nil to local subscribers.
func TestBroadcaster_DispatchPayload_NilParams(t *testing.T) {
	b, _, cleanup := newTestBroadcaster(t)
	defer cleanup()

	sub := b.Subscribe(context.Background(), "s")
	defer sub.Close()

	b.dispatchPayload(`{"method":"notifications/tools/list_changed"}`)

	select {
	case ev := <-sub.Events():
		assert.Equal(t, "notifications/tools/list_changed", ev.Method)
		assert.Nil(t, ev.Params)
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive event")
	}
}

// A reconnect on a broadcaster built WithReconnectEvent reaches its
// subscribers as that event, in order with the notifications after it, so a
// subscriber whose state is carried only by these events can re-read it (#1902).
func TestBroadcaster_Reconnected_PublishesConfiguredEvent(t *testing.T) {
	b, _, cleanup := newTestBroadcaster(t)
	defer cleanup()
	WithReconnectEvent("platform/reload/resync")(b)

	sub := b.Subscribe(context.Background(), "reload-bus")
	defer sub.Close()

	b.reconnected()
	b.dispatchPayload(`{"method":"platform/reload/persona"}`)

	for _, want := range []string{"platform/reload/resync", "platform/reload/persona"} {
		select {
		case ev := <-sub.Events():
			assert.Equal(t, want, ev.Method)
			assert.Empty(t, ev.Params)
		case <-time.After(time.Second):
			t.Fatalf("subscriber did not receive %s", want)
		}
	}
}

// Without the option a reconnect publishes nothing: the client-facing
// broadcaster's subscribers catch up on their next list call. The marker sent
// after it is the first thing the subscriber receives.
func TestBroadcaster_Reconnected_WithoutEventPublishesNothing(t *testing.T) {
	b, _, cleanup := newTestBroadcaster(t)
	defer cleanup()

	sub := b.Subscribe(context.Background(), "session-x")
	defer sub.Close()

	b.reconnected()
	b.dispatchPayload(`{"method":"notifications/tools/list_changed"}`)

	select {
	case ev := <-sub.Events():
		assert.Equal(t, "notifications/tools/list_changed", ev.Method)
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive the marker")
	}
}

// The run loop turns lib/pq's nil notification into the reconnect event and
// forwards the notification after it.
func TestBroadcaster_Run_NilNotificationIsReconnect(t *testing.T) {
	b, _, cleanup := newTestBroadcaster(t)
	defer cleanup()
	b.reconnectEvent = "platform/reload/resync"

	sub := b.Subscribe(context.Background(), "reload-bus")
	defer sub.Close()

	ch := make(chan *pq.Notification, 2)
	ch <- nil
	ch <- &pq.Notification{Extra: `{"method":"platform/reload/apikey"}`}
	close(ch)
	b.consume(ch)

	for _, want := range []string{"platform/reload/resync", "platform/reload/apikey"} {
		select {
		case ev := <-sub.Events():
			assert.Equal(t, want, ev.Method)
		case <-time.After(time.Second):
			t.Fatalf("subscriber did not receive %s", want)
		}
	}
}

// An event addressed to one session carries its address through NOTIFY to
// every replica's subscribers, which is what lets the session's own stream
// alone deliver it (#1946).
func TestBroadcaster_CarriesTheSessionAddress(t *testing.T) {
	b, mock, cleanup := newTestBroadcaster(t)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_notify($1, $2)")).
		WithArgs(DefaultNotifyChannel, `{"method":"notifications/tools/list_changed","session_id":"sess-a"}`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	require.NoError(t, b.Publish(context.Background(), session.Event{Method: "notifications/tools/list_changed", SessionID: "sess-a"}))
	assert.NoError(t, mock.ExpectationsWereMet())

	sub := b.Subscribe(context.Background(), "sess-a")
	defer sub.Close()
	b.dispatchPayload(`{"method":"notifications/tools/list_changed","session_id":"sess-a"}`)
	select {
	case ev := <-sub.Events():
		assert.Equal(t, "sess-a", ev.SessionID)
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive event")
	}
}
