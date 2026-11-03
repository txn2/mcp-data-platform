package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/connprobe"
	"github.com/txn2/mcp-data-platform/internal/connstate"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Errors the connection registry answers with.
var (
	// ErrConnectionExists is returned when AddConnection names a connection
	// already registered.
	ErrConnectionExists = errors.New("postgres: connection already exists")
	// ErrConnectionNotFound is returned for a connection that is not
	// registered.
	ErrConnectionNotFound = errors.New("postgres: connection not found")
)

// validateTimeout bounds the check a connection save runs against its
// database.
const validateTimeout = 10 * time.Second

// Toolkit is the postgres toolkit. One Toolkit serves every registered
// PostgreSQL connection, each with a pool of its own on each replica.
type Toolkit struct {
	name        string
	defaultName string

	mu          sync.RWMutex
	connections map[string]*conn

	// open are the script transactions in progress, rolled back by Close.
	openMu sync.Mutex
	open   map[*Tx]struct{}

	// migrations keeps manage_schema's migrations; nil registers no tool.
	migrations MigrationStore
	// references finds the saved scripts a rename would break; nil checks
	// none.
	references ScriptReferences
	// scratchHook builds the toolkit a throwaway database is served by; nil
	// builds a pool. A unit test's stand-in.
	scratchHook func(name string, cfg Config) (*Toolkit, error)

	// The sweep of throwaway databases (sweep.go), stopped by Close.
	sweepOnce sync.Once
	sweepMu   sync.Mutex
	sweepStop chan struct{}

	guard   atomic.Pointer[ConnectGuard]
	metrics atomic.Pointer[observability.Metrics]
	dbCfg   observability.DBConfig
}

// conn is one connection in service: its configuration, its pool, and the
// types it has resolved.
type conn struct {
	cfg  Config
	pool *pgxpool.Pool
	// roles are the pools of the login roles personas map to (#2403), nil
	// when the connection maps none.
	roles *rolePools
	types *typeCache
	// cache is the statement cache, nil when statement_cache is off.
	cache *stmtCache
	// metrics is the toolkit's recorder.
	metrics *atomic.Pointer[observability.Metrics]
	// tracer reports the statements sent below pgx's query path (prepared.go).
	tracer pgx.QueryTracer
	// ledgerQ reads the migration ledger; nil reads it from the pool.
	ledgerQ ledgerReader
	// openSession holds a connection for a migration; nil takes one from
	// the pool.
	openSession func(context.Context) (session, error)
	// begin opens a transaction: the pool's, or a test's stand-in.
	begin func(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// NewMulti builds the toolkit from every configured instance. Building a pool
// opens no connection, so an unreachable database does not hold up startup;
// a connection that cannot be built (its TLS material does not load) is logged
// and skipped.
func NewMulti(cfg MultiConfig) *Toolkit {
	t := &Toolkit{
		name:        cfg.DefaultName,
		defaultName: cfg.DefaultName,
		connections: make(map[string]*conn, len(cfg.Instances)),
		dbCfg:       observability.DBConfigFromEnv(),
	}
	names := make([]string, 0, len(cfg.Instances))
	for name := range cfg.Instances {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c, err := t.newConn(name, cfg.Instances[name])
		if err != nil {
			slog.Warn("postgres: skipping connection", "connection", logsan.SanitizeForLog(name), "error", err)
			continue
		}
		t.connections[name] = c
	}
	return t
}

// newConn builds a connection's pool. Its connect hook reads the guard in
// force when a physical connection opens, not when the pool was built, so the
// platform installs the guard after the toolkit exists.
func (t *Toolkit) newConn(name string, cfg Config) (*conn, error) {
	cfg.ConnectionName = name
	tracer := &statementTracer{connection: name, database: cfg.Database, cfg: t.dbCfg, metrics: &t.metrics}
	pc, err := poolConfig(cfg, t.afterConnect, tracer)
	if err != nil {
		return nil, err
	}
	c := &conn{cfg: cfg, types: newTypeCache(), metrics: &t.metrics, tracer: tracer}
	if cfg.StatementCache {
		c.cache = newStmtCache(name, &t.metrics)
		pc.BeforeClose = c.cache.forget
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), pc)
	if err != nil {
		return nil, fmt.Errorf("postgres: building the pool: %w", err)
	}
	c.pool = pool
	c.begin = c.poolBegin
	if len(cfg.PersonaRoles) > 0 {
		c.roles = &rolePools{pools: make(map[string]*pgxpool.Pool), build: func(rc Config) (*pgxpool.Pool, error) {
			rpc, err := poolConfig(rc, t.afterConnect, tracer)
			if err != nil {
				return nil, err
			}
			if c.cache != nil {
				rpc.BeforeClose = c.cache.forget
			}
			p, err := pgxpool.NewWithConfig(context.Background(), rpc)
			if err != nil {
				return nil, fmt.Errorf("postgres: building the pool: %w", err)
			}
			return p, nil
		}}
	}
	return c, nil
}

// afterConnect runs the guard in force on a new physical connection, then
// refuses a server older than MinServerMajor. The guard goes first: the
// platform's own database is refused as that whatever its version.
func (t *Toolkit) afterConnect(ctx context.Context, c *pgx.Conn) error {
	if g := t.guard.Load(); g != nil && *g != nil {
		if err := (*g)(ctx, c); err != nil {
			return err
		}
	}
	return checkFloor(c.PgConn())
}

// SetConnectGuard installs the check every new physical connection passes:
// the platform's refusal of its own database (OwnDatabaseGuard).
func (t *Toolkit) SetConnectGuard(g ConnectGuard) {
	t.guard.Store(&g)
}

// SetMetrics installs the recorder statements are counted through, for the
// pools built before it as well as after.
func (t *Toolkit) SetMetrics(m *observability.Metrics) {
	t.metrics.Store(m)
	m.RegisterAppDBPools(t.poolSamples)
}

// poolSamples is every pool's state on this replica, for the appdb_pool
// series.
func (t *Toolkit) poolSamples() []observability.AppDBPoolSample {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]observability.AppDBPoolSample, 0, len(t.connections))
	for name, c := range t.connections {
		pools := c.allPools()
		if len(pools) == 0 {
			continue
		}
		// A connection's role pools (#2403) are reported with its own pool,
		// under its name.
		s := observability.AppDBPoolSample{Connection: name}
		for _, p := range pools {
			st := p.Stat()
			s.Acquired += int64(st.AcquiredConns())
			s.Idle += int64(st.IdleConns())
			s.Max += int64(st.MaxConns())
			s.Acquires += st.AcquireCount()
			s.Waited += st.EmptyAcquireCount()
			s.WaitSecondsInTotal += st.EmptyAcquireWaitTime().Seconds()
		}
		out = append(out, s)
	}
	return out
}

