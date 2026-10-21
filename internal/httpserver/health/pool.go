package health

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"
)

// Pool starvation (#2169). A replica whose database pool hands out no
// connection serves nothing that reads the database, yet the process state
// alone kept /readyz at 200 while one did. A pool every connection of which
// is in use, and stays so, takes the replica out of service. A database that
// is down is not this: its pool opens no connections, and /readyz leaves an
// upstream outage to the dependency probes so it never takes every replica out
// at once.
const (
	// DefaultPoolWait is how long one probe waits for a connection. It is
	// under the one second a readiness probe is given by default.
	DefaultPoolWait = 500 * time.Millisecond
	// DefaultPoolStarvedFor is how long the pool must go without handing one
	// out before the replica reports not ready, so a busy pool that frees a
	// connection now and then is not taken out of service.
	DefaultPoolStarvedFor = 30 * time.Second
	statusPoolStarved     = "database_pool_starved"
)

// PoolProbe reports whether a database pool can hand out a connection.
type PoolProbe struct {
	db         *sql.DB
	wait       time.Duration
	starvedFor time.Duration
	now        func() time.Time

	mu    sync.Mutex
	since time.Time // when the pool was first found starved; zero while it is not
}

// NewPoolProbe probes db, waiting wait for a connection and reporting it
// starved once it has handed none out for starvedFor.
func NewPoolProbe(db *sql.DB, wait, starvedFor time.Duration) *PoolProbe {
	return &PoolProbe{db: db, wait: wait, starvedFor: starvedFor, now: time.Now}
}

// Starved reports whether every connection of the pool has been in use, with
// none handed out on request, for at least starvedFor.
func (p *PoolProbe) Starved(ctx context.Context) bool {
	if p == nil || p.db == nil {
		return false
	}
	st := p.db.Stats()
	if st.MaxOpenConnections == 0 || st.InUse < st.MaxOpenConnections || p.answers(ctx) {
		p.mu.Lock()
		p.since = time.Time{}
		p.mu.Unlock()
		return false
	}
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.since.IsZero() {
		p.since = now
	}
	if now.Sub(p.since) < p.starvedFor {
		return false
	}
	slog.WarnContext(ctx, "database pool starved: every connection in use and none handed out",
		"in_use", st.InUse, "max_open_conns", st.MaxOpenConnections, "since", p.since)
	return true
}

// answers reports whether the pool hands out a connection within wait.
func (p *PoolProbe) answers(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, p.wait)
	defer cancel()
	conn, err := p.db.Conn(ctx)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
