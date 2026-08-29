package resource

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// folderStore is the mock store with a listing that filters the way the real
// one does: a folder and everything beneath it, inside one library.
type folderStore struct{ *mockStore }

func newFolderStore() *folderStore { return &folderStore{mockStore: newMockStore()} }

// fileAt puts a resource in the fixture library at one path, addressed the way
// the platform would have minted it.
func (f *folderStore) fileAt(id, path, filename, owner string) *Resource {
	r := &Resource{
		ID: id, Scope: ScopeUser, ScopeID: "sub-1", Path: path,
		Filename: filename, DisplayName: filename,
		URI:         BuildURI("mcp", ScopeUser, "sub-1", path, filename),
		UploaderSub: owner,
	}
	f.resources[id] = r
	return r
}

func folderDeps(store Store) Deps { return Deps{Store: store, URIScheme: "mcp"} }

func fixtureLibrary() ScopeFilter { return ScopeFilter{Scope: ScopeUser, ScopeID: "sub-1"} }

func folderOwner() *Claims {
	return &Claims{Sub: "sub-1", Email: "me@example.com"}
}

// TestMoveFolderRewritesEveryResourceBeneathItAtEveryDepth is the acceptance
// criterion: a rename is a prefix rewrite over the whole subtree, and every
// address it vacates keeps resolving.
func TestMoveFolderRewritesEveryResourceBeneathItAtEveryDepth(t *testing.T) {
	store := newFolderStore()
	store.fileAt("r-1", "data", "top.csv", "sub-1")
	store.fileAt("r-2", "data/media-manager", "mid.csv", "sub-1")
	store.fileAt("r-3", "data/media-manager/shows", "deep.csv", "sub-1")
	// A sibling whose name merely starts with the same letters must not move.
	store.fileAt("r-4", "data-archive", "sibling.csv", "sub-1")

	moves := &recordingMoves{}
	deps := folderDeps(store)
	deps.MoveRecorder = moves

	got, err := MoveFolder(context.Background(), deps, folderOwner(),
		FolderRename{Library: fixtureLibrary(), From: "data", To: "archive"})
	if err != nil {
		t.Fatalf("MoveFolder: %v", err)
	}
	if len(got.Moved) != 3 {
		t.Fatalf("moved %d resources, want 3: %+v", len(got.Moved), got.Moved)
	}

	want := map[string]string{
		"r-1": "mcp://user/sub-1/archive/top.csv",
		"r-2": "mcp://user/sub-1/archive/media-manager/mid.csv",
		"r-3": "mcp://user/sub-1/archive/media-manager/shows/deep.csv",
		"r-4": "mcp://user/sub-1/data-archive/sibling.csv",
	}
	for id, uri := range want {
		if store.resources[id].URI != uri {
			t.Errorf("%s URI = %q, want %q", id, store.resources[id].URI, uri)
		}
	}
	if store.resources["r-3"].Path != "archive/media-manager/shows" {
		t.Errorf("r-3 path = %q", store.resources["r-3"].Path)
	}

	// Every vacated address still resolves, which is what keeps a knowledge page
	// written last year pointing at the file it named.
	for _, old := range []string{
		"mcp://user/sub-1/data/top.csv",
		"mcp://user/sub-1/data/media-manager/mid.csv",
		"mcp://user/sub-1/data/media-manager/shows/deep.csv",
	} {
		r, err := store.GetByURI(context.Background(), old)
		if err != nil || r == nil {
			t.Errorf("the vacated address %s no longer resolves: %v", old, err)
		}
	}
	// One audit event per resource: the trail's question is what address a given
	// file has now, and a single row naming the folder answers it for none.
	if len(moves.events) != 3 {
		t.Errorf("audit events = %d, want 3", len(moves.events))
	}
	for _, ev := range moves.events {
		if ev.FromPath == ev.ToPath || ev.FromURI == ev.ToURI {
			t.Errorf("audit event does not name both folders: %+v", ev)
		}
	}
}

func TestMoveFolderNestsAFolderUnderAnother(t *testing.T) {
	store := newFolderStore()
	store.fileAt("r-1", "weekly", "a.csv", "sub-1")
	store.fileAt("r-2", "weekly/old", "b.csv", "sub-1")

	if _, err := MoveFolder(context.Background(), folderDeps(store), folderOwner(),
		FolderRename{Library: fixtureLibrary(), From: "weekly", To: "data/weekly"}); err != nil {
		t.Fatalf("MoveFolder: %v", err)
	}
	if store.resources["r-1"].URI != "mcp://user/sub-1/data/weekly/a.csv" ||
		store.resources["r-2"].URI != "mcp://user/sub-1/data/weekly/old/b.csv" {
		t.Errorf("nesting produced %q and %q",
			store.resources["r-1"].URI, store.resources["r-2"].URI)
	}
}

