package resource

import (
	"context"
	"errors"
	"fmt"
)

// MoveEvent describes one resource refiled into another library, as the audit
// trail records it: who moved what, out of which library and into which.
//
// Both URIs are carried because the move rewrites the address. An event naming
// only the resource id would answer "who published this to everyone" but not
// "what is the address a knowledge page written last year still cites", and the
// second question is the one an alias exists to answer.
type MoveEvent struct {
	ResourceID  string
	DisplayName string
	FromScope   Scope
	FromScopeID string
	FromURI     string
	ToScope     Scope
	ToScopeID   string
	ToURI       string
	UserID      string
	UserEmail   string
}

// MoveRecorder records a completed move.
//
// Implementations are best-effort and must not fail the move: the resource has
// already been refiled by the time this is called, and refusing to report a
// completed write because its audit row would not persist would be a lie about
// what happened. Recording is off entirely when audit is disabled, which is why
// every call site tolerates a nil recorder.
type MoveRecorder interface {
	RecordMove(ctx context.Context, ev MoveEvent)
}

// ErrMoveForbidden is returned when the caller may modify the resource but may
// not file it in the library they named.
var ErrMoveForbidden = errors.New("insufficient permissions for target library")

// moveConflictError reports that the target library already holds a resource at
// the address the moved one would take. It names the occupant, because the
// person moving the file cannot see the other library's contents and "that
// address is taken" with nothing else is an error they can do nothing about.
type moveConflictError struct{ msg string }

func (e *moveConflictError) Error() string { return e.msg }

// IsMoveConflict reports whether an error from MoveResource means the target
// library already holds a resource at the destination URI.
func IsMoveConflict(err error) bool {
	var c *moveConflictError
	return errors.As(err, &c) || errors.Is(err, ErrURIConflict)
}

// MoveResource files an existing resource in another library.
//
// The caller has already established that claims may modify res
// (CanModifyResource); this checks the destination half and performs the write.
// It returns the moved resource's new URI, or ("", nil) when the resource is
// already in the named library -- a move to where the file already is is not an
// error, and refusing it would make an idempotent PATCH fail.
//
// The blob is not copied and the reference rows are not touched. An asset or a
// prompt that declared this resource keeps rendering it: both key on the
// resource id, and the serve-time rewrite matches the URI string recorded on the
// reference row, which is what the author wrote and stays what they wrote.
func MoveResource(ctx context.Context, deps Deps, claims *Claims, res *Resource, to ScopeFilter) (string, error) {
	if err := ValidateScope(to.Scope, to.ScopeID); err != nil {
		return "", err
	}
	if res.Scope == to.Scope && res.ScopeID == to.ScopeID {
		return "", nil
	}
	if !CanMoveToLibrary(*claims, to.Scope, to.ScopeID) {
		return "", ErrMoveForbidden
	}

	scheme := deps.URIScheme
	if scheme == "" {
		scheme = DefaultURIScheme
	}
	uri := MovedURI(scheme, res, to.Scope, to.ScopeID)

	if err := checkURIFree(ctx, deps, res.ID, uri); err != nil {
		return "", err
	}

	// The library being left is read before the write, not after. A store is
	// free to hand back the same *Resource it later mutates, and every "from"
	// field -- the alias recorded, the audit event's origin -- would then name
	// the destination on both sides.
	ev := MoveEvent{
		ResourceID: res.ID, DisplayName: res.DisplayName,
		FromScope: res.Scope, FromScopeID: res.ScopeID, FromURI: res.URI,
		ToScope: to.Scope, ToScopeID: to.ScopeID, ToURI: uri,
		UserID: claims.Sub, UserEmail: PersonAddress(*claims),
	}
	dest := Move{Scope: to.Scope, ScopeID: to.ScopeID, URI: uri, FromURI: res.URI}
	if err := deps.Store.Move(ctx, res.ID, dest); err != nil {
		return "", fmt.Errorf("refiling resource: %w", err)
	}

	// No-op without a recorder (audit disabled, or no database), and never fails
	// the move: the resource has already been refiled, and refusing to report a
	// completed write because its audit row would not persist would be a lie
	// about what happened.
	if deps.MoveRecorder != nil {
		deps.MoveRecorder.RecordMove(ctx, ev)
	}
	return uri, nil
}

// checkURIFree refuses a move onto an address another resource occupies, naming
// the occupant.
//
// The comparison on the returned URI is what separates a live collision from an
// alias. GetByURI resolves an address a resource has vacated as well as one it
// holds, so a hit whose own URI is not the address asked about is a previous
// occupant -- exactly the case a move onto that address is allowed to reclaim,
// and the case the store's own write clears the alias for.
//
// A read that fails is not a free address: a move that then hit the UNIQUE
// constraint would report a database error where the answer was "taken", so the
// failure is surfaced.
func checkURIFree(ctx context.Context, deps Deps, id, uri string) error {
	existing, err := deps.Store.GetByURI(ctx, uri)
	if err != nil {
		if IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("checking target library: %w", err)
	}
	if existing == nil || existing.ID == id || existing.URI != uri {
		return nil
	}
	return &moveConflictError{msg: fmt.Sprintf(
		"the target library already holds %q at %s; rename or remove it first",
		existing.DisplayName, uri)}
}
