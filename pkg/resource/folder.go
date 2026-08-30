package resource

import (
	"context"
	"errors"
	"fmt"
)

// MaxFolderMoveResources bounds how many resources one folder move may rewrite.
//
// The move is one transaction and one audit event per resource, both of which
// are paid inside the request. The cap is a refusal rather than a truncation: a
// rename that moved the first five hundred files and left the rest behind would
// be the half-renamed folder the transaction exists to prevent, reported as a
// success.
const MaxFolderMoveResources = 500

// ErrFolderEmpty is returned when no resource in the library lies under the
// folder being moved. Folders are derived from the paths in use (#1529), so a
// folder with nothing under it does not exist and cannot be renamed.
var ErrFolderEmpty = errors.New("no resources are filed under that folder")

// folderMoveError reports a folder move refused whole, carrying the sentence the
// person is shown. Every one of them leaves the library exactly as it was.
type folderMoveError struct{ msg string }

func (e *folderMoveError) Error() string { return e.msg }

// IsFolderMoveRefused reports whether a folder move was refused for a reason the
// caller can state and act on, as opposed to having failed.
func IsFolderMoveRefused(err error) bool {
	var e *folderMoveError
	return errors.As(err, &e)
}

// folderForbiddenError is the one refusal above that is about authority rather
// than about addresses, so it answers 403 while still carrying the sentence
// naming the file that stopped the move.
type folderForbiddenError struct{ msg string }

func (e *folderForbiddenError) Error() string { return e.msg }

// Unwrap makes the refusal match ErrMoveForbidden, so a caller that already
// distinguishes "you may not file it there" needs no second check for "you may
// not move what is in it".
func (*folderForbiddenError) Unwrap() error { return ErrMoveForbidden }

// FolderRename names a folder in one library and where it is going.
//
// The library is part of it rather than derived from the folder, because a path
// is only unique inside one: "data/weekly" names a different folder in every
// library that has one.
type FolderRename struct {
	Library ScopeFilter
	From    string
	To      string
}

// FolderMove is what one folder move did: the resources it rewrote, and the
// address each of them now answers at.
type FolderMove struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Moved is one entry per resource rewritten, in listing order.
	Moved []FolderMoveEntry `json:"moved"`
}

// FolderMoveEntry is one resource carried by a folder move.
type FolderMoveEntry struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	URI     string `json:"uri"`
	FromURI string `json:"from_uri"`
}

// MoveFolder renames a folder, or nests it under another one, by rewriting the
// path prefix of every resource beneath it.
//
// The whole subtree moves in one transaction and every refusal leaves it
// untouched, because a half-renamed folder is not a state anyone should be able
// to observe (#1529). Each resource records the address it vacated in the alias
// table, so a citation written against the old address keeps resolving -- the
// same machinery a single move uses, applied once per resource.
//
// The caller must be able to modify every resource under the folder. A folder is
// not a thing with permissions of its own -- it exists because resources are
// filed under it -- so the authority to move it is the authority over what is in
// it, and a subtree holding one file the caller may not touch is refused whole
// rather than moved in part.
func MoveFolder(ctx context.Context, deps Deps, claims *Claims, move FolderRename) (*FolderMove, error) {
	lib, from, to := move.Library, move.From, move.To
	if err := validateFolderMove(lib, from, to); err != nil {
		return nil, err
	}
	// The library has to be one the caller can see before its contents are read.
	// Without this, naming somebody else's library and guessing a path would
	// answer 404 for a folder that is not there and 403 for one that is, which
	// is a listing of a library the caller cannot list.
	if !CanSeeLibrary(*claims, lib) {
		return nil, ErrMoveForbidden
	}
	found, err := listFolder(ctx, deps, lib, from)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, ErrFolderEmpty
	}
	if len(found) > MaxFolderMoveResources {
		return nil, &folderMoveError{msg: fmt.Sprintf(
			"%d resources are filed under %q, more than the %d one move rewrites; move a subfolder at a time",
			len(found), from, MaxFolderMoveResources)}
	}

	scheme := deps.URIScheme
	if scheme == "" {
		scheme = DefaultURIScheme
	}
	return writeFolderMove(ctx, deps, claims, folderPlan{scheme: scheme, lib: lib, from: from, to: to}, found)
}

// writeFolderMove plans the batch, refuses it on any conflict, writes it in one
// transaction and records what it carried.
func writeFolderMove(
	ctx context.Context, deps Deps, claims *Claims, p folderPlan, found []Resource,
) (*FolderMove, error) {
	moves, events, err := planFolderMove(p, claims, found)
	if err != nil {
		return nil, err
	}
	if err := checkFolderTargets(ctx, deps, moves); err != nil {
		return nil, err
	}

	if err := deps.Store.Move(ctx, moves); err != nil {
		if errors.Is(err, ErrURIConflict) {
			return nil, &folderMoveError{msg: fmt.Sprintf(
				"another resource already answers at an address under %q; nothing was moved", p.to)}
		}
		return nil, fmt.Errorf("moving folder: %w", err)
	}

	// Recording is best-effort and per resource, the same trail a single move
	// writes: the question the trail answers is what address a given file now
	// has, and one row naming the folder would not answer it for any of them.
	if deps.MoveRecorder != nil {
		for _, ev := range events {
			deps.MoveRecorder.RecordMove(ctx, ev)
		}
	}

	out := &FolderMove{From: p.from, To: p.to, Moved: make([]FolderMoveEntry, 0, len(moves))}
	for _, m := range moves {
		out.Moved = append(out.Moved, FolderMoveEntry{ID: m.ID, Path: m.Path, URI: m.URI, FromURI: m.FromURI})
	}
	return out, nil
}

