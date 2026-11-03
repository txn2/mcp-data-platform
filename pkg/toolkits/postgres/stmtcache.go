package postgres

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// stmtCacheSize is how many statements one physical connection keeps
// prepared. The least recently used is deallocated past it, so a script that
// builds its SQL text at run time cannot grow a connection without bound.
const stmtCacheSize = 256

// staleResultType is the SQLSTATE PostgreSQL answers a prepared statement
// whose result columns changed under it ("cached plan must not change result
// type"): a table it reads was altered since it was prepared.
const staleResultType = "0A000"

// stmtCache is a connection's statement cache (#2402): each statement is
// prepared, under a name pgx derives from its text, on the physical
// connection that first runs it, and every later call on that connection runs
// the prepared statement without describing it again.
//
// A physical connection's prepared statements are the platform's own: a
// script's PREPARE, DEALLOCATE and DISCARD are refused (run.go), and the reset
// a released connection runs keeps them (resetSession). A migration the
// platform applies advances the generation, and each physical connection
// deallocates everything at its next use, so a statement prepared against the
// old table is not run against the new one.
type stmtCache struct {
	metrics    *atomic.Pointer[observability.Metrics]
	connection string

	gen   atomic.Uint64
	mu    sync.Mutex
	conns map[*pgx.Conn]*preparedSet
}

// preparedSet is what one physical connection holds prepared.
type preparedSet struct {
	// order is the prepared statements' text, least recently used first.
	order []string
	// gen is the generation the set was prepared in.
	gen uint64
	// stale are statements a run found out of date, deallocated at the
	// connection's next use (a failed transaction cannot deallocate).
	stale []string
}

func newStmtCache(connection string, metrics *atomic.Pointer[observability.Metrics]) *stmtCache {
	return &stmtCache{connection: connection, metrics: metrics, conns: make(map[*pgx.Conn]*preparedSet)}
}

// invalidate makes every physical connection deallocate what it holds at its
// next use: the schema changed.
func (s *stmtCache) invalidate() { s.gen.Add(1) }

// forget drops a physical connection the pool closed.
func (s *stmtCache) forget(pc *pgx.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, pc)
}

// describe prepares sql on tx's physical connection, or finds it prepared
// there already.
func (s *stmtCache) describe(ctx context.Context, tx pgx.Tx, sql string) (*pgconn.StatementDescription, error) {
	pc := tx.Conn()
	set, flush, stale := s.setOf(pc)
	if flush {
		if err := pc.DeallocateAll(ctx); err != nil {
			return nil, err //nolint:wrapcheck // read by its type
		}
	}
	for _, name := range stale {
		if err := pc.Deallocate(ctx, name); err != nil {
			return nil, err //nolint:wrapcheck // read by its type
		}
	}
	hit := slices.Contains(set.order, sql)
	sd, err := tx.Prepare(ctx, sql, sql)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by its type
	}
	if s.metrics != nil {
		s.metrics.Load().RecordAppDBStatementCache(ctx, s.connection, hit)
	}
	evict := s.use(set, sql)
	if evict != "" {
		if err := pc.Deallocate(ctx, evict); err != nil {
			return nil, err //nolint:wrapcheck // read by its type
		}
	}
	return sd, nil
}

// setOf is pc's prepared set, whether everything in it must be deallocated
// first (the generation moved), and the statements found stale since its
// last use.
func (s *stmtCache) setOf(pc *pgx.Conn) (set *preparedSet, flush bool, stale []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gen := s.gen.Load()
	set, ok := s.conns[pc]
	if !ok {
		set = &preparedSet{gen: gen}
		s.conns[pc] = set
	}
	if set.gen != gen {
		set.order, set.stale, set.gen = nil, nil, gen
		return set, true, nil
	}
	stale, set.stale = set.stale, nil
	for _, name := range stale {
		set.order = slices.DeleteFunc(set.order, func(o string) bool { return o == name })
	}
	return set, false, stale
}

// use moves sql to the most recently used end of set and returns the
// statement past the cache's size, or "".
func (s *stmtCache) use(set *preparedSet, sql string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	set.order = slices.DeleteFunc(set.order, func(o string) bool { return o == sql })
	set.order = append(set.order, sql)
	if len(set.order) <= stmtCacheSize {
		return ""
	}
	evict := set.order[0]
	set.order = set.order[1:]
	return evict
}

// failed notes a statement the server refused because the plan it was
// prepared with no longer fits the table, so the connection deallocates it at
// its next use and the next call prepares it again.
func (s *stmtCache) failed(tx pgx.Tx, sql string, err error) {
	if sqlState(err) != staleResultType {
		return
	}
	pc := tx.Conn()
	s.mu.Lock()
	defer s.mu.Unlock()
	if set, ok := s.conns[pc]; ok {
		set.stale = append(set.stale, sql)
	}
}

// InvalidateStatements makes every physical connection of connection
// deallocate its prepared statements at its next use: its schema changed
// (#2404 applies a migration through it).
func (t *Toolkit) InvalidateStatements(connection string) {
	if c, ok := t.lookup(connection); ok && c.cache != nil {
		c.cache.invalidate()
	}
}
