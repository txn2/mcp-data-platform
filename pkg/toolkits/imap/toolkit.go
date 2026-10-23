package imap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/conncatchup"
	"github.com/txn2/mcp-data-platform/internal/connprobe"
	"github.com/txn2/mcp-data-platform/internal/connset"
	"github.com/txn2/mcp-data-platform/internal/connstate"
	"github.com/txn2/mcp-data-platform/internal/imapmail"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Tool names this toolkit registers.
const (
	// ToolSearch finds messages and returns their headers.
	ToolSearch = "mail_search"
	// ToolMessage reads one message.
	ToolMessage = "mail_message"
	// ToolThread reads a message's thread.
	ToolThread = "mail_thread"
	// ToolAttachment saves one attachment as a managed resource.
	ToolAttachment = "mail_attachment"
)

// ErrConnectionNotFound is the refusal of a connection name the toolkit does
// not serve, and what the connection store reports a missing one with.
var ErrConnectionNotFound = errors.New("imap: connection not found")

// Toolkit serves every imap connection.
type Toolkit struct {
	name  string
	conns *connset.Set[Config]

	mu     sync.RWMutex
	lander toolkit.ResourceLander
}

// NewMulti creates the toolkit from every configured instance.
func NewMulti(cfg MultiConfig) *Toolkit {
	t := &Toolkit{name: cfg.DefaultName}
	t.conns = connset.New(Kind, ErrConnectionNotFound, buildConfig, nil)
	for name, c := range cfg.Instances {
		c.ConnectionName = name
		t.conns.Put(name, c)
	}
	return t
}

func buildConfig(name string, raw map[string]any) (Config, error) {
	c, err := ParseConfig(raw)
	if err != nil {
		return Config{}, err
	}
	c.ConnectionName = name
	return c, nil
}

// SetResourceLander attaches where mail_attachment saves files. It must be
// set before the tools are registered: without it mail_attachment is not
// offered.
func (t *Toolkit) SetResourceLander(l toolkit.ResourceLander) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lander = l
}

func (t *Toolkit) resourceLander() toolkit.ResourceLander {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lander
}

// Kind returns the connection kind.
func (*Toolkit) Kind() string { return Kind }

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string { return t.name }

// Connection returns the default connection name.
func (t *Toolkit) Connection() string { return t.name }

// Tools lists the tools this toolkit registers; mail_attachment only when
// there is a resource library to save into.
func (t *Toolkit) Tools() []string {
	tools := []string{ToolSearch, ToolMessage, ToolThread}
	if t.resourceLander() != nil {
		tools = append(tools, ToolAttachment)
	}
	return tools
}

// SetSemanticProvider is a no-op; mail carries no catalog context.
func (*Toolkit) SetSemanticProvider(semantic.Provider) {}

// SetQueryProvider is a no-op.
func (*Toolkit) SetQueryProvider(query.Provider) {}

// Close releases nothing; a session lives for one call.
func (*Toolkit) Close() error { return nil }

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

// ListConnections describes every served connection; every imap connection
// is read-only.
func (t *Toolkit) ListConnections() []toolkit.ConnectionDetail {
	out := []toolkit.ConnectionDetail{}
	t.conns.Each(func(name string, c Config) {
		ro := true
		desc := c.Description
		if desc == "" {
			desc = "Mailbox " + c.Username + " (" + strings.Join(c.Folders, ", ") + ")"
		}
		out = append(out, toolkit.ConnectionDetail{Name: name, Description: desc, IsDefault: name == t.name, ReadOnly: &ro})
	})
	return out
}

// ProbeConnection logs in and opens each configured folder read-only,
// reporting how many messages each holds.
func (t *Toolkit) ProbeConnection(ctx context.Context, name string) connprobe.Result {
	c, err := t.conns.Get(ctx, name)
	if err != nil {
		return connprobe.Failure(fmt.Sprintf("connection %q is not served by this toolkit", name), nil)
	}
	s, err := open(ctx, c)
	if err != nil {
		return connprobe.Failure(fmt.Sprintf("%s did not accept the connection", c.Host), err)
	}
	defer s.Close() //nolint:errcheck // logout after the probe
	parts := make([]string, 0, len(c.Folders))
	for _, f := range c.Folders {
		_, n, err := s.Examine(f)
		if err != nil {
			return connprobe.Failure(fmt.Sprintf("logged in to %s, but the folder %q could not be opened", c.Host, f), err)
		}
		parts = append(parts, fmt.Sprintf("%s (%d messages)", f, n))
	}
	return connprobe.Success(fmt.Sprintf("logged in to %s as %s; %s", c.Host, c.Username, strings.Join(parts, ", ")))
}

// open starts a session for one call, recording what the server answered as
// the connection's state.
func open(ctx context.Context, c Config) (*imapmail.Session, error) {
	acct, err := c.account(ctx)
	if err != nil {
		return nil, err
	}
	s, err := imapmail.Open(ctx, acct)
	if err != nil {
		state := connstate.Unreachable
		if strings.Contains(err.Error(), "logging in") {
			state = connstate.AuthFailed
		}
		connstate.Observe(Kind, c.ConnectionName, state)
		slog.WarnContext(ctx, "imap: session failed", "connection", c.ConnectionName, "error", err)
		return nil, fmt.Errorf("imap: %w", err)
	}
	connstate.Observe(Kind, c.ConnectionName, connstate.Healthy)
	return s, nil
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