// TestMoveFolderUpItsOwnTreeSurvivesTheAddressItsOwnMembersHold is the case the
// store's parking step exists for: moving a/b to a hands one member the address
// another member has not vacated yet.
func TestMoveFolderUpItsOwnTreeSurvivesTheAddressItsOwnMembersHold(t *testing.T) {
	store := newFolderStore()
	store.fileAt("r-1", "a/b", "x.csv", "sub-1")
	store.fileAt("r-2", "a/b/b", "x.csv", "sub-1")

	if _, err := MoveFolder(context.Background(), folderDeps(store), folderOwner(),
		FolderRename{Library: fixtureLibrary(), From: "a/b", To: "a"}); err != nil {
		t.Fatalf("MoveFolder: %v", err)
	}
	if store.resources["r-1"].URI != "mcp://user/sub-1/a/x.csv" ||
		store.resources["r-2"].URI != "mcp://user/sub-1/a/b/x.csv" {
		t.Errorf("addresses after the move: %q and %q",
			store.resources["r-1"].URI, store.resources["r-2"].URI)
	}
}

func TestMoveFolderRefusesWholeOnACollisionAndMovesNothing(t *testing.T) {
	store := newFolderStore()
	store.fileAt("r-1", "data", "a.csv", "sub-1")
	store.fileAt("r-2", "data", "b.csv", "sub-1")
	// The occupant sits at the address r-1 would take.
	occupant := store.fileAt("r-3", "archive", "a.csv", "sub-1")
	occupant.DisplayName = "The Existing One"

	_, err := MoveFolder(context.Background(), folderDeps(store), folderOwner(),
		FolderRename{Library: fixtureLibrary(), From: "data", To: "archive"})
	if !IsFolderMoveRefused(err) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if !strings.Contains(err.Error(), "The Existing One") {
		t.Errorf("the refusal does not name the occupant: %v", err)
	}
	// A half-renamed folder is not a state anyone should be able to observe, so
	// the resource that would not have collided must not have moved either.
	if store.resources["r-2"].Path != "data" {
		t.Errorf("r-2 moved anyway: path = %q", store.resources["r-2"].Path)
	}
}

// TestMoveFolderRefusesASubtreeHoldingAFileTheCallerCannotChange is the rule
// that a folder is not a thing with permissions of its own: the authority to
// rename it is the authority over what is in it.
//
// The library is a persona's, which is the only place a caller can hold part of
// a folder and not the rest: belonging to a persona is not authority over its
// library, so a member may change the files they put there and no others.
func TestMoveFolderRefusesASubtreeHoldingAFileTheCallerCannotChange(t *testing.T) {
	store := newFolderStore()
	mine := &Resource{
		ID: "r-1", Scope: ScopePersona, ScopeID: "ops", Path: "data",
		Filename: "mine.csv", DisplayName: "Mine",
		URI:         BuildURI("mcp", ScopePersona, "ops", "data", "mine.csv"),
		UploaderSub: "sub-1",
	}
	theirs := &Resource{
		ID: "r-2", Scope: ScopePersona, ScopeID: "ops", Path: "data/theirs",
		Filename: "theirs.csv", DisplayName: "Somebody Else's",
		URI:         BuildURI("mcp", ScopePersona, "ops", "data/theirs", "theirs.csv"),
		UploaderSub: "sub-2",
	}
	store.resources["r-1"], store.resources["r-2"] = mine, theirs
	member := &Claims{Sub: "sub-1", Email: "me@example.com", Personas: []string{"ops"}}

	_, err := MoveFolder(context.Background(), folderDeps(store), member,
		FolderRename{Library: ScopeFilter{Scope: ScopePersona, ScopeID: "ops"}, From: "data", To: "archive"})
	if !errors.Is(err, ErrMoveForbidden) {
		t.Fatalf("err = %v, want ErrMoveForbidden", err)
	}
	if !strings.Contains(err.Error(), "Somebody Else's") {
		t.Errorf("the refusal does not name the file that stopped it: %v", err)
	}
	if store.resources["r-1"].Path != "data" {
		t.Error("a refused folder move still wrote")
	}
}

func TestMoveFolderRefusesTheShapesNoRewriteCanExpress(t *testing.T) {
	store := newFolderStore()
	store.fileAt("r-1", "data", "a.csv", "sub-1")

	tests := []struct {
		name, from, to, says string
		invalidPath          bool
	}{
		{name: "into itself", from: "data", to: "data/inner", says: "cannot hold it"},
		{name: "to where it is", from: "data", to: "data", says: "already where it is"},
		{name: "from an unusable path", from: "Data", to: "archive", says: "must match", invalidPath: true},
		{name: "to an unusable path", from: "data", to: "archive/", says: "start or end", invalidPath: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := MoveFolder(context.Background(), folderDeps(store), folderOwner(),
				FolderRename{Library: fixtureLibrary(), From: tc.from, To: tc.to})
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("err = %v, want it to name %q", err, tc.says)
			}
			if IsInvalidPath(err) != tc.invalidPath {
				t.Errorf("IsInvalidPath = %v, want %v", IsInvalidPath(err), tc.invalidPath)
			}
		})
	}
}

