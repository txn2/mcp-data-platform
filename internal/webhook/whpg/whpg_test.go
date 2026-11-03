package whpg

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// fakeListener hands out the notifications sent on its channel; a wait with
// none times out with its context.
type fakeListener struct {
	ch     chan postgres.Notification
	closed chan struct{}
	once   sync.Once
	err    error
}

func newListener() *fakeListener {
	return &fakeListener{ch: make(chan postgres.Notification, 16), closed: make(chan struct{})}
}

func (l *fakeListener) Wait(ctx context.Context) (postgres.Notification, error) {
	if l.err != nil {
		return postgres.Notification{}, l.err
	}
	select {
	case n := <-l.ch:
		return n, nil
	case <-ctx.Done():
		return postgres.Notification{}, ctx.Err() //nolint:wrapcheck // a fake's deadline, read by its type
	}
}

func (l *fakeListener) Close() { l.once.Do(func() { close(l.closed) }) }

// fakeDB serves one listener and an outbox of rows.
type fakeDB struct {
	mu        sync.Mutex
	listener  *fakeListener
	listenErr error
	listened  [][]string
	rows      []postgres.OutboxRow
	drains    int
	drained   chan struct{}
}

func (d *fakeDB) Listen(_ context.Context, connection string, channels []string) (Listener, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.listened = append(d.listened, append([]string{connection}, channels...))
	if d.listenErr != nil {
		return nil, d.listenErr
	}
	return d.listener, nil
}

func (d *fakeDB) DrainOutbox(_ context.Context, _, _ string, limit int, write func([]postgres.OutboxRow) error) (int, error) {
	d.mu.Lock()
	n := min(limit, len(d.rows))
	batch := append([]postgres.OutboxRow(nil), d.rows[:n]...)
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.drains++
		d.mu.Unlock()
		if d.drained != nil {
			select {
			case d.drained <- struct{}{}:
			default:
			}
		}
	}()
	if n == 0 {
		return 0, nil
	}
	if err := write(batch); err != nil {
		return 0, err
	}
	d.mu.Lock()
	d.rows = d.rows[n:]
	d.mu.Unlock()
	return n, nil
}

type fakeWriter struct {
	mu     sync.Mutex
	events []whevent.Event
	err    error
}

func (w *fakeWriter) Write(_ context.Context, _ whsource.Source, events []whevent.Event) error {
	if w.err != nil {
		return w.err
	}
	w.mu.Lock()
	w.events = append(w.events, events...)
	w.mu.Unlock()
	return nil
}

func (w *fakeWriter) all() []whevent.Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]whevent.Event(nil), w.events...)
}

type fakeRejections struct {
	mu  sync.Mutex
	got []whstore.Rejection
}

func (r *fakeRejections) RecordRejections(_ context.Context, rs []whstore.Rejection) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, rs...)
	return nil
}

type fakeStates struct {
	mu                  sync.Mutex
	listening, stopped  []string
	delivered, rejected int64
	failed              []string
	failedCh            chan struct{}
	// filed is signaled after each delivery's counts, the last thing a
	// delivery records.
	filed chan struct{}
}

func (s *fakeStates) Listening(_ context.Context, source, replica string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listening = append(s.listening, source+"@"+replica)
	return nil
}

func (s *fakeStates) Stopped(_ context.Context, source, replica string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = append(s.stopped, source+"@"+replica)
	return nil
}

func (s *fakeStates) Filed(_ context.Context, _ string, delivered, rejected int64, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delivered += delivered
	s.rejected += rejected
	if s.filed != nil {
		s.filed <- struct{}{}
	}
	return nil
}

func (s *fakeStates) Failed(_ context.Context, _, msg string, _ time.Time) error {
	s.mu.Lock()
	s.failed = append(s.failed, msg)
	s.mu.Unlock()
	if s.failedCh != nil {
		s.failedCh <- struct{}{}
	}
	return nil
}

type fakeSources struct {
	mu   sync.Mutex
	list []whsource.Source
}

func (f *fakeSources) List(context.Context) ([]whsource.Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]whsource.Source(nil), f.list...), nil
}

func (f *fakeSources) set(list ...whsource.Source) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.list = list
}

// locks hands out one lock per source, held until released.
type locks struct {
	mu   sync.Mutex
	held map[string]bool
}

func (l *locks) take(_ context.Context, source string) (release func(), ok bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held == nil {
		l.held = map[string]bool{}
	}
	if l.held[source] {
		return nil, false, nil
	}
	l.held[source] = true
	return func() { l.mu.Lock(); delete(l.held, source); l.mu.Unlock() }, true, nil
}

func (l *locks) isHeld(source string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held[source]
}

func outboxSrc() whsource.Source {
	return whsource.Source{Name: "orders-outbox", Connection: "scratch", Enabled: true, Config: whsource.Config{
		Input: whsource.InputPostgres, Postgres: &whsource.Postgres{Connection: "orders", Mode: whsource.PostgresOutbox, Table: "public.outbox", MaxPayloadBytes: 64},
	}}
}

