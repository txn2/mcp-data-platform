package resource

import (
	"context"
	"errors"
	"testing"
)

// The address-keyed library a run filed into before the platform knew its
// author's subject is folded into the subject-keyed one (#1677): every file is
// refiled at the same path with the same id, the vacated address resolves as an
// alias, and a path the subject-keyed library already has is left alone.

func foldFrom() ScopeFilter { return ScopeFilter{Scope: ScopeUser, ScopeID: "author@example.com"} }
func foldTo() ScopeFilter   { return ScopeFilter{Scope: ScopeUser, ScopeID: "author-sub"} }
func foldActor() Claims     { return Claims{Sub: "author-sub", Email: "author@example.com"} }

// fileIn puts a resource in a library at a path, addressed as the platform
// would have minted it.
func (f *folderStore) fileIn(id, scopeID, path, filename string) {
	f.resources[id] = &Resource{
		ID: id, Scope: ScopeUser, ScopeID: scopeID, Path: path,
		Filename: filename, DisplayName: filename,
		URI: BuildURI("mcp", ScopeUser, scopeID, path, filename),
	}
}

func TestFoldLibrary_RefilesEveryFileAtItsOwnPath(t *testing.T) {
	store := newFolderStore()
	store.fileIn("r-1", "author@example.com", "qa", "rolling.csv")
	store.fileIn("r-2", "author@example.com", "qa/deep", "other.csv")
	store.fileIn("r-3", "author-sub", "qa", "session.csv") // already where it belongs
	store.fileIn("r-4", "someone-else", "qa", "rolling.csv")

	var created, deleted []string
	moves := &recordingMoves{}
	deps := folderDeps(store)
	deps.MoveRecorder = moves
	deps.OnCreate = func(r *Resource) { created = append(created, r.URI) }
	deps.OnDelete = func(uri string) { deleted = append(deleted, uri) }

	got, err := FoldLibrary(context.Background(), deps, foldFrom(), foldTo(), foldActor())
	if err != nil {
		t.Fatalf("FoldLibrary: %v", err)
	}
	if len(got.Moved) != 2 || len(got.Skipped) != 0 {
		t.Fatalf("fold = %+v", got)
	}
	want := map[string]string{
		"r-1": "mcp://user/author-sub/qa/rolling.csv",
		"r-2": "mcp://user/author-sub/qa/deep/other.csv",
		"r-3": "mcp://user/author-sub/qa/session.csv",
		"r-4": "mcp://user/someone-else/qa/rolling.csv",
	}
	for id, uri := range want {
		if r := store.resources[id]; r.URI != uri {
			t.Errorf("%s URI = %q, want %q", id, r.URI, uri)
		}
	}
	if r := store.resources["r-1"]; r.ScopeID != "author-sub" || r.Path != "qa" {
		t.Errorf("r-1 = %+v", r)
	}
	// The vacated address keeps resolving, so a script body naming it does.
	old, err := store.GetByURI(context.Background(), "mcp://user/author@example.com/qa/rolling.csv")
	if err != nil || old == nil || old.ID != "r-1" {
		t.Errorf("the vacated address does not resolve: %v, %v", old, err)
	}
	// The registry is re-pointed, and the trail names the person.
	if len(deleted) != 2 || len(created) != 2 {
		t.Errorf("registry callbacks: deleted %v, created %v", deleted, created)
	}
	if len(moves.events) != 2 {
		t.Fatalf("recorded %d moves, want 2", len(moves.events))
	}
	ev := moves.events[0]
	if ev.UserID != "author-sub" || ev.UserEmail != "author@example.com" || ev.ToScopeID != "author-sub" {
		t.Errorf("move event = %+v", ev)
	}
}

func TestFoldLibrary_LeavesAFileWhosePathIsTaken(t *testing.T) {
	store := newFolderStore()
	store.fileIn("run-made", "author@example.com", "qa", "probe.csv")
	store.fileIn("session-made", "author-sub", "qa", "probe.csv")
	store.fileIn("free", "author@example.com", "qa", "free.csv")

	got, err := FoldLibrary(context.Background(), folderDeps(store), foldFrom(), foldTo(), foldActor())
	if err != nil {
		t.Fatalf("FoldLibrary: %v", err)
	}
	if len(got.Moved) != 1 || got.Moved[0].ID != "free" {
		t.Errorf("moved = %+v", got.Moved)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].ID != "run-made" {
		t.Errorf("skipped = %+v", got.Skipped)
	}
	// The session's file stays what the path names, and the run's stays
	// reachable where it was.
	if store.resources["session-made"].URI != "mcp://user/author-sub/qa/probe.csv" {
		t.Errorf("the occupant moved: %+v", store.resources["session-made"])
	}
	if store.resources["run-made"].URI != "mcp://user/author@example.com/qa/probe.csv" {
		t.Errorf("the skipped file moved: %+v", store.resources["run-made"])
	}
}

