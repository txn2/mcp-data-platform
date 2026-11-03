// Package whpg is the postgres input of a webhook source (#2408): the
// platform reads an application database and files what it reads as events,
// written exactly as the HTTP receiver writes a request's. Compaction into
// Parquet, the source's table, retention, alerts and the scripts naming the
// source then treat it like any other source.
//
// A notify source files each notification's payload; PostgreSQL delivers a
// notification only to a session listening when it is sent, so this mode is
// at most once. An outbox source drains an outbox table: a row is read,
// filed, and deleted in one transaction, and a poll drains the rows whose
// notification no one heard, so a missed notification delays an event and
// never loses it. Rows are filed before they are deleted, so a commit that
// fails after the write files them again, under the same event id, which
// compaction keeps once.
//
// One replica listens per source, under an advisory lock; another takes over
// on its next pass once the lock is free. What a payload holds is written by
// whoever can write the table or call pg_notify: it is data, bounded in size
// and never read as configuration.
//
//nolint:revive // max-public-structs: the exported types are the manager, its Config and the ports it is assembled from, one cohesive input.
package whpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// Tick is how often the manager starts the listeners of sources this replica
// can take and stops the ones whose source changed.
const Tick = 5 * time.Second

// outboxBatch is how many outbox rows one transaction drains.
const outboxBatch = 100

// notifyBatch bounds the notifications one delivery files, and notifyGather
// how long it waits for more after the first.
const (
	notifyBatch  = 100
	notifyGather = 100 * time.Millisecond
)

// LockKeyspace is the advisory-lock space a postgres source's listener is
// held in; the key within it is a hash of the source's name.
const LockKeyspace int32 = 4713212

// LockKey is the key a source's listener is locked under.
func LockKey(source string) int32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(source))
	return int32(min(h.Sum32()&math.MaxInt32, math.MaxInt32)) // #nosec G115 -- masked and clamped to 31 bits
}

// Ports.
type (
	// Sources lists the stored sources.
	Sources interface {
		List(ctx context.Context) ([]whsource.Source, error)
	}
	// Database reads application databases: the postgres toolkit.
	Database interface {
		Listen(ctx context.Context, connection string, channels []string) (Listener, error)
		DrainOutbox(ctx context.Context, connection, table string, limit int, write func([]postgres.OutboxRow) error) (int, error)
	}
	// Listener is one listening connection.
	Listener interface {
		Wait(ctx context.Context) (postgres.Notification, error)
		Close()
	}
	// Writer files events as segments (whwrite.Writer).
	Writer interface {
		Write(ctx context.Context, src whsource.Source, events []whevent.Event) error
	}
	// Rejections records what a listener refused.
	Rejections interface {
		RecordRejections(ctx context.Context, rejections []whstore.Rejection) error
	}
	// StateStore keeps each source's listener state (States).
	StateStore interface {
		Listening(ctx context.Context, source, replica string, since time.Time) error
		Stopped(ctx context.Context, source, replica string) error
		Filed(ctx context.Context, source string, delivered, rejected int64, at time.Time) error
		Failed(ctx context.Context, source, msg string, at time.Time) error
	}
	// Lock takes the one-replica lock on a source's listener, reporting
	// whether it was free.
	Lock func(ctx context.Context, source string) (release func(), ok bool, err error)
)

// Config assembles a manager.
type Config struct {
	Sources Sources
	// Database returns the live application databases, nil on a deployment
	// that serves none.
	Database   func() Database
	Writer     Writer
	Rejections Rejections
	States     StateStore
	Lock       Lock
	Replica    string
	Logger     *slog.Logger
	Now        func() time.Time
	// Tick overrides Tick; zero is Tick.
	Tick time.Duration
}

// Manager runs the listeners of the postgres sources this replica holds.
type Manager struct {
	cfg  Config
	stop chan struct{}
	wg   sync.WaitGroup
	// base is the context listeners run under, set by Start.
	base context.Context

	mu      sync.Mutex
	running map[string]*listening
	seq     uint64
}

// listening is one source's listener in progress.
type listening struct {
	fingerprint string
	cancel      context.CancelFunc
	// done is closed once the listener ended and released its lock.
	done chan struct{}
}

