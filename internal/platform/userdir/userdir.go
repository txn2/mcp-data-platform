// Package userdir assembles the known-users directory (#614) behind one Handle:
// the Postgres-backed user store and the *user.Directory that wraps it with
// throttled, asynchronous upserts of authenticated people.
//
// Construction takes a single explicit input — a *sql.DB — so the subsystem is
// constructible and testable without a Platform. It imports pkg/user and
// pkg/middleware (for the authenticated-user shape), never pkg/platform. The
// *sql.DB is a shared foundation owned by the caller and passed in.
//
// New returns nil when db is nil: the directory needs a database, so a no-DB
// deployment gets the nil Handle and every accessor and observer degrades to a
// no-op (consumers fall back to free-typed email sharing). The two Observe
// methods are the seams the caller wires as the authenticator's UserObserver and
// the browser-session login callback; the directory itself sanitizes, throttles,
// and writes asynchronously, so both are cheap. The layer owns no background
// goroutine of its own, so it needs no Stop/Close.
package userdir

import (
	"database/sql"
	"log/slog"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

// Auth-type labels that represent a real person (as opposed to an API key or
// anonymous session). Only these populate the known-users directory.
const (
	authTypeLabelOIDC  = "oidc"
	authTypeLabelOAuth = "oauth"
)

// Handle owns the assembled known-users directory: the user store and the
// *user.Directory. Store() backs the callers that surface the directory (the
// find-tools / portal directory endpoints); Directory() is read to decide
// whether to wrap the authenticator. Both are nil on a nil Handle (no database),
// and the Observe methods are no-ops there, so every consumer degrades cleanly.
type Handle struct {
	store     user.Store
	directory *user.Directory
}

// New builds the user store and directory from db. It returns nil when db is nil
// (the directory is a no-op without a database), so the caller holds a nil Handle
// that every accessor and observer treats as disabled.
func New(db *sql.DB) *Handle {
	if db == nil {
		return nil
	}
	store := user.NewPostgresStore(db)
	slog.Info("user directory enabled")
	return &Handle{store: store, directory: user.NewDirectory(store)}
}

// Store returns the known-users directory store, or nil on a nil Handle (no
// database configured).
func (h *Handle) Store() user.Store {
	if h == nil {
		return nil
	}
	return h.store
}

// Directory returns the throttled async directory, or nil on a nil Handle. The
// caller reads it to decide whether to wrap the authenticator with the observer.
func (h *Handle) Directory() *user.Directory {
	if h == nil {
		return nil
	}
	return h.directory
}

// ObserveAuthenticated records an authenticated person in the directory. It is
// wired as the UserObserver on the authenticator, so it runs on every successful
// authentication. Only real people (OIDC/OAuth) are recorded; API keys and
// anonymous sessions are not persons to share with. No-op on a nil Handle, a nil
// directory, or a nil info.
func (h *Handle) ObserveAuthenticated(info *middleware.UserInfo) {
	if h == nil || h.directory == nil || info == nil {
		return
	}
	if info.AuthType != authTypeLabelOIDC && info.AuthType != authTypeLabelOAuth {
		return
	}
	first, last := user.NameFromClaims(info.Claims, info.Name)
	h.directory.Observe(info.Email, first, last)
}

// ObserveBrowserLogin records a portal/admin SPA user in the directory at login.
// The browser-session flow already supplies a split first/last name from the
// id_token, so this routes straight to the directory (which sanitizes, throttles,
// and writes asynchronously). No-op on a nil Handle or a nil directory.
func (h *Handle) ObserveBrowserLogin(email, firstName, lastName string) {
	if h == nil || h.directory == nil {
		return
	}
	h.directory.Observe(email, firstName, lastName)
}
