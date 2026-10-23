package caldav

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/caldav"
	"github.com/txn2/mcp-data-platform/internal/conncatchup"
	"github.com/txn2/mcp-data-platform/internal/connprobe"
	"github.com/txn2/mcp-data-platform/internal/connset"
	"github.com/txn2/mcp-data-platform/internal/upstreamauth"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Tool names this toolkit registers.
const (
	// ToolList lists the calendars on an account.
	ToolList = "calendar_list"
	// ToolEvents lists the events in a time range.
	ToolEvents = "calendar_events"
	// ToolEvent reads one event in full.
	ToolEvent = "calendar_event"
)

// ErrConnectionNotFound is the refusal of a connection name the toolkit does
// not serve, and what the connection store reports a missing one with.
var ErrConnectionNotFound = errors.New("caldav: connection not found")

// discoveryTTL is how long a connection keeps the calendars it found before
// asking the server again: long enough that a burst of calls asks once, short
// enough that a calendar the account adds shows up within minutes.
const discoveryTTL = 5 * time.Minute

// Toolkit serves every caldav connection.
type Toolkit struct {
	name  string
	conns *connset.Set[*conn]
}

// conn is one served connection: its configuration and the client it reads
// the account with, and the calendars it last found there.
type conn struct {
	name   string
	cfg    Config
	http   *http.Client
	client *caldav.Client

	mu        sync.Mutex
	home      string
	calendars []caldav.Calendar
	found     time.Time
}

// NewMulti creates the toolkit from every configured instance.
func NewMulti(cfg MultiConfig) *Toolkit {
	t := &Toolkit{name: cfg.DefaultName}
	t.conns = connset.New(Kind, ErrConnectionNotFound, buildConn, releaseConn)
	for name, c := range cfg.Instances {
		cc, err := newConn(name, c)
		if err != nil {
			slog.Warn("caldav: skipping connection", "connection", name, "error", err)
			continue
		}
		t.conns.Put(name, cc)
	}
	return t
}

func buildConn(name string, cfg map[string]any) (*conn, error) {
	c, err := ParseConfig(cfg)
	if err != nil {
		return nil, err
	}
	return newConn(name, c)
}

func newConn(name string, c Config) (*conn, error) {
	c.ConnectionName = name
	c.Upstream.ConnectionName = name
	auth, err := upstreamauth.NewAuthenticator(c.Upstream)
	if err != nil {
		return nil, err //nolint:wrapcheck // prefixed by the shared policy
	}
	hc := upstreamauth.NewHTTPClient(c.Upstream)
	client, err := caldav.New(c.URL, hc, auth.Apply)
	if err != nil {
		return nil, err //nolint:wrapcheck // the protocol package names itself
	}
	return &conn{name: name, cfg: c, http: hc, client: client}, nil
}

func releaseConn(c *conn) {
	if c.http != nil {
		c.http.CloseIdleConnections()
	}
}

// Kind returns the connection kind.
func (*Toolkit) Kind() string { return Kind }

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string { return t.name }

// Connection returns the default connection name.
func (t *Toolkit) Connection() string { return t.name }

// Tools lists the tools this toolkit registers.
func (*Toolkit) Tools() []string { return []string{ToolList, ToolEvents, ToolEvent} }

// SetSemanticProvider is a no-op; calendars carry no catalog context.
func (*Toolkit) SetSemanticProvider(semantic.Provider) {}

// SetQueryProvider is a no-op.
func (*Toolkit) SetQueryProvider(query.Provider) {}

// Close releases every connection's idle transports.
func (t *Toolkit) Close() error {
	t.conns.Close()
	return nil
}

// SetConnectionStore attaches the store a connection saved on another
// replica is read from.
func (t *Toolkit) SetConnectionStore(s conncatchup.Store) { t.conns.SetStore(s) }

// AddConnection serves a connection an operator saved.
func (t *Toolkit) AddConnection(name string, config map[string]any) error {
	return t.conns.Add(name, config) //nolint:wrapcheck // the registry names the kind
}

// UpdateConnection replaces a served connection.
func (t *Toolkit) UpdateConnection(name string, config map[string]any) error {
	return t.conns.Update(name, config) //nolint:wrapcheck // the registry names the kind
}

// AdoptConnection serves a connection another replica saved.
func (t *Toolkit) AdoptConnection(name string, config map[string]any) error {
	return t.conns.Adopt(name, config) //nolint:wrapcheck // the registry names the kind
}

// RemoveConnection stops serving a connection.
func (t *Toolkit) RemoveConnection(name string) error {
	return t.conns.Remove(name) //nolint:wrapcheck // the registry names the kind
}

// HasConnection reports whether name is served.
func (t *Toolkit) HasConnection(name string) bool { return t.conns.Has(name) }

// ValidateConnection checks a configuration without serving it.
func (*Toolkit) ValidateConnection(_ string, config map[string]any) error {
	_, err := ParseConfig(config)
	return err
}

// ListConnections describes every served connection. Every caldav connection
// is read-only, and says so.
func (t *Toolkit) ListConnections() []toolkit.ConnectionDetail {
	out := []toolkit.ConnectionDetail{}
	readOnly := true
	t.conns.Each(func(name string, c *conn) {
		ro := readOnly
		out = append(out, toolkit.ConnectionDetail{
			Name:               name,
			Description:        c.cfg.Description,
			IsDefault:          name == t.name,
			ReadOnly:           &ro,
			MaxConcurrentCalls: c.cfg.Upstream.MaxConcurrentCalls,
		})
	})
	return out
}

// ProbeConnection finds the account's calendars and reports how many there
// are: the address, TLS, the credential and discovery in one read.
func (t *Toolkit) ProbeConnection(ctx context.Context, name string) connprobe.Result {
	c, err := t.conns.Get(ctx, name)
	if err != nil {
		return connprobe.Failure(fmt.Sprintf("connection %q is not served by this toolkit", name), nil)
	}
	cals, err := c.discover(ctx, true)
	if err != nil {
		return connprobe.Failure(upstreamauth.UnreachedSubject(name, c.cfg.URL, err), err)
	}
	names := make([]string, 0, len(cals))
	for _, cal := range cals {
		names = append(names, cal.DisplayName)
	}
	return connprobe.Success(fmt.Sprintf("%s answered; the account has %d calendar(s): %s",
		c.cfg.URL, len(cals), strings.Join(names, ", ")))
}

// discover returns the account's calendars, from the last discovery when it
// is recent and fresh is not asked for.
func (c *conn) discover(ctx context.Context, fresh bool) ([]caldav.Calendar, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !fresh && c.calendars != nil && time.Since(c.found) < discoveryTTL {
		return c.calendars, nil
	}
	if c.home == "" || fresh {
		home, err := c.client.HomeSet(ctx)
		if err != nil {
			return nil, err //nolint:wrapcheck // the protocol package names itself
		}
		c.home = home
	}
	cals, err := c.client.Calendars(ctx, c.home)
	if err != nil {
		return nil, err //nolint:wrapcheck // the protocol package names itself
	}
	c.calendars, c.found = cals, time.Now()
	return cals, nil
}

// Verify interface compliance.
var (
	_ connprobe.Prober            = (*Toolkit)(nil)
	_ toolkit.ConnectionLister    = (*Toolkit)(nil)
	_ toolkit.ConnectionManager   = (*Toolkit)(nil)
	_ toolkit.ConnectionValidator = (*Toolkit)(nil)
	_ toolkit.ConnectionUpdater   = (*Toolkit)(nil)
	_ toolkit.ConnectionAdopter   = (*Toolkit)(nil)
)
