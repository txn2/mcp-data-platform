package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// folderFixture is the folder-move route wired over a small library owned by
// sub-1, with the MCP registry callbacks a rename has to fire.
type folderFixture struct {
	h     *Handler
	store *folderStore
	notes *movedNotifier
}

func folderHandler(t *testing.T, claims *Claims) folderFixture {
	t.Helper()
	f := folderFixture{store: newFolderStore(), notes: &movedNotifier{}}
	f.store.fileAt("r-1", "data", "top.csv", "sub-1")
	f.store.fileAt("r-2", "data/shows", "deep.csv", "sub-1")
	deps := Deps{
		Store: f.store, URIScheme: "mcp",
		OnCreate: func(r *Resource) { f.notes.created = append(f.notes.created, r.URI) },
		OnDelete: func(uri string) { f.notes.deleted = append(f.notes.deleted, uri) },
	}
	f.h = NewHandler(deps, func(*http.Request) (*Claims, error) { return claims, nil }, nil)
	return f
}

func postFolderMove(t *testing.T, h http.Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/resources/folders/move", bytes.NewReader(raw)))
	return rec
}

func folderMoveBody(from, to string) map[string]any {
	return map[string]any{"scope": "user", "scope_id": "sub-1", "from": from, "to": to}
}

// TestFolderMoveRouteReportsEachResourceAndReRegistersIt covers both halves of
// what the route owes its caller: the addresses every file now answers at, and
// an MCP registry that no longer lists the folder that is gone.
func TestFolderMoveRouteReportsEachResourceAndReRegistersIt(t *testing.T) {
	f := folderHandler(t, &Claims{Sub: "sub-1", Email: "me@example.com"})

	rec := postFolderMove(t, f.h, folderMoveBody("data", "archive"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	var got FolderMove
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the response: %v", err)
	}
	if got.From != "data" || got.To != "archive" || len(got.Moved) != 2 {
		t.Fatalf("response = %+v", got)
	}
	for _, m := range got.Moved {
		if m.URI == m.FromURI || !strings.HasPrefix(m.Path, "archive") {
			t.Errorf("entry did not move: %+v", m)
		}
	}
	// The registry is keyed on the URI, so every vacated address is withdrawn
	// and every new one registered, or a client keeps listing a folder that no
	// longer exists.
	if len(f.notes.deleted) != 2 || len(f.notes.created) != 2 {
		t.Fatalf("registry callbacks: %d deleted, %d created", len(f.notes.deleted), len(f.notes.created))
	}
	for _, uri := range f.notes.created {
		if !strings.Contains(uri, "/archive/") {
			t.Errorf("re-registered under %q", uri)
		}
	}
}

func TestFolderMoveRouteRefusesABodyItCannotRead(t *testing.T) {
	f := folderHandler(t, &Claims{Sub: "sub-1"})
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/resources/folders/move", strings.NewReader("{not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestFolderMoveRouteRefusesAnUnauthenticatedCaller(t *testing.T) {
	f := folderFixture{store: newFolderStore()}
	f.store.fileAt("r-1", "data", "a.csv", "sub-1")
	h := NewHandler(Deps{Store: f.store, URIScheme: "mcp"},
		func(*http.Request) (*Claims, error) { return nil, errors.New("no token") }, nil)

	if rec := postFolderMove(t, h, folderMoveBody("data", "archive")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

// TestFolderMoveRouteAnswersTheStatusThatSaysWhy maps each refusal onto the code
// a client branches on. Every one of them left the library untouched, which is
// what lets them share one shape.
func TestFolderMoveRouteAnswersTheStatusThatSaysWhy(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		want int
	}{
		{"a folder nothing is filed under", folderMoveBody("nowhere", "archive"), http.StatusNotFound},
		{"a path that breaks the rules", folderMoveBody("data", "Archive"), http.StatusBadRequest},
		{"a library that names nothing", map[string]any{
			"scope": "persona", "from": "data", "to": "archive",
		}, http.StatusBadRequest},
		{"a folder into itself", folderMoveBody("data", "data/inner"), http.StatusConflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := folderHandler(t, &Claims{Sub: "sub-1"})
			rec := postFolderMove(t, f.h, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if f.store.resources["r-1"].Path != "data" {
				t.Error("a refused folder move still wrote")
			}
		})
	}
}

// TestFolderMoveRouteRefusesASubtreeTheCallerCannotChange is the 403 arm: the
// authority to rename a folder is the authority over the files in it.
func TestFolderMoveRouteRefusesASubtreeTheCallerCannotChange(t *testing.T) {
	f := folderHandler(t, &Claims{Sub: "sub-9", Email: "them@example.com", IsAdmin: false})
	// Somebody else's personal library: they may not write it and did not upload
	// what is in it.
	rec := postFolderMove(t, f.h, folderMoveBody("data", "archive"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
}

// TestFolderMoveRouteSurfacesAFailureAsAFailure keeps a broken read from reading
// as a refusal the caller could act on.
func TestFolderMoveRouteSurfacesAFailureAsAFailure(t *testing.T) {
	store := &brokenListStore{folderStore: newFolderStore()}
	store.fileAt("r-1", "data", "a.csv", "sub-1")
	h := NewHandler(Deps{Store: store, URIScheme: "mcp"},
		func(*http.Request) (*Claims, error) { return &Claims{Sub: "sub-1"}, nil }, nil)

	rec := postFolderMove(t, h, folderMoveBody("data", "archive"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "postgres") {
		t.Errorf("the response leaked the cause: %s", rec.Body.String())
	}
}

// brokenListStore fails only the listing, so the route's failure branch is
// reached with every other read intact.
type brokenListStore struct{ *folderStore }

func (*brokenListStore) List(context.Context, Filter) ([]Resource, int, error) {
	return nil, 0, errors.New("reading folder contents: postgres is unreachable")
}

// TestFolderMoveReRegistersFromTheStoredRow guards the read-back: the
// registration has to carry the row as stored, not the plan that produced it,
// and a row that cannot be read back is skipped rather than registered wrong.
func TestFolderMoveReRegistersFromTheStoredRow(t *testing.T) {
	f := folderHandler(t, &Claims{Sub: "sub-1"})
	rec := postFolderMove(t, f.h, folderMoveBody("data/shows", "data/episodes"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if len(f.notes.created) != 1 || f.notes.created[0] != f.store.resources["r-2"].URI {
		t.Errorf("re-registered %v, want the stored row's URI %q",
			f.notes.created, f.store.resources["r-2"].URI)
	}
	// The sibling above the renamed folder is untouched.
	if f.store.resources["r-1"].Path != "data" {
		t.Errorf("r-1 moved: %q", f.store.resources["r-1"].Path)
	}
}

// TestFolderMoveWithoutARegistryStillMoves: a deployment with no MCP
// registration wired must not have its renames fail on the callback.
func TestFolderMoveWithoutARegistryStillMoves(t *testing.T) {
	store := newFolderStore()
	store.fileAt("r-1", "data", "a.csv", "sub-1")
	h := NewHandler(Deps{Store: store, URIScheme: "mcp"},
		func(*http.Request) (*Claims, error) { return &Claims{Sub: "sub-1"}, nil }, nil)

	if rec := postFolderMove(t, h, folderMoveBody("data", "archive")); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if store.resources["r-1"].Path != "archive" {
		t.Errorf("path = %q", store.resources["r-1"].Path)
	}
}

// TestFolderMoveSkipsAResourceItCannotReadBack: the rename committed, so a
// read-back that fails costs one stale registration, not a failed move reported
// to somebody whose files did move.
func TestFolderMoveSkipsAResourceItCannotReadBack(t *testing.T) {
	store := &unreadableAfterMoveStore{folderStore: newFolderStore()}
	store.fileAt("r-1", "data", "a.csv", "sub-1")
	var created []string
	h := NewHandler(Deps{
		Store: store, URIScheme: "mcp",
		OnCreate: func(r *Resource) { created = append(created, r.URI) },
	}, func(*http.Request) (*Claims, error) { return &Claims{Sub: "sub-1"}, nil }, nil)

	if rec := postFolderMove(t, h, folderMoveBody("data", "archive")); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if len(created) != 0 {
		t.Errorf("registered %v from a read-back that failed", created)
	}
	if store.resources["r-1"].Path != "archive" {
		t.Error("the move itself did not commit")
	}
}

// unreadableAfterMoveStore serves every read the plan needs and then refuses the
// read-back, which is the only window the registration refresh runs in.
type unreadableAfterMoveStore struct {
	*folderStore
	moved bool
}

func (s *unreadableAfterMoveStore) Move(ctx context.Context, moves []Move) error {
	if err := s.folderStore.Move(ctx, moves); err != nil {
		return err
	}
	s.moved = true
	return nil
}

func (s *unreadableAfterMoveStore) Get(ctx context.Context, id string) (*Resource, error) {
	if s.moved {
		return nil, errors.New("scanning resource: connection reset")
	}
	return s.folderStore.Get(ctx, id)
}
