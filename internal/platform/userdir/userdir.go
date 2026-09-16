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
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/platform/subjects"
	subjectspg "github.com/txn2/mcp-data-platform/internal/platform/subjects/postgres"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
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
	// subjects records the subject each address authenticates as, learned at
	// the same chokepoint the directory observes on, and is what a
	// managed-script run reads to file resources in its author's own library
	// (#1677).
	subjects *subjects.Book
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
	return &Handle{
		store:     store,
		directory: user.NewDirectory(store),
		subjects:  subjects.New(subjectspg.New(db)),
	}
}

// Subjects is the recorded pairs a managed-script run resolves its author's
// subject through, or nil on a nil Handle (no database).
func (h *Handle) Subjects() *subjects.Book {
	if h == nil {
		return nil
	}
	return h.subjects
}

// BindResourceFold supplies what the fold of an address-keyed library into the
// subject-keyed one refiles through, once the managed-resource layer exists.
func (h *Handle) BindResourceFold(deps resource.Deps) {
	if h == nil {
		return
	}
	h.subjects.BindResources(deps)
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
	// The pair is recorded for an API key too: a key is held by a person and
	// a session presents it as its own subject, so a script that person
	// authors has to file where that session files. The directory below stays
	// people-only; a key is nobody to share with.
	h.subjects.Observe(info)
	if info.AuthType != authTypeLabelOIDC && info.AuthType != authTypeLabelOAuth {
		return
	}
	first, last := user.NameFromClaims(info.Claims, info.Name)
	h.directory.Observe(info.Email, first, last, info.Roles)
}

// ObserveBrowserLogin records a portal/admin SPA user in the directory at login.
// The browser-session flow already supplies a split first/last name and the
// subject and roles it extracted from the id_token, so this routes straight to
// the directory (which sanitizes, throttles, and writes asynchronously). No-op
// on a nil Handle or a nil directory.
//
// The subject pair is recorded here as well as on the token path. A person who
// only ever signs in through the portal never passes through the authenticator,
// so this is the only place the platform learns what they authenticate as --
// which a managed-script run acting for them reads (#1677), and which a key
// issued against their account authenticates as (#1759).
func (h *Handle) ObserveBrowserLogin(email, firstName, lastName, subject string, roles []string) {
	if h == nil || h.directory == nil {
		return
	}
	h.directory.Observe(email, firstName, lastName, roles)
	h.subjects.Observe(&middleware.UserInfo{
		UserID:   subject,
		Email:    email,
		Roles:    roles,
		AuthType: authTypeLabelOIDC,
	})
}

// BoundPrincipal resolves the person an API key is issued against: the subject
// their own sessions authenticate as, their address as the directory holds it,
// and the roles the identity provider last said they hold (#1759).
//
// It answers auth.ErrNoBoundPrincipal for somebody the platform cannot speak
// for: an address the directory has no row for (including a person an
// administrator has since removed, which is how removing them revokes their
// keys) and one it has never seen sign in, which has no subject to present and
// no roles to carry. A read that FAILED reports that failure instead, so a key
// is refused rather than resolved against a directory that could not answer,
// and a caller can tell "there is nobody" from "I could not look".
//
// The two reads are independent and are made together, so a bound key costs one
// round trip's worth of latency rather than two.
func (h *Handle) BoundPrincipal(ctx context.Context, email string) (*auth.BoundPrincipal, error) {
	if h == nil || h.store == nil {
		return nil, auth.ErrNoBoundPrincipal
	}
	address, err := user.NormalizeEmail(email)
	if err != nil {
		// Not an address the directory could hold, so nobody is bound.
		return nil, auth.ErrNoBoundPrincipal
	}

	var person *user.User
	var subject string
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		found, err := h.store.Get(groupCtx, address)
		if errors.Is(err, user.ErrNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading the account a key is bound to: %w", err)
		}
		person = found
		return nil
	})
	group.Go(func() error {
		found, err := h.subjects.Subject(groupCtx, address)
		if err != nil {
			return fmt.Errorf("reading the subject that account authenticates as: %w", err)
		}
		subject = found
		return nil
	})
	if err := group.Wait(); err != nil {
		return nil, fmt.Errorf("resolving the account a key is bound to: %w", err)
	}
	if person == nil || subject == "" {
		return nil, auth.ErrNoBoundPrincipal
	}
	return &auth.BoundPrincipal{Subject: subject, Email: person.Email, Roles: person.Roles}, nil
}
