package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Identity is who a statement runs for (#2403): the persona whose login role
// the statement runs as, and the caller claims every transaction sets. The
// script host puts it on the context of each call (WithIdentity); a context
// without one runs as the connection's own role with every claim empty.
type Identity struct {
	// Persona chooses the login role: the one persona_roles maps it to, or
	// the connection's user when it maps none. It is also mcp.persona.
	Persona string
	// UserID and Email are mcp.user_id and mcp.email: an authenticated
	// endpoint's caller, or the author of the script a run executes.
	UserID string
	Email  string
	// Source is mcp.source: "endpoint" for an endpoint request, "script" for
	// a run.
	Source string
}

// The values Identity.Source takes.
const (
	SourceEndpoint = "endpoint"
	SourceScript   = "script"
)

type identityKey struct{}

// WithIdentity returns ctx carrying who its statements run for.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityOf is the identity on ctx, the zero value when none.
func IdentityOf(ctx context.Context) Identity {
	id, _ := ctx.Value(identityKey{}).(Identity)
	return id
}

// claimsSQL sets the caller claims as transaction-local settings from bound
// values. Every claim is set in every transaction, as the empty string when
// absent, so a statement never reads a value an earlier caller's transaction
// on the same physical connection set: a setting made with is_local ends with
// its transaction, and the reset a released connection runs (RESET ALL)
// removes one a statement made for its session.
const claimsSQL = "SELECT set_config('mcp.user_id', $1, true), set_config('mcp.email', $2, true), " +
	"set_config('mcp.persona', $3, true), set_config('mcp.source', $4, true)"

// claims are the values claimsSQL binds, in order, as text.
func (id Identity) claims() [][]byte {
	return [][]byte{[]byte(id.UserID), []byte(id.Email), []byte(id.Persona), []byte(id.Source)}
}

// rolePools are a connection's pools for the login roles personas map to,
// each built the first time a statement runs as it. A role logs in directly
// rather than being switched to with SET ROLE: a session can RESET ROLE back
// to the role it logged in as, so only a login is a boundary against the
// person writing the SQL.
type rolePools struct {
	mu    sync.Mutex
	pools map[string]*pgxpool.Pool
	// build makes the pool of one persona's role from the connection's
	// configuration with the role's user and password.
	build func(cfg Config) (*pgxpool.Pool, error)
}

// poolFor is the pool a statement for persona runs on: its role's, or the
// connection's own when persona maps no role.
func (c *conn) poolFor(persona string) (*pgxpool.Pool, error) {
	role, ok := c.cfg.PersonaRoles[persona]
	if !ok || c.roles == nil {
		return c.pool, nil
	}
	c.roles.mu.Lock()
	defer c.roles.mu.Unlock()
	if p, ok := c.roles.pools[persona]; ok {
		return p, nil
	}
	rc := c.cfg
	rc.User, rc.Password = role, c.cfg.PersonaRolePasswords[persona]
	p, err := c.roles.build(rc)
	if err != nil {
		return nil, fmt.Errorf("appdb: building the pool of persona %q's role on connection %q: %w",
			persona, c.cfg.ConnectionName, err)
	}
	c.roles.pools[persona] = p
	return p, nil
}

// allPools is the connection's pool and every role pool built so far.
func (c *conn) allPools() []*pgxpool.Pool {
	out := []*pgxpool.Pool{}
	if c.pool != nil {
		out = append(out, c.pool)
	}
	if c.roles == nil {
		return out
	}
	c.roles.mu.Lock()
	defer c.roles.mu.Unlock()
	personas := make([]string, 0, len(c.roles.pools))
	for p := range c.roles.pools {
		personas = append(personas, p)
	}
	sort.Strings(personas)
	for _, p := range personas {
		out = append(out, c.roles.pools[p])
	}
	return out
}

// close closes every pool of the connection, waiting for the statements in
// flight on each.
func (c *conn) close() {
	for _, p := range c.allPools() {
		p.Close()
	}
}

// roleProbeSQL is what a persona role's probe asks: who it logged in as, and
// whether row-level security applies to it at all.
const roleProbeSQL = "SELECT current_user, rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user"

// probeRoles logs in as every persona role the connection maps and reports
// each, warning of one that row-level security does not apply to: a
// superuser, or a role with BYPASSRLS. The text names roles and personas,
// never a password.
func (c *conn) probeRoles(ctx context.Context) (string, error) {
	personas := make([]string, 0, len(c.cfg.PersonaRoles))
	for p := range c.cfg.PersonaRoles {
		personas = append(personas, p)
	}
	sort.Strings(personas)
	var b strings.Builder
	for _, persona := range personas {
		pool, err := c.poolFor(persona)
		if err != nil {
			return "", err
		}
		var user string
		var super, bypass bool
		err = pool.QueryRow(ctx, roleProbeSQL).Scan(&user, &super, &bypass)
		if err != nil {
			return "", fmt.Errorf("persona %q's role %s did not answer: %w",
				persona, c.cfg.PersonaRoles[persona], errors.New(scrubDriverError(err)))
		}
		fmt.Fprintf(&b, "; persona %s runs as %s", persona, user)
		if super || bypass {
			_, _ = b.WriteString(" (superuser or BYPASSRLS: row-level security does not apply to it)")
		}
	}
	return b.String(), nil
}

// ConnectionRoles are the login roles connection's statements run as: its
// own user and each persona's role, sorted. A backend of another role is not
// the platform's to cancel or terminate (#2410). Nil for an unknown connection.
func (t *Toolkit) ConnectionRoles(connection string) []string {
	c, ok := t.lookup(connection)
	if !ok {
		return nil
	}
	out := []string{c.cfg.User}
	for _, role := range c.cfg.PersonaRoles {
		if !slices.Contains(out, role) {
			out = append(out, role)
		}
	}
	sort.Strings(out)
	return out
}

// ApplyPersonas are the personas connection's schema_apply_personas names,
// which may apply its migrations and maintain it (#2410).
func (t *Toolkit) ApplyPersonas(connection string) []string {
	if c, ok := t.lookup(connection); ok {
		return slices.Clone(c.cfg.SchemaApplyPersonas)
	}
	return nil
}