func TestMoveFolderRefusesAnUnusableLibrary(t *testing.T) {
	store := newFolderStore()
	_, err := MoveFolder(context.Background(), folderDeps(store), folderOwner(),
		FolderRename{Library: ScopeFilter{Scope: ScopePersona}, From: "data", To: "archive"})
	if !IsInvalidScope(err) {
		t.Fatalf("err = %v, want an invalid-scope error", err)
	}
}

// TestMoveFolderRefusesAFolderNothingIsFiledUnder is the consequence of folders
// being derived from the paths in use rather than stored as rows: a folder with
// nothing under it does not exist, so there is nothing to rename.
func TestMoveFolderRefusesAFolderNothingIsFiledUnder(t *testing.T) {
	store := newFolderStore()
	store.fileAt("r-1", "data", "a.csv", "sub-1")

	_, err := MoveFolder(context.Background(), folderDeps(store), folderOwner(),
		FolderRename{Library: fixtureLibrary(), From: "nothing-here", To: "archive"})
	if !errors.Is(err, ErrFolderEmpty) {
		t.Fatalf("err = %v, want ErrFolderEmpty", err)
	}
}

// TestMoveFolderRefusesASubtreePastTheCapRatherThanMovingPartOfIt states the
// bound rather than truncating at it: a rename that carried the first N files
// and left the rest is the half-renamed folder the transaction exists to
// prevent, reported as a success.
func TestMoveFolderRefusesASubtreePastTheCapRatherThanMovingPartOfIt(t *testing.T) {
	store := newFolderStore()
	for i := range MaxFolderMoveResources + 1 {
		store.fileAt(fmt.Sprintf("r-%d", i), "data", fmt.Sprintf("f%d.csv", i), "sub-1")
	}

	_, err := MoveFolder(context.Background(), folderDeps(store), folderOwner(),
		FolderRename{Library: fixtureLibrary(), From: "data", To: "archive"})
	if !IsFolderMoveRefused(err) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d resources", MaxFolderMoveResources+1)) {
		t.Errorf("the refusal does not state the true count: %v", err)
	}
	if store.resources["r-0"].Path != "data" {
		t.Error("a refused folder move still wrote")
	}
}

func TestIsFolderMoveRefusedIgnoresOtherErrors(t *testing.T) {
	if IsFolderMoveRefused(errors.New("connection refused")) || IsFolderMoveRefused(nil) {
		t.Error("an unrelated error read as a refusal")
	}
}

// TestMoveFolderRefusesALibraryTheCallerCannotSee closes the listing the route
// would otherwise be: naming somebody else's library and guessing a path would
// answer 404 for a folder that is not there and 403 for one that is.
func TestMoveFolderRefusesALibraryTheCallerCannotSee(t *testing.T) {
	store := newFolderStore()
	store.fileAt("r-1", "data", "a.csv", "sub-2")
	store.resources["r-1"].ScopeID = "sub-2"

	stranger := &Claims{Sub: "sub-9", Email: "them@example.com"}
	for _, tc := range []struct {
		name string
		from string
	}{
		{"a folder that is there", "data"},
		{"a folder that is not", "nowhere"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := MoveFolder(context.Background(), folderDeps(store), stranger,
				FolderRename{Library: ScopeFilter{Scope: ScopeUser, ScopeID: "sub-2"}, From: tc.from, To: "archive"})
			// The same answer either way, which is what stops the route
			// reporting what is in a library the caller cannot list.
			if !errors.Is(err, ErrMoveForbidden) {
				t.Fatalf("err = %v, want ErrMoveForbidden", err)
			}
		})
	}
}

// TestMoveFolderLetsAnAdministratorReorganizeALibraryTheyDoNotBelongTo is the
// other half of the same rule: write authority over a library is authority to
// reorganize it, or an admin could publish a persona resource and never file it.
func TestMoveFolderLetsAnAdministratorReorganizeALibraryTheyDoNotBelongTo(t *testing.T) {
	store := newFolderStore()
	r := store.fileAt("r-1", "data", "a.csv", "someone")
	r.Scope, r.ScopeID = ScopePersona, "ops"
	r.URI = BuildURI("mcp", ScopePersona, "ops", "data", "a.csv")

	admin := &Claims{Sub: "sub-admin", Email: "admin@example.com", IsAdmin: true}
	if _, err := MoveFolder(context.Background(), folderDeps(store), admin,
		FolderRename{Library: ScopeFilter{Scope: ScopePersona, ScopeID: "ops"}, From: "data", To: "archive"}); err != nil {
		t.Fatalf("MoveFolder: %v", err)
	}
	if store.resources["r-1"].Path != "archive" {
		t.Errorf("path = %q", store.resources["r-1"].Path)
	}
}