func TestFoldLibrary_ReclaimsAnAddressHeldOnlyAsAnAlias(t *testing.T) {
	store := newFolderStore()
	store.fileIn("moved-away", "author-sub", "archive", "probe.csv")
	store.aliases = map[string]string{"mcp://user/author-sub/qa/probe.csv": "moved-away"}
	store.fileIn("run-made", "author@example.com", "qa", "probe.csv")

	got, err := FoldLibrary(context.Background(), folderDeps(store), foldFrom(), foldTo(), foldActor())
	if err != nil {
		t.Fatalf("FoldLibrary: %v", err)
	}
	if len(got.Moved) != 1 || len(got.Skipped) != 0 {
		t.Fatalf("fold = %+v", got)
	}
	if r := store.resources["run-made"]; r.URI != "mcp://user/author-sub/qa/probe.csv" {
		t.Errorf("run-made = %+v", r)
	}
}

func TestFoldLibrary_NothingToFoldIsNotAnError(t *testing.T) {
	store := newFolderStore()
	store.fileIn("r-3", "author-sub", "qa", "session.csv")
	tests := []struct {
		name     string
		from, to ScopeFilter
	}{
		{"an empty address library", foldFrom(), foldTo()},
		{"the same library twice", foldTo(), foldTo()},
		{"no address", ScopeFilter{Scope: ScopeUser}, foldTo()},
		{"no subject", foldFrom(), ScopeFilter{Scope: ScopeUser}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FoldLibrary(context.Background(), folderDeps(store), tc.from, tc.to, foldActor())
			if err != nil || len(got.Moved) != 0 || len(got.Skipped) != 0 {
				t.Fatalf("fold = %+v, %v", got, err)
			}
		})
	}
}

func TestFoldLibrary_ReportsAStoreThatCannotAnswer(t *testing.T) {
	t.Run("listing", func(t *testing.T) {
		store := &brokenListStore{folderStore: newFolderStore()}
		if _, err := FoldLibrary(context.Background(), folderDeps(store), foldFrom(), foldTo(), foldActor()); err == nil {
			t.Fatal("a listing that failed must not read as an empty library")
		}
	})
	t.Run("target check", func(t *testing.T) {
		store := &brokenLookupStore{folderStore: newFolderStore()}
		store.fileIn("r-1", "author@example.com", "qa", "rolling.csv")
		if _, err := FoldLibrary(context.Background(), folderDeps(store), foldFrom(), foldTo(), foldActor()); err == nil {
			t.Fatal("a lookup that failed must not read as a free address")
		}
	})
	t.Run("a race for the address", func(t *testing.T) {
		store := &conflictingMoveStore{folderStore: newFolderStore()}
		store.fileIn("r-1", "author@example.com", "qa", "rolling.csv")
		_, err := FoldLibrary(context.Background(), folderDeps(store), foldFrom(), foldTo(), foldActor())
		if !errors.Is(err, ErrURIConflict) {
			t.Fatalf("err = %v, want the conflict", err)
		}
	})
	t.Run("a move the store refuses", func(t *testing.T) {
		store := &failingMoveStore{folderStore: newFolderStore()}
		store.fileIn("r-1", "author@example.com", "qa", "rolling.csv")
		if _, err := FoldLibrary(context.Background(), folderDeps(store), foldFrom(), foldTo(), foldActor()); err == nil {
			t.Fatal("a refused move must be reported")
		}
	})
}

type brokenLookupStore struct{ *folderStore }

func (*brokenLookupStore) GetByURI(context.Context, string) (*Resource, error) {
	return nil, errors.New("database away")
}

type conflictingMoveStore struct{ *folderStore }

func (*conflictingMoveStore) Move(context.Context, []Move) error { return ErrURIConflict }

type failingMoveStore struct{ *folderStore }

func (*failingMoveStore) Move(context.Context, []Move) error { return errors.New("disk full") }
