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

// fakeFolders is a FolderStore and PeopleLister whose answers a test sets.
type fakeFolders struct {
	createErr, deleteErr error
	exists               bool
	existsErr            error
	moved                []FolderRename
	moveErr              error
	people               []Person
	peopleErr            error
	created, deleted     []NewFolder
}

func (f *fakeFolders) CreateFolder(_ context.Context, lib ScopeFilter, path, _ string) error {
	f.created = append(f.created, NewFolder{Library: lib, Path: path})
	return f.createErr
}

func (f *fakeFolders) DeleteFolder(_ context.Context, lib ScopeFilter, path string) error {
	f.deleted = append(f.deleted, NewFolder{Library: lib, Path: path})
	return f.deleteErr
}

func (f *fakeFolders) FolderExists(context.Context, ScopeFilter, string) (bool, error) {
	return f.exists, f.existsErr
}

func (f *fakeFolders) MoveFolderTree(_ context.Context, tree FolderRename, _ []Move) error {
	f.moved = append(f.moved, tree)
	return f.moveErr
}

func (f *fakeFolders) People(context.Context) ([]Person, error) { return f.people, f.peopleErr }

// folderRoutes is the handler over the fixture library with folders stored.
func folderRoutes(t *testing.T, claims *Claims, folders *fakeFolders) *Handler {
	t.Helper()
	store := newFolderStore()
	store.fileAt("r-1", "data", "top.csv", "sub-1")
	deps := Deps{Store: store, URIScheme: "mcp"}
	if folders != nil {
		deps.Folders = folders
		deps.People = folders
	}
	return NewHandler(deps, func(*http.Request) (*Claims, error) { return claims, nil }, nil)
}

func sendFolder(t *testing.T, h http.Handler, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method,
		"/api/v1/resources/folders", bytes.NewReader([]byte(body))))
	return rec
}

const mineBody = `{"scope":"user","scope_id":"sub-1","path":"data/new"}`

func TestFolderCreateRoute(t *testing.T) {
	owner := &Claims{Sub: "sub-1", Email: "me@example.com"}
	cases := map[string]struct {
		claims  *Claims
		folders *fakeFolders
		body    string
		want    int
	}{
		"created":               {owner, &fakeFolders{}, mineBody, http.StatusCreated},
		"already there":         {owner, &fakeFolders{createErr: ErrFolderExists}, mineBody, http.StatusConflict},
		"somebody else's":       {&Claims{Sub: "sub-2"}, &fakeFolders{}, mineBody, http.StatusForbidden},
		"bad path":              {owner, &fakeFolders{}, `{"scope":"user","scope_id":"sub-1","path":"/x/"}`, http.StatusBadRequest},
		"bad scope":             {owner, &fakeFolders{}, `{"scope":"moon","path":"x"}`, http.StatusBadRequest},
		"bad body":              {owner, &fakeFolders{}, `{`, http.StatusBadRequest},
		"store keeps no folder": {owner, nil, mineBody, http.StatusServiceUnavailable},
		"store fails":           {owner, &fakeFolders{createErr: errors.New("boom")}, mineBody, http.StatusInternalServerError},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := sendFolder(t, folderRoutes(t, tc.claims, tc.folders), http.MethodPost, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestFolderCreateRouteRecordsTheLibraryAndPath(t *testing.T) {
	folders := &fakeFolders{}
	rec := sendFolder(t, folderRoutes(t, &Claims{Sub: "sub-1"}, folders), http.MethodPost, mineBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	want := NewFolder{Library: ScopeFilter{Scope: ScopeUser, ScopeID: "sub-1"}, Path: "data/new"}
	if len(folders.created) != 1 || folders.created[0] != want {
		t.Fatalf("created = %+v", folders.created)
	}
	if !strings.Contains(rec.Body.String(), `"path":"data/new"`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestFolderDeleteRoute(t *testing.T) {
	owner := &Claims{Sub: "sub-1"}
	cases := map[string]struct {
		claims  *Claims
		folders *fakeFolders
		want    int
	}{
		"deleted":               {owner, &fakeFolders{}, http.StatusNoContent},
		"still holds files":     {owner, &fakeFolders{deleteErr: ErrFolderNotEmpty}, http.StatusConflict},
		"no such folder":        {owner, &fakeFolders{deleteErr: ErrFolderEmpty}, http.StatusNotFound},
		"somebody else's":       {&Claims{Sub: "sub-2"}, &fakeFolders{}, http.StatusForbidden},
		"store keeps no folder": {owner, nil, http.StatusServiceUnavailable},
		"store fails":           {owner, &fakeFolders{deleteErr: errors.New("boom")}, http.StatusInternalServerError},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := sendFolder(t, folderRoutes(t, tc.claims, tc.folders), http.MethodDelete, mineBody)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	rec := sendFolder(t, folderRoutes(t, owner, &fakeFolders{}), http.MethodDelete, `{`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad body: status = %d", rec.Code)
	}
	rec = sendFolder(t, folderRoutes(t, owner, &fakeFolders{}), http.MethodDelete, `{"scope":"user","scope_id":"sub-1","path":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty path: status = %d", rec.Code)
	}
}

func TestPeopleRoute(t *testing.T) {
	admin := &Claims{Sub: "admin", IsAdmin: true}
	listed := &fakeFolders{people: []Person{{ScopeID: "sub-9", Email: "p@example.com", Count: 3}}}
	cases := map[string]struct {
		claims  *Claims
		folders *fakeFolders
		want    int
	}{
		"administrator":  {admin, listed, http.StatusOK},
		"anyone else":    {&Claims{Sub: "sub-1"}, listed, http.StatusForbidden},
		"no lister":      {admin, nil, http.StatusServiceUnavailable},
		"the read fails": {admin, &fakeFolders{peopleErr: errors.New("boom")}, http.StatusInternalServerError},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			folderRoutes(t, tc.claims, tc.folders).ServeHTTP(rec,
				httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/resources/people", nil))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want != http.StatusOK {
				return
			}
			var got peopleResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.People) != 1 || got.People[0].Email != "p@example.com" || got.People[0].Count != 3 {
				t.Errorf("people = %+v", got.People)
			}
		})
	}
}

// TestFolderMoveOfAStoredEmptyFolder: a folder with no files moves when it is
// stored, through the store's tree move (#1872), and is still not found when
// it is not.
func TestFolderMoveOfAStoredEmptyFolder(t *testing.T) {
	owner := &Claims{Sub: "sub-1"}
	folders := &fakeFolders{exists: true}
	rec := postFolderMove(t, folderRoutes(t, owner, folders), folderMoveBody("empty", "kept"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(folders.moved) != 1 || folders.moved[0].From != "empty" || folders.moved[0].To != "kept" {
		t.Fatalf("tree moves = %+v", folders.moved)
	}

	rec = postFolderMove(t, folderRoutes(t, owner, &fakeFolders{}), folderMoveBody("empty", "kept"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("an unstored empty path: status = %d", rec.Code)
	}
	rec = postFolderMove(t, folderRoutes(t, owner, &fakeFolders{existsErr: errors.New("boom")}), folderMoveBody("empty", "kept"))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("a failed existence read: status = %d", rec.Code)
	}
}