func notifySrc() whsource.Source {
	return whsource.Source{Name: "order-events", Connection: "scratch", Enabled: true, Config: whsource.Config{
		Input: whsource.InputPostgres, Postgres: &whsource.Postgres{Connection: "orders", Mode: whsource.PostgresNotify, Channels: []string{"order_events"}, MaxPayloadBytes: 64},
	}}
}

type harness struct {
	m       *Manager
	db      *fakeDB
	writer  *fakeWriter
	rejects *fakeRejections
	states  *fakeStates
	sources *fakeSources
	locks   *locks
}

func newHarness(t *testing.T, srcs ...whsource.Source) *harness {
	t.Helper()
	h := &harness{
		db: &fakeDB{listener: newListener(), drained: make(chan struct{}, 16)}, writer: &fakeWriter{},
		rejects: &fakeRejections{}, states: &fakeStates{failedCh: make(chan struct{}, 4), filed: make(chan struct{}, 16)}, sources: &fakeSources{list: srcs}, locks: &locks{},
	}
	h.m = New(Config{
		Sources: h.sources, Database: func() Database { return h.db }, Writer: h.writer, Rejections: h.rejects,
		States: h.states, Lock: h.locks.take, Replica: "r1", Tick: time.Hour,
		Now: func() time.Time { return time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC) },
	})
	require.NotNil(t, h.m)
	return h
}

func wait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestNew_MissingDependency(t *testing.T) {
	assert.Nil(t, New(Config{}))
	var m *Manager
	m.Start(context.Background())
	m.Stop()
}

// An outbox source drains at start and on every notification, files each
// row as an event under the row's id, refuses a row past the size bound, and
// stops listening when its source is removed.
func TestOutbox_DrainsAtStartAndOnNotification(t *testing.T) {
	h := newHarness(t, outboxSrc())
	created := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	h.db.rows = []postgres.OutboxRow{
		{ID: 1, Table: "public.orders", Op: "INSERT", Payload: `{"id":7,"total":12.5}`, CreatedAt: created},
		{ID: 2, Table: "public.orders", Op: "UPDATE", Payload: `{"note":"` + strings.Repeat("x", 80) + `"}`, CreatedAt: created},
		{ID: 9, Table: strings.Repeat("t", 70), Op: "INSERT", Payload: `{}`, CreatedAt: created},
	}
	require.NoError(t, h.m.Pass(context.Background()))
	wait(t, h.states.filed, "the drain at start")

	events := h.writer.all()
	require.Len(t, events, 1)
	assert.Equal(t, "outbox-1", events[0].EventID)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(events[0].Payload, &doc))
	assert.Equal(t, "public.orders", doc["table"])
	assert.Equal(t, "INSERT", doc["op"])
	assert.Equal(t, map[string]any{"id": json.Number("7"), "total": json.Number("12.5")}, numbers(t, events[0].Payload)["row"])
	require.Len(t, h.rejects.got, 2, "a row whose table column alone passes the bound is refused too")
	assert.Equal(t, "too_large", h.rejects.got[0].Outcome)
	assert.Equal(t, []string{"outbox-2"}, h.rejects.got[0].EventIDs)
	assert.Equal(t, []string{"outbox-9"}, h.rejects.got[1].EventIDs)
	assert.Contains(t, h.rejects.got[0].Reason, "more than max_payload_bytes (64)")
	assert.NotContains(t, h.rejects.got[0].Reason, "xxxx", "a refusal never repeats the payload")
	assert.NotContains(t, h.rejects.got[0].Reason, "public.orders", "a refusal names the row by its id alone")
	assert.Equal(t, [][]string{{"orders", "public.outbox"}}, h.db.listened, "an outbox listens on its table's channel")

	h.db.mu.Lock()
	h.db.rows = append(h.db.rows, postgres.OutboxRow{ID: 3, Table: "public.orders", Op: "DELETE", Payload: `{"id":7}`, CreatedAt: created})
	h.db.mu.Unlock()
	h.db.listener.ch <- postgres.Notification{Channel: "public.outbox"}
	wait(t, h.states.filed, "the drain on a notification")
	assert.Equal(t, "outbox-3", h.writer.all()[1].EventID)

	assert.True(t, h.locks.isHeld("orders-outbox"))
	require.NoError(t, h.m.Pass(context.Background()), "a running listener is left alone")
	h.sources.set()
	require.NoError(t, h.m.Pass(context.Background()))
	h.m.Stop()
	assert.False(t, h.locks.isHeld("orders-outbox"), "a stopped listener releases its lock")
	h.states.mu.Lock()
	defer h.states.mu.Unlock()
	assert.Equal(t, []string{"orders-outbox@r1"}, h.states.listening)
	assert.Equal(t, []string{"orders-outbox@r1"}, h.states.stopped)
	assert.Equal(t, int64(2), h.states.delivered)
	assert.Equal(t, int64(2), h.states.rejected)
}

func numbers(t *testing.T, b []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	var out map[string]any
	require.NoError(t, dec.Decode(&out))
	return out
}