// Kind implements registry.Toolkit.
func (*Toolkit) Kind() string { return Kind }

// Name implements registry.Toolkit.
func (t *Toolkit) Name() string { return t.name }

// Connection implements registry.Toolkit: the default connection's name.
func (t *Toolkit) Connection() string { return t.defaultName }

// Tools implements registry.Toolkit. An application database has no agent
// tool for its data: scripts and endpoints reach it through the host's
// platform.query, platform.execute, platform.transaction and platform.batch,
// which call the toolkit directly (#2428, #2402). The one tool is
// manage_schema (#2404), which builds the schema, when a migration store is
// installed.
func (t *Toolkit) Tools() []string {
	if t.migrations == nil {
		return []string{}
	}
	return []string{SchemaToolName}
}

// SetSemanticProvider implements registry.Toolkit. Postgres tables are
// enriched once the catalog scans them (#2401).
func (*Toolkit) SetSemanticProvider(semantic.Provider) {}

// SetQueryProvider implements registry.Toolkit.
func (*Toolkit) SetQueryProvider(query.Provider) {}

// Close rolls back every transaction in progress and closes every pool.
func (t *Toolkit) Close() error {
	t.stopSweep()
	t.abandonOpen()
	t.mu.Lock()
	defer t.mu.Unlock()
	for name, c := range t.connections {
		c.close()
		delete(t.connections, name)
	}
	return nil
}

// RegisterTools implements registry.Toolkit: manage_schema, when a migration
// store is installed (see Tools).
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	if t.migrations != nil {
		t.registerSchemaTool(s)
	}
}

// AddConnection implements toolkit.ConnectionManager.
func (t *Toolkit) AddConnection(name string, config map[string]any) error {
	cfg, err := parseFilled(name, config)
	if err != nil {
		return err
	}
	c, err := t.newConn(name, cfg)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.connections[name]; ok {
		c.close()
		return fmt.Errorf("adding %s: %w", name, ErrConnectionExists)
	}
	t.connections[name] = c
	return nil
}

// UpdateConnection implements toolkit.ConnectionUpdater: the connection's new
// pool replaces the old one, which closes once its statements in flight end.
func (t *Toolkit) UpdateConnection(name string, config map[string]any) error {
	cfg, err := parseFilled(name, config)
	if err != nil {
		return err
	}
	c, err := t.newConn(name, cfg)
	if err != nil {
		return err
	}
	t.mu.Lock()
	previous := t.connections[name]
	t.connections[name] = c
	t.mu.Unlock()
	if previous != nil {
		go previous.close()
	}
	return nil
}

// RemoveConnection implements toolkit.ConnectionManager.
func (t *Toolkit) RemoveConnection(name string) error {
	t.mu.Lock()
	c, ok := t.connections[name]
	delete(t.connections, name)
	t.mu.Unlock()
	if !ok {
		return fmt.Errorf("removing %s: %w", name, ErrConnectionNotFound)
	}
	go c.close()
	return nil
}

