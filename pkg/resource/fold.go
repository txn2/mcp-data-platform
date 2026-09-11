package resource

import (
	"context"
	"errors"
	"fmt"
)

// A user library is keyed by subject, and a managed-script run that knew its
// author only by address filed the same path in a second library keyed by that
// address (#1677). Folding is how the second library is put back into the first
// once the platform knows which subject the address authenticates as: every
// resource filed under the address-keyed library is refiled, same folder path
// and same filename, into the subject-keyed one. The resource keeps its id, so
// every reference to it resolves as before, and the address it vacates becomes
// an alias, so a citation written against the old URI resolves too.

// FoldEntry is one resource a fold considered.
type FoldEntry struct {
	ID      string
	URI     string
	FromURI string
}

// Fold is what a fold did: the resources now filed in the target library, and
// the ones left where they were because the target address was already taken.
type Fold struct {
	Moved   []FoldEntry
	Skipped []FoldEntry
}

// FoldLibrary refiles every resource in the from library into the to library,
// keeping each one's folder path and filename. It is a platform operation
// rather than a caller's: actor names the person both libraries belong to,
// which is what the move trail records, and no authority check is made beyond
// that -- the two libraries are one person's, keyed two ways.
//
// A resource whose target address is already occupied is skipped rather than
// failing the fold: the occupant is the file the person's own session made at
// that path, and it stays what that path names. The skipped file stays
// reachable by its reference and in the address-keyed library, which the
// person's session still lists.
//
// Nothing to fold is an empty Fold, not an error, which is what makes this
// cheap to call on every occasion the pair is learned.
func FoldLibrary(ctx context.Context, deps Deps, from, to ScopeFilter, actor Claims) (*Fold, error) {
	out := &Fold{}
	if from == to || from.ScopeID == "" || to.ScopeID == "" {
		return out, nil
	}
	scheme := deps.URIScheme
	if scheme == "" {
		scheme = DefaultURIScheme
	}
	// A skipped row stays in the from library, so every later listing returns
	// it again; it is considered once.
	f := &folder{deps: deps, scheme: scheme, to: to, actor: actor, out: out, skipped: map[string]bool{}}
	for {
		found, err := listFolder(ctx, deps, from, "")
		if err != nil {
			return out, err
		}
		found = f.withoutSkipped(found)
		if len(found) == 0 {
			return out, nil
		}
		if len(found) > MaxFolderMoveResources {
			found = found[:MaxFolderMoveResources]
		}
		if err := f.batch(ctx, found); err != nil {
			return out, err
		}
	}
}

// folder is one fold in progress: where the rows go, who is recorded as moving
// them, what has been done, and which rows were left where they were.
type folder struct {
	deps    Deps
	scheme  string
	to      ScopeFilter
	actor   Claims
	out     *Fold
	skipped map[string]bool
}

// withoutSkipped drops the rows an earlier batch already left where they were.
func (f *folder) withoutSkipped(found []Resource) []Resource {
	if len(f.skipped) == 0 {
		return found
	}
	kept := found[:0]
	for _, r := range found {
		if !f.skipped[r.ID] {
			kept = append(kept, r)
		}
	}
	return kept
}

// batch plans, checks and writes one batch, recording each row on out and each
// row it left alone in skipped.
func (f *folder) batch(ctx context.Context, found []Resource) error {
	moves := make([]Move, 0, len(found))
	events := make([]MoveEvent, 0, len(found))
	updated := make([]*Resource, 0, len(found))
	for i := range found {
		r := &found[i]
		uri := RelocatedURI(f.scheme, r, f.to.Scope, f.to.ScopeID, r.Path)
		taken, err := addressTaken(ctx, f.deps, r.ID, uri)
		if err != nil {
			return err
		}
		if taken {
			f.skipped[r.ID] = true
			f.out.Skipped = append(f.out.Skipped, FoldEntry{ID: r.ID, URI: r.URI, FromURI: r.URI})
			continue
		}
		moves = append(moves, Move{ID: r.ID, Scope: f.to.Scope, ScopeID: f.to.ScopeID, Path: r.Path, URI: uri, FromURI: r.URI})
		events = append(events, MoveEvent{
			ResourceID: r.ID, DisplayName: r.DisplayName,
			FromScope: r.Scope, FromScopeID: r.ScopeID, FromPath: r.Path, FromURI: r.URI,
			ToScope: f.to.Scope, ToScopeID: f.to.ScopeID, ToPath: r.Path, ToURI: uri,
			UserID: f.actor.Sub, UserEmail: PersonAddress(f.actor),
		})
		after := *r
		after.Scope, after.ScopeID, after.URI = f.to.Scope, f.to.ScopeID, uri
		updated = append(updated, &after)
	}
	if len(moves) == 0 {
		return nil
	}
	if err := f.deps.Store.Move(ctx, moves); err != nil {
		if errors.Is(err, ErrURIConflict) {
			return fmt.Errorf("folding %s into %s: an address was taken while the fold ran: %w",
				moves[0].FromURI, f.to.ScopeID, err)
		}
		return fmt.Errorf("folding library: %w", err)
	}
	f.announce(ctx, moves, events, updated)
	return nil
}

// announce records each completed move on out and in the move trail, and
// re-points the MCP registry: it is keyed on the URI, so the vacated address is
// withdrawn and the new one registered, as a PATCH move does.
func (f *folder) announce(ctx context.Context, moves []Move, events []MoveEvent, updated []*Resource) {
	for i, m := range moves {
		f.out.Moved = append(f.out.Moved, FoldEntry{ID: m.ID, URI: m.URI, FromURI: m.FromURI})
		if f.deps.MoveRecorder != nil {
			f.deps.MoveRecorder.RecordMove(ctx, events[i])
		}
		if f.deps.OnDelete != nil {
			f.deps.OnDelete(m.FromURI)
		}
		if f.deps.OnCreate != nil {
			f.deps.OnCreate(updated[i])
		}
	}
}

// addressTaken reports whether a live resource other than id occupies uri. A
// hit whose own URI differs is a previous occupant resolved through the alias
// table, which the move reclaims.
func addressTaken(ctx context.Context, deps Deps, id, uri string) (bool, error) {
	existing, err := deps.Store.GetByURI(ctx, uri)
	if err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("checking the target address: %w", err)
	}
	return existing != nil && existing.URI == uri && existing.ID != id, nil
}
