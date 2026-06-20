// Package pkcestore holds in-flight PKCE state across the oauth-start →
// /oauth/callback round trip for the platform's outbound OAuth flows.
//
// It is deliberately separate from pkg/admin (the HTTP layer that drives
// the flow): the store is auth-storage infrastructure with no dependency
// on the admin handler, so it lives below it in the import graph. Two
// implementations ship — an in-memory map (single-replica, default) and a
// Postgres-backed store (multi-replica safe). The admin Handler selects
// based on whether a database is configured.
package pkcestore

import (
	"context"
	"errors"
	"time"
)

// TTL is how long an in-progress oauth-start hold-state remains valid
// before the operator must restart the flow. Salesforce and most
// providers complete the redirect in seconds; 10 minutes is a generous
// window that survives slow MFA prompts. Both stores and the admin
// oauth handlers reference this single value so the GC cutoff, the
// Postgres expires_at, and the user-facing TTL stay in sync.
const TTL = 10 * time.Minute

// gcInterval is how often the in-memory store sweeps stranded oauth-start
// entries on its own (separate from opportunistic put/take sweeps), and
// how often the Postgres store sweeps expired rows. One minute keeps the
// leak ceiling tight without being chatty for a feature that's idle most
// of the time.
const gcInterval = 1 * time.Minute

// ErrStateNotFound is returned by Store.Take when no state row matches
// (or the row had already expired). Callers use errors.Is to distinguish
// "no such pending flow" from a transport/IO error.
var ErrStateNotFound = errors.New("pkcestore: state not found")

// State is the server-side hold for one pending OAuth flow. It maps the
// random state token to the data the callback handler needs.
//
// Fields are exported so the admin oauth handlers can construct and read
// a State across the package boundary; the store implementations carry
// pointers to it unchanged.
type State struct {
	// Kind is the connection kind ("mcp" or "api") so the unified
	// /oauth/callback handler can dispatch the per-kind config parser
	// and post-auth side effects. Empty in legacy rows pre-dating
	// migration 000039 — those rows are MCP gateway flows by
	// construction (only kind that used this table at the time).
	Kind string
	// Connection is the name of the connection the flow authenticates.
	Connection string
	// CodeVerifier is the PKCE verifier paired with the challenge sent
	// to the provider; encrypted at rest by the Postgres store.
	CodeVerifier string
	// StartedBy identifies the admin who initiated the flow (for audit).
	StartedBy string
	// CreatedAt is when oauth-start recorded the hold; drives GC expiry.
	CreatedAt time.Time
	// ReturnURL is where the callback redirects the operator afterward.
	ReturnURL string
	// RedirectURI is the OAuth redirect_uri registered for the exchange.
	RedirectURI string
}

// Store holds in-flight PKCE state across the oauth-start →
// /oauth/callback round trip. Implementations must be safe for
// concurrent use.
type Store interface {
	// Put stores a state record. Implementations may evict entries that
	// have outlived TTL on every Put.
	Put(ctx context.Context, state string, val *State) error

	// Take atomically reads-and-deletes a state record. Returns
	// ErrStateNotFound when no row matches (or the row had already
	// expired). Other errors indicate transport/IO failure.
	Take(ctx context.Context, state string) (*State, error)

	// Close releases any background goroutines or DB resources. Safe to
	// call multiple times.
	Close() error
}