// A notify source files each notification, JSON as JSON and other text as a
// string, gathers a burst into one write, and refuses an oversized payload.
func TestNotify_FilesEachNotification(t *testing.T) {
	h := newHarness(t, notifySrc())
	h.db.listener.ch <- postgres.Notification{Channel: "order_events", Payload: `{"order":7}`, PID: 42}
	h.db.listener.ch <- postgres.Notification{Channel: "order_events", Payload: "plain text"}
	h.db.listener.ch <- postgres.Notification{Channel: "order_events", Payload: strings.Repeat("y", 65)}
	require.NoError(t, h.m.Pass(context.Background()))
	wait(t, h.states.filed, "the notifications")
	h.m.Stop()

	events := h.writer.all()
	require.Len(t, events, 2)
	first := numbers(t, events[0].Payload)
	assert.Equal(t, map[string]any{"order": json.Number("7")}, first["payload"])
	assert.Equal(t, json.Number("42"), first["pid"])
	assert.Equal(t, "plain text", numbers(t, events[1].Payload)["payload"])
	assert.NotEqual(t, events[0].EventID, events[1].EventID)
	assert.True(t, strings.HasPrefix(events[0].EventID, "notify-r1-"))
	require.Len(t, h.rejects.got, 1)
	assert.Equal(t, "too_large", h.rejects.got[0].Outcome)
	assert.Equal(t, [][]string{{"orders", "order_events"}}, h.db.listened)
}

// A source naming its own event id path keeps the id the path reads.
func TestNotify_EventIDPath(t *testing.T) {
	src := notifySrc()
	src.Config.EventIDPath = "payload.order"
	h := newHarness(t, src)
	h.db.listener.ch <- postgres.Notification{Channel: "order_events", Payload: `{"order":"o-7"}`}
	require.NoError(t, h.m.Pass(context.Background()))
	wait(t, h.states.filed, "the notification")
	h.m.Stop()
	assert.Equal(t, "o-7", h.writer.all()[0].EventID)
}

// A listener that cannot listen, or loses its connection, records the
// failure and frees its lock for the next pass.
func TestListener_Failures(t *testing.T) {
	h := newHarness(t, notifySrc())
	h.db.listenErr = errors.New(`appdb: listening on channel "order_events" of connection "orders": permission denied`)
	require.NoError(t, h.m.Pass(context.Background()))
	wait(t, h.states.failedCh, "the failed listen")
	h.m.Stop()
	assert.Contains(t, h.states.failed[0], "permission denied")
	assert.False(t, h.locks.isHeld("order-events"))

	h = newHarness(t, outboxSrc())
	h.db.listener.err = errors.New("connection reset")
	require.NoError(t, h.m.Pass(context.Background()))
	wait(t, h.states.failedCh, "the lost connection")
	h.m.Stop()
	assert.Contains(t, h.states.failed[0], "the listening connection was lost")

	h = newHarness(t, outboxSrc())
	h.writer.err = errors.New("bucket gone")
	h.db.rows = []postgres.OutboxRow{{ID: 1, Table: "public.orders", Op: "INSERT", Payload: `{}`}}
	require.NoError(t, h.m.Pass(context.Background()))
	wait(t, h.states.failedCh, "the failed write")
	h.m.Stop()
	assert.Contains(t, h.states.failed[0], "bucket gone")
	assert.Len(t, h.db.rows, 1, "a row whose write failed stays in the outbox")
	assert.Empty(t, h.rejects.got)

	h = newHarness(t, outboxSrc())
	h.m.cfg.Database = func() Database { return nil }
	require.NoError(t, h.m.Pass(context.Background()))
	wait(t, h.states.failedCh, "no database")
	h.m.Stop()
	assert.Contains(t, h.states.failed[0], "no application database")
}

// One replica listens per source: a source whose lock another replica holds
// is left to it, and a changed source is listened to again.
func TestPass_LockAndChange(t *testing.T) {
	h := newHarness(t, outboxSrc())
	release, ok, _ := h.locks.take(context.Background(), "orders-outbox")
	require.True(t, ok)
	require.NoError(t, h.m.Pass(context.Background()))
	assert.False(t, h.m.isRunning("orders-outbox"), "another replica holds the lock")
	release()

	require.NoError(t, h.m.Pass(context.Background()))
	wait(t, h.db.drained, "the first drain")
	assert.True(t, h.m.isRunning("orders-outbox"))
	changed := outboxSrc()
	changed.Config.Postgres.PollSeconds = 5
	h.sources.set(changed)
	require.NoError(t, h.m.Pass(context.Background()))
	wait(t, h.db.drained, "the drain of the changed source")
	h.m.Stop()
	assert.Len(t, h.db.listened, 2, "a changed source is listened to again")

	disabled := outboxSrc()
	disabled.Enabled = false
	h = newHarness(t, disabled, whsource.Source{Name: "plain", Enabled: true})
	require.NoError(t, h.m.Pass(context.Background()))
	h.m.Stop()
	assert.Empty(t, h.db.listened, "a disabled source and a source of another input are not listened to")
}

func TestLockKey(t *testing.T) {
	assert.Equal(t, LockKey("a"), LockKey("a"))
	assert.NotEqual(t, LockKey("a"), LockKey("b"))
	assert.GreaterOrEqual(t, LockKey("orders-outbox"), int32(0))
}