// validateFolderMove checks the library and the two paths, including the one
// shape a path pair can take that no rewrite can express.
func validateFolderMove(lib ScopeFilter, from, to string) error {
	if err := ValidateScope(lib.Scope, lib.ScopeID); err != nil {
		return err
	}
	if err := ValidatePath(from); err != nil {
		return &invalidPathError{msg: err.Error()}
	}
	if err := ValidatePath(to); err != nil {
		return &invalidPathError{msg: err.Error()}
	}
	if from == to {
		return &folderMoveError{msg: fmt.Sprintf("%q is already where it is", from)}
	}
	// Nesting a folder inside itself has no result: every resource beneath it
	// would be rewritten to a path beneath its own new location, forever.
	if PathUnder(to, from) {
		return &folderMoveError{msg: fmt.Sprintf("%q is inside %q and cannot hold it", to, from)}
	}
	return nil
}

// listFolder reads every resource in the library filed under the folder, paging
// until the listing is exhausted or the cap is passed.
//
// It reads one page past the cap deliberately: the refusal above states how many
// resources are under the folder, and a read that stopped at the cap could only
// say "at least this many", which is not a number anyone can act on. Reading one
// more page bounds the overshoot while still reporting a true count for every
// subtree the cap is close to.
func listFolder(ctx context.Context, deps Deps, lib ScopeFilter, from string) ([]Resource, error) {
	filter := Filter{Scopes: []ScopeFilter{lib}, Path: from, Limit: MaxListLimit}
	var found []Resource
	for {
		page, total, err := deps.Store.List(ctx, filter)
		if err != nil {
			return nil, fmt.Errorf("reading folder contents: %w", err)
		}
		found = append(found, page...)
		if len(page) == 0 || len(found) >= total || len(found) > MaxFolderMoveResources {
			return found, nil
		}
		filter.Offset = len(found)
	}
}

// folderPlan is one folder move, as everything the plan is derived from: the
// library, the prefix pair, and the URI scheme addresses are minted under.
type folderPlan struct {
	scheme string
	lib    ScopeFilter
	from   string
	to     string
}

// planFolderMove turns the resources under a folder into the relocations that
// carry them, refusing the whole move if the caller may not modify one of them
// or if two of them would land on the same address.
func planFolderMove(p folderPlan, claims *Claims, found []Resource) ([]Move, []MoveEvent, error) {
	moves := make([]Move, 0, len(found))
	events := make([]MoveEvent, 0, len(found))
	taken := make(map[string]string, len(found))
	for i := range found {
		r := &found[i]
		move, err := p.relocate(claims, r, taken)
		if err != nil {
			return nil, nil, err
		}
		taken[move.URI] = r.DisplayName
		moves = append(moves, move)
		events = append(events, MoveEvent{
			ResourceID: r.ID, DisplayName: r.DisplayName,
			FromScope: r.Scope, FromScopeID: r.ScopeID, FromPath: r.Path, FromURI: r.URI,
			ToScope: p.lib.Scope, ToScopeID: p.lib.ScopeID, ToPath: move.Path, ToURI: move.URI,
			UserID: claims.Sub, UserEmail: PersonAddress(*claims),
		})
	}
	return moves, events, nil
}

// relocate is where one resource under the folder is going, refusing it for a
// reason the whole move is then abandoned for.
//
// taken is what the resources planned before it have claimed, which is the one
// collision the database cannot report usefully: two rows of the same batch
// landing on one address fail as a constraint violation naming neither.
func (p folderPlan) relocate(claims *Claims, r *Resource, taken map[string]string) (Move, error) {
	if !CanModifyResource(*claims, r) {
		return Move{}, &folderForbiddenError{msg: fmt.Sprintf(
			"%q is under %q and you may not change it; nothing was moved", r.DisplayName, p.from)}
	}
	path := RepointPath(r.Path, p.from, p.to)
	if err := ValidatePath(path); err != nil {
		return Move{}, &invalidPathError{msg: err.Error()}
	}
	uri := RelocatedURI(p.scheme, r, p.lib.Scope, p.lib.ScopeID, path)
	if other, dup := taken[uri]; dup {
		return Move{}, &folderMoveError{msg: fmt.Sprintf(
			"%q and %q would both answer at %s; nothing was moved", other, r.DisplayName, uri)}
	}
	return Move{
		ID: r.ID, Scope: p.lib.Scope, ScopeID: p.lib.ScopeID,
		Path: path, URI: uri, FromURI: r.URI,
	}, nil
}

// checkFolderTargets refuses the move when a resource outside the subtree
// already answers at one of the addresses it would take, naming that resource.
//
// The database enforces this too, and the store reports its rejection as
// ErrURIConflict. Reading first is what turns "that address is taken" into a
// sentence naming the file holding it, which is the difference between a
// refusal someone can act on and one they cannot.
func checkFolderTargets(ctx context.Context, deps Deps, moves []Move) error {
	moving := make(map[string]bool, len(moves))
	for _, m := range moves {
		moving[m.ID] = true
	}
	for _, m := range moves {
		existing, err := deps.Store.GetByURI(ctx, m.URI)
		if err != nil {
			if IsNotFound(err) {
				continue
			}
			return fmt.Errorf("checking target folder: %w", err)
		}
		// A hit whose own URI is not the address asked about is a previous
		// occupant resolved through the alias table, which this move reclaims.
		if existing == nil || existing.URI != m.URI || moving[existing.ID] {
			continue
		}
		return &folderMoveError{msg: fmt.Sprintf(
			"%q already answers at %s; nothing was moved", existing.DisplayName, m.URI)}
	}
	return nil
}