// HasConnection implements toolkit.ConnectionManager.
func (t *Toolkit) HasConnection(name string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.connections[name]
	return ok
}

// ValidateConnection implements toolkit.ConnectionValidator: a save is refused
// when its configuration does not parse, and when its database answers and is
// the platform's own or on a server older than MinServerMajor. A database
// that does not answer is not refused here: both checks run again on every
// connection the pool opens.
func (t *Toolkit) ValidateConnection(name string, config map[string]any) error {
	cfg, err := parseFilled(name, config)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), validateTimeout)
	defer cancel()
	cfg.ConnectionName = name
	pc, err := poolConfig(cfg, nil, nil)
	if err != nil {
		return err
	}
	c, err := pgx.ConnectConfig(ctx, pc.ConnConfig)
	if err != nil {
		return nil //nolint:nilerr // an unreachable database is refused by the connect checks when it first answers
	}
	defer func() { _ = c.Close(context.Background()) }()
	return t.afterConnect(ctx, c)
}

// ListConnections implements toolkit.ConnectionLister.
func (t *Toolkit) ListConnections() []toolkit.ConnectionDetail {
	t.mu.RLock()
	defer t.mu.RUnlock()
	names := make([]string, 0, len(t.connections))
	for name := range t.connections {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]toolkit.ConnectionDetail, 0, len(names))
	for _, name := range names {
		readOnly := t.connections[name].cfg.ReadOnly
		out = append(out, toolkit.ConnectionDetail{
			Name:        name,
			Description: t.connections[name].cfg.Description,
			IsDefault:   name == t.defaultName,
			ReadOnly:    &readOnly,
		})
	}
	return out
}

// lookup returns a connection in service.
func (t *Toolkit) lookup(name string) (*conn, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	c, ok := t.connections[name]
	return c, ok
}

// ProbeConnection implements connprobe.Prober: it opens a connection, asks the
// server its version and the database and user it reached, and reports them.
func (t *Toolkit) ProbeConnection(ctx context.Context, name string) connprobe.Result {
	c, ok := t.lookup(name)
	if !ok {
		return connprobe.Failure(fmt.Sprintf("connection %q is not served by this toolkit", name), nil)
	}
	var version, database, user string
	err := c.pool.QueryRow(ctx, "SELECT current_setting('server_version'), current_database(), current_user").
		Scan(&version, &database, &user)
	observe(name, err)
	if old, ok := tooOld(err); ok {
		return connprobe.Failure(fmt.Sprintf("connection %q answered from PostgreSQL %s; the oldest an application database accepts is %d",
			name, old.Version, MinServerMajor), old)
	}
	if err != nil {
		return connprobe.Failure(fmt.Sprintf("connection %q did not answer", name), errors.New(scrubDriverError(err)))
	}
	detail := fmt.Sprintf("PostgreSQL %s answered for database %s as %s", version, database, user)
	if c.cfg.ReadOnly {
		detail = fmt.Sprintf("%s; this connection is read_only and refuses platform.execute", detail)
	}
	roles, err := c.probeRoles(ctx)
	if err != nil {
		return connprobe.Failure(fmt.Sprintf("connection %q answered as %s, and a persona role did not", name, user), err)
	}
	return connprobe.Success(detail + roles + t.probeListen(ctx, c))
}

// observe records what a call found of its connection.
func observe(name string, err error) {
	switch {
	case err == nil, statementError(err) != nil && !isConnectError(err):
		connstate.Observe(Kind, name, connstate.Healthy)
	case isAuthFailure(err):
		connstate.Observe(Kind, name, connstate.AuthFailed)
	case isConnectError(err):
		connstate.Observe(Kind, name, connstate.Unreachable)
	}
}

func isConnectError(err error) bool {
	var ce *pgconn.ConnectError
	return errors.As(err, &ce)
}

// isAuthFailure reports a connection refused for its credentials: SQLSTATE
// 28000 (invalid authorization) or 28P01 (invalid password).
func isAuthFailure(err error) bool {
	code := sqlState(err)
	return code == "28000" || code == "28P01"
}

// Interface checks.
var (
	_ toolkit.ConnectionManager   = (*Toolkit)(nil)
	_ toolkit.ConnectionUpdater   = (*Toolkit)(nil)
	_ toolkit.ConnectionValidator = (*Toolkit)(nil)
	_ toolkit.ConnectionLister    = (*Toolkit)(nil)
	_ connprobe.Prober            = (*Toolkit)(nil)
)

// rec is the toolkit's recorder, nil when none is installed. Every
// observability.Metrics method is nil-safe.
func (c *conn) rec() *observability.Metrics {
	if c.metrics == nil {
		return nil
	}
	return c.metrics.Load()
}

// reader is where the migration ledger is read from.
func (c *conn) reader() ledgerReader {
	if c.ledgerQ != nil {
		return c.ledgerQ
	}
	return c.pool
}
