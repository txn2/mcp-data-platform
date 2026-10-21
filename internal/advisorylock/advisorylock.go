// Package advisorylock holds the platform's PostgreSQL session advisory locks
// on one database session per process (#2169).
//
// An advisory lock belongs to the session that took it, so a lock held for a
// background pass used to pin a pooled connection of its own for the whole
// pass, while the pass drew its queries from the same pool. Each locked loop
// added one more pinned connection. With four loops locked on a pool of five,
// the passes waited on the pool for their own queries, never finished, never
// released their locks, and starved every request on the replica.
//
// PostgreSQL lets one session hold any number of advisory locks, so every
// try-lock the process takes goes onto one Session: however many loops hold
// a lock, together they pin one connection, and the session goes back to the
// pool when the last lock is released. The pass itself runs on the pool, never
// on the lock's session.
//
// A session lock is reentrant in PostgreSQL: the session holding a key is
// granted it again. A Session therefore refuses a key the process already
// holds, so two goroutines of one process exclude each other as two replicas
// do.
package advisorylock

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// statementTimeout bounds one lock or unlock statement. It runs on a context
// detached from the caller's, since a statement canceled mid-flight can end
// the session and with it every lock the process holds.
const statementTimeout = 5 * time.Second

// Key is one advisory lock: a single bigint, or the two-integer form, which
// PostgreSQL keeps in a key space of its own.
type Key struct {
	id    int64
	space int32
	n     int32
	pair  bool
}

// ID is the lock keyed by one bigint.
func ID(id int64) Key { return Key{id: id} }

// Pair is the lock keyed by two integers.
func Pair(space, n int32) Key { return Key{space: space, n: n, pair: true} }

// statement is the query taking (or, with unlock, releasing) k.
func (k Key) statement(unlock bool) (query string, args []any) {
	fn := "pg_try_advisory_lock"
	if unlock {
		fn = "pg_advisory_unlock"
	}
	if k.pair {
		return "SELECT " + fn + "($1, $2)", []any{k.space, k.n}
	}
	return "SELECT " + fn + "($1)", []any{k.id}
}

// Session is the one database session a process takes its advisory locks on.
// It is safe for concurrent use.
type Session struct {
	db *sql.DB

	mu sync.Mutex
	// conn is the session, nil while no lock is held.
	conn *sql.Conn
	// gen counts the sessions opened; a release from an earlier one is a lock
	// that ended with its session.
	gen  uint64
	held map[Key]struct{}
}

// New builds a Session over db. Production code uses For, which keeps one
// per database handle; New is for a test that stands two replicas on one.
func New(db *sql.DB) *Session {
	return &Session{db: db, held: map[Key]struct{}{}}
}

//nolint:gochecknoglobals // one lock session per database handle in the process, which is the point
var sessions sync.Map

// For is the process's lock session over db.
func For(db *sql.DB) *Session {
	if s, ok := sessions.Load(db); ok {
		return s.(*Session) //nolint:forcetypeassert,errcheck // only *Session is stored
	}
	s, _ := sessions.LoadOrStore(db, New(db))
	return s.(*Session) //nolint:forcetypeassert,errcheck // only *Session is stored
}

// Try takes key on db's lock session; see Session.Try.
func Try(ctx context.Context, db *sql.DB, key Key) (release func(), ok bool, err error) {
	return For(db).Try(ctx, key)
}

// Try takes key without waiting. ok is false when another session holds it
// or this process already does. release gives it back and is safe to call
// more than once; it does nothing when the session ended in the meantime,
// since that released the lock.
func (s *Session) Try(ctx context.Context, key Key) (release func(), ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err //nolint:wrapcheck // the caller's own context
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.open(ctx); err != nil {
		return nil, false, err
	}
	if _, mine := s.held[key]; mine {
		s.closeIfIdle()
		return nil, false, nil
	}
	got, err := s.take(ctx, key)
	if err != nil {
		// A statement that failed took nothing. A session whose connection
		// broke has lost every lock on it, and is ended so the next take
		// opens another rather than failing on it until its holders finish.
		if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
			s.lose(ctx, "the lock session's connection broke")
		}
		s.closeIfIdle()
		return nil, false, fmt.Errorf("taking the advisory lock: %w", err)
	}
	if !got {
		s.closeIfIdle()
		return nil, false, nil
	}
	s.held[key] = struct{}{}
	gen := s.gen
	var once sync.Once
	return func() { once.Do(func() { s.release(ctx, key, gen) }) }, true, nil
}

// Held reports whether any session, this process's included, holds key.
func (s *Session) Held(ctx context.Context, key Key) (bool, error) {
	s.mu.Lock()
	_, mine := s.held[key]
	s.mu.Unlock()
	if mine {
		return true, nil
	}
	release, ok, err := s.Try(ctx, key)
	if err != nil || !ok {
		return err == nil, err
	}
	release()
	return false, nil
}

// open takes the session from the pool when none is open. It is called with
// mu held and gives mu up while it waits on the pool, so a release is never
// held behind a wait for a connection.
func (s *Session) open(ctx context.Context) error {
	if s.conn != nil {
		return nil
	}
	s.mu.Unlock()
	conn, err := s.db.Conn(ctx)
	s.mu.Lock()
	if err != nil {
		return fmt.Errorf("acquiring the lock session: %w", err)
	}
	if s.conn != nil {
		_ = conn.Close()
		return nil
	}
	s.conn = conn
	s.gen++
	return nil
}

// take runs key's try-lock statement on the session.
func (s *Session) take(ctx context.Context, key Key) (bool, error) {
	stmtCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statementTimeout)
	defer cancel()
	q, args := key.statement(false)
	var got bool
	err := s.conn.QueryRowContext(stmtCtx, q, args...).Scan(&got)
	return got, err //nolint:wrapcheck // the caller names the statement
}

// unlock runs key's unlock statement on the session.
func (s *Session) unlock(ctx context.Context, key Key) error {
	stmtCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statementTimeout)
	defer cancel()
	q, args := key.statement(true)
	_, err := s.conn.ExecContext(stmtCtx, q, args...)
	return err //nolint:wrapcheck // the caller names the statement
}

// release gives key back, unless the session it was taken on has ended.
func (s *Session) release(ctx context.Context, key Key, gen uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.gen || s.conn == nil {
		return
	}
	if _, mine := s.held[key]; !mine {
		return
	}
	delete(s.held, key)
	if err := s.unlock(ctx, key); err != nil {
		// The session may still hold the lock; ending it is what releases it.
		slog.WarnContext(ctx, "advisory unlock failed; ending the lock session",
			"error", logsan.SanitizeForLog(err.Error()))
		s.lose(ctx, "an unlock failed")
		return
	}
	s.closeIfIdle()
}

// closeIfIdle returns the session to the pool when it holds no lock.
func (s *Session) closeIfIdle() {
	if len(s.held) == 0 && s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
}

// lose ends the session, which releases every lock on it, and drops it from
// the pool. A lock held when it ends is reported, since its holder still
// runs.
func (s *Session) lose(ctx context.Context, why string) {
	if n := len(s.held); n > 0 {
		slog.WarnContext(ctx, "advisory lock session ended with locks held", "reason", why, "locks", n)
	}
	if s.conn != nil {
		_ = s.conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = s.conn.Close()
		s.conn = nil
	}
	s.held = map[Key]struct{}{}
}