// New returns a manager, nil when a dependency is missing.
func New(cfg Config) *Manager {
	if cfg.Sources == nil || cfg.Database == nil || cfg.Writer == nil || cfg.States == nil || cfg.Lock == nil {
		return nil
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Tick <= 0 {
		cfg.Tick = Tick
	}
	return &Manager{cfg: cfg, stop: make(chan struct{}), running: map[string]*listening{}}
}

// Start runs the manager until Stop.
func (m *Manager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	m.base = context.WithoutCancel(ctx)
	m.wg.Go(func() {
		bgloop.Run(ctx, bgloop.Loop{Name: bgloop.NameWebhookPGListen, Every: m.cfg.Tick, Immediate: true, Stop: m.stop, Body: m.Pass})
	})
}

// Stop ends the manager and every listener, and waits for them.
func (m *Manager) Stop() {
	if m == nil {
		return
	}
	close(m.stop)
	m.mu.Lock()
	for _, l := range m.running {
		l.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// Pass stops the listeners whose source was removed, disabled or changed,
// and starts one for each enabled postgres source whose lock is free.
func (m *Manager) Pass(ctx context.Context) error {
	list, err := m.cfg.Sources.List(ctx)
	if err != nil {
		return fmt.Errorf("listing webhook sources: %w", err)
	}
	want := map[string]whsource.Source{}
	for _, src := range list {
		src = src.WithDefaults()
		if src.Enabled && src.IsPostgres() {
			want[src.Name] = src
		}
	}
	// A changed source's listener is stopped and waited for, so the lock it
	// releases is free for the listener started from the change.
	for _, done := range m.stopUnwanted(want) {
		<-done
	}
	var errs []error
	for name, src := range want {
		if m.isRunning(name) {
			continue
		}
		if err := m.take(ctx, src); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("starting postgres source listeners: %w", errors.Join(errs...))
	}
	return nil
}

// stopUnwanted cancels the listeners of sources no longer wanted as they
// were started, returning what each closes once it ended.
func (m *Manager) stopUnwanted(want map[string]whsource.Source) []chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	var stopped []chan struct{}
	for name, l := range m.running {
		src, ok := want[name]
		if !ok || fingerprint(src) != l.fingerprint {
			l.cancel()
			delete(m.running, name)
			stopped = append(stopped, l.done)
		}
	}
	return stopped
}

func (m *Manager) isRunning(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.running[name]
	return ok
}

// take starts src's listener when its lock is free.
func (m *Manager) take(ctx context.Context, src whsource.Source) error {
	release, ok, err := m.cfg.Lock(ctx, src.Name)
	if err != nil {
		return fmt.Errorf("locking postgres source %s: %w", src.Name, err)
	}
	if !ok {
		return nil
	}
	base := m.base
	if base == nil {
		base = context.WithoutCancel(ctx)
	}
	lctx, cancel := context.WithCancel(base)
	l := &listening{fingerprint: fingerprint(src), cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	m.running[src.Name] = l
	m.mu.Unlock()
	m.wg.Go(func() {
		defer close(l.done)
		defer release()
		defer m.forget(src.Name, l)
		m.run(lctx, src)
	})
	return nil
}

// forget removes l from the running listeners, unless a newer one replaced it.
func (m *Manager) forget(name string, l *listening) {
	l.cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running[name] == l {
		delete(m.running, name)
	}
}

// run listens for src until ctx ends or the listener fails; a failure is
// recorded, and the next pass starts the listener again.
func (m *Manager) run(ctx context.Context, src whsource.Source) {
	m.state(ctx, src.Name, func(ctx context.Context) error {
		return m.cfg.States.Listening(ctx, src.Name, m.cfg.Replica, m.cfg.Now())
	})
	defer m.state(ctx, src.Name, func(ctx context.Context) error { return m.cfg.States.Stopped(ctx, src.Name, m.cfg.Replica) })
	err := m.listen(ctx, src)
	if err == nil || ctx.Err() != nil {
		return
	}
	m.cfg.Logger.WarnContext(ctx, "webhooks: postgres listener stopped", "source", logsan.SanitizeForLog(src.Name),
		"error", logsan.SanitizeForLog(err.Error()))
	m.state(ctx, src.Name, func(ctx context.Context) error { return m.cfg.States.Failed(ctx, src.Name, err.Error(), m.cfg.Now()) })
}

// state writes a listener's state on a context that outlives its cancel,
// logging a write that fails.
func (m *Manager) state(ctx context.Context, source string, write func(context.Context) error) {
	if err := write(context.WithoutCancel(ctx)); err != nil {
		m.cfg.Logger.WarnContext(ctx, "webhooks: recording a postgres listener's state", "source", logsan.SanitizeForLog(source),
			"error", logsan.SanitizeForLog(err.Error()))
	}
}

// errNoDatabase is a postgres source on a deployment serving no application
// database.
var errNoDatabase = errors.New("no application database is served on this deployment")

// listen reads src's database until ctx ends or the connection fails.
func (m *Manager) listen(ctx context.Context, src whsource.Source) error {
	db := m.cfg.Database()
	if db == nil {
		return errNoDatabase
	}
	p := src.Config.Postgres
	if p.Mode == whsource.PostgresOutbox {
		return m.outbox(ctx, db, src)
	}
	l, err := db.Listen(ctx, p.Connection, p.Channels)
	if err != nil {
		return err //nolint:wrapcheck // the toolkit names the connection and channel
	}
	defer l.Close()
	for ctx.Err() == nil {
		n, err := l.Wait(ctx)
		if err != nil {
			return lost(ctx, err)
		}
		batch := m.gather(ctx, l, n)
		if err := bgloop.Unit(ctx, bgloop.NameWebhookPGDeliver, func(ctx context.Context) error {
			return m.fileNotifications(ctx, src, batch)
		}); err != nil {
			return err //nolint:wrapcheck // the delivery's own failure, which names its step
		}
	}
	return nil
}

// gather collects the notifications that arrive shortly after first, up to
// notifyBatch, so a burst is filed as one segment.
func (*Manager) gather(ctx context.Context, l Listener, first postgres.Notification) []postgres.Notification {
	batch := []postgres.Notification{first}
	for len(batch) < notifyBatch {
		wctx, cancel := context.WithTimeout(ctx, notifyGather)
		n, err := l.Wait(wctx)
		cancel()
		if err != nil {
			break
		}
		batch = append(batch, n)
	}
	return batch
}

// outbox drains src's outbox at start, on each notification, and at least
// every poll interval.
func (m *Manager) outbox(ctx context.Context, db Database, src whsource.Source) error {
	p := src.Config.Postgres
	l, err := db.Listen(ctx, p.Connection, []string{p.Table})
	if err != nil {
		return err //nolint:wrapcheck // the toolkit names the connection and channel
	}
	defer l.Close()
	poll := time.Duration(p.PollSeconds) * time.Second
	for ctx.Err() == nil {
		if err := bgloop.Unit(ctx, bgloop.NameWebhookPGDeliver, func(ctx context.Context) error {
			return m.drain(ctx, db, src)
		}); err != nil {
			return err //nolint:wrapcheck // the delivery's own failure, which names its step
		}
		wctx, cancel := context.WithTimeout(ctx, poll)
		_, err := l.Wait(wctx)
		timedOut := wctx.Err() != nil && ctx.Err() == nil
		cancel()
		if err != nil && !timedOut {
			return lost(ctx, err)
		}
	}
	return nil
}

// drain files the outbox's rows until fewer than a batch remain.
func (m *Manager) drain(ctx context.Context, db Database, src whsource.Source) error {
	p := src.Config.Postgres
	for {
		n, err := db.DrainOutbox(ctx, p.Connection, p.Table, outboxBatch, func(rows []postgres.OutboxRow) error {
			return m.fileRows(ctx, src, rows)
		})
		if err != nil {
			return err //nolint:wrapcheck // the toolkit's or the writer's failure, naming the step
		}
		if n < outboxBatch {
			return nil
		}
	}
}

// lost is a listener's failed wait: nil when the manager stopped it.
func lost(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil //nolint:nilerr // a wait the manager canceled is a stop, not a failure
	}
	return fmt.Errorf("the listening connection was lost: %w", err)
}

// fingerprint is what a running listener was started from: a source changed
// in any of it is listened to again.
func fingerprint(src whsource.Source) string {
	b, _ := json.Marshal(src.Config)
	return string(b)
}
