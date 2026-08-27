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

// movedNotifier records the MCP registry callbacks a PATCH fires, so a test can
// assert that a moved resource is withdrawn from the address it left.
type movedNotifier struct {
	created []string
	deleted []string
}

// moveFixture is the update route wired over one resource owned by sub-1 and
// filed in that person's own library, with everything a move writes to.
type moveFixture struct {
	h     *Handler
	store *mockStore
	moves *recordingMoves
	notes *movedNotifier
}

func moveHandler(t *testing.T, claims *Claims) moveFixture {
	t.Helper()
	f := moveFixture{store: newMockStore(), moves: &recordingMoves{}, notes: &movedNotifier{}}
	f.store.resources[testResourceID] = ownedResource()
	deps := Deps{
		Store: f.store, S3Bucket: "test-bucket", URIScheme: "mcp", MoveRecorder: f.moves,
		OnCreate: func(r *Resource) { f.notes.created = append(f.notes.created, r.URI) },
		OnDelete: func(uri string) { f.notes.deleted = append(f.notes.deleted, uri) },
	}
	f.h = NewHandler(deps, func(*http.Request) (*Claims, error) { return claims, nil }, nil)
	return f
}

// testResourceID is the resource every move test acts on.
const testResourceID = "res-1"

func patchResource(t *testing.T, h *Handler, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch,
		"/api/v1/resources/"+testResourceID, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// memberOfOps is the acceptance case: somebody who owns a file and belongs to a
// persona, with no admin authority anywhere.
func memberOfOps() *Claims {
	return &Claims{Sub: "sub-1", Email: "me@example.com", Personas: []string{"ops"}, Roles: []string{"analyst"}}
}

func TestPatchMovesAResourceIntoAPersonaTheOwnerBelongsTo(t *testing.T) {
	f := moveHandler(t, memberOfOps())

	rec := patchResource(t, f.h, map[string]any{"scope": "persona", "scope_id": "ops"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	res := f.store.resources[testResourceID]
	if res.Scope != ScopePersona || res.ScopeID != "ops" {
		t.Fatalf("resource is in %s/%s", res.Scope, res.ScopeID)
	}
	if res.URI != "mcp://persona/ops/templates/report.docx" {
		t.Errorf("URI = %q", res.URI)
	}
	// The blob is not copied: the key still names the library the file was
	// uploaded into, and nothing recomputes it on read.
	if res.S3Key != "resources/user/sub-1/res-1/report.docx" {
		t.Errorf("S3 key changed on a move: %q", res.S3Key)
	}
	if len(f.moves.events) != 1 {
		t.Errorf("audit events = %d, want 1", len(f.moves.events))
	}
	// The MCP registry is keyed on the URI, so the address the resource left has
	// to be withdrawn or clients keep listing a resource that is no longer there.
	if len(f.notes.deleted) != 1 || f.notes.deleted[0] != "mcp://user/sub-1/templates/report.docx" {
		t.Errorf("withdrawn URIs = %v", f.notes.deleted)
	}
	if len(f.notes.created) != 1 || f.notes.created[0] != res.URI {
		t.Errorf("registered URIs = %v", f.notes.created)
	}
}

func TestPatchMoveKeepsTheOldAddressResolving(t *testing.T) {
	f := moveHandler(t, memberOfOps())
	old := f.store.resources[testResourceID].URI

	if rec := patchResource(t, f.h, map[string]any{
		"scope": "persona", "scope_id": "ops",
	}); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	// A knowledge page or a script body that hard-codes the old URI as text
	// resolves it through GetByURI; the alias is what keeps that working.
	res, err := f.store.GetByURI(context.Background(), old)
	if err != nil {
		t.Fatalf("the vacated address no longer resolves: %v", err)
	}
	if res.ID != "res-1" {
		t.Errorf("the vacated address resolves to %q", res.ID)
	}
}

func TestPatchRefusesALibraryTheCallerCannotWrite(t *testing.T) {
	f := moveHandler(t, memberOfOps())

	rec := patchResource(t, f.h, map[string]any{"scope": "global"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if f.store.resources[testResourceID].Scope != ScopeUser || len(f.moves.events) != 0 {
		t.Error("a refused move still changed the resource")
	}
}

func TestPatchRefusesAMoveByANonOwner(t *testing.T) {
	// A stranger who can neither modify the resource nor even see it gets the
	// route's existing answer, and the move is never reached.
	stranger := &Claims{Sub: "sub-9", Email: "them@example.com", Roles: []string{"analyst"}}
	f := moveHandler(t, stranger)

	rec := patchResource(t, f.h, map[string]any{"scope": "persona", "scope_id": "ops"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if f.store.resources[testResourceID].Scope != ScopeUser {
		t.Error("a refused move still changed the resource")
	}
}

func TestPatchMoveOntoATakenAddressIsRefusedAndChangesNothing(t *testing.T) {
	f := moveHandler(t, memberOfOps())
	f.store.resources["res-2"] = &Resource{
		ID: "res-2", Scope: ScopePersona, ScopeID: "ops", DisplayName: "Quarterly Report",
		Category: "templates", Filename: "report.docx",
		URI: "mcp://persona/ops/templates/report.docx",
	}

	rec := patchResource(t, f.h, map[string]any{
		"scope": "persona", "scope_id": "ops", "display_name": "Renamed",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Quarterly Report") {
		t.Errorf("the conflict does not name the occupant: %s", rec.Body.String())
	}
	// The move runs before the metadata edit precisely so a refused move cannot
	// leave half the request committed.
	res := f.store.resources[testResourceID]
	if res.Scope != ScopeUser || res.DisplayName != "Report" {
		t.Errorf("a refused request still wrote: %+v", res)
	}
}

func TestPatchRefusesAnUnusableTarget(t *testing.T) {
	f := moveHandler(t, memberOfOps())

	rec := patchResource(t, f.h, map[string]any{"scope": "persona"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestPatchWithoutAScopeLeavesTheLibraryAlone(t *testing.T) {
	f := moveHandler(t, memberOfOps())

	rec := patchResource(t, f.h, map[string]any{"display_name": "Renamed"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	res := f.store.resources[testResourceID]
	if res.DisplayName != "Renamed" || res.Scope != ScopeUser {
		t.Errorf("resource = %+v", res)
	}
	if len(f.moves.events) != 0 {
		t.Error("a metadata edit produced a move event")
	}
	// Nothing was vacated, so nothing is withdrawn from the MCP registry.
	if len(f.notes.deleted) != 0 {
		t.Errorf("withdrawn URIs = %v", f.notes.deleted)
	}
}

func TestPatchMoveToTheCurrentLibraryIsANoOp(t *testing.T) {
	f := moveHandler(t, memberOfOps())

	rec := patchResource(t, f.h, map[string]any{"scope": "user", "scope_id": "sub-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if f.store.resources[testResourceID].URI != "mcp://user/sub-1/templates/report.docx" {
		t.Error("a no-op move rewrote the URI")
	}
	if len(f.moves.events) != 0 || len(f.notes.deleted) != 0 {
		t.Errorf("a no-op move was recorded: events=%d withdrawn=%v", len(f.moves.events), f.notes.deleted)
	}
}

func TestPatchCarriesAMoveAndAMetadataEditTogether(t *testing.T) {
	f := moveHandler(t, memberOfOps())

	rec := patchResource(t, f.h, map[string]any{
		"scope": "persona", "scope_id": "ops", "display_name": "Ops Report",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	res := f.store.resources[testResourceID]
	if res.ScopeID != "ops" || res.DisplayName != "Ops Report" {
		t.Errorf("resource = %+v", res)
	}
}

// TestPatchMoveByAnAdministratorReachesEveryLibrary is the administrator half of
// the acceptance: any persona, the global library, and a named person's.
func TestPatchMoveByAnAdministratorReachesEveryLibrary(t *testing.T) {
	tests := []struct {
		name    string
		scope   string
		scopeID string
		wantURI string
	}{
		{"global", "global", "", "mcp://global/templates/report.docx"},
		{"a persona they do not belong to", "persona", "finance", "mcp://persona/finance/templates/report.docx"},
		{"a named person's library", "user", "her@example.com", "mcp://user/her@example.com/templates/report.docx"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := moveHandler(t, &Claims{Sub: "admin-1", Email: "admin@example.com", IsAdmin: true})
			body := map[string]any{"scope": tc.scope}
			if tc.scopeID != "" {
				body["scope_id"] = tc.scopeID
			}
			if rec := patchResource(t, f.h, body); rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if got := f.store.resources[testResourceID].URI; got != tc.wantURI {
				t.Errorf("URI = %q, want %q", got, tc.wantURI)
			}
		})
	}
}

func TestPatchMoveFailureIsReportedWithoutInternals(t *testing.T) {
	store := newMockStore()
	store.resources[testResourceID] = ownedResource()
	deps := Deps{Store: &brokenMoveStore{mockStore: store}, URIScheme: "mcp"}
	h := NewHandler(deps, func(*http.Request) (*Claims, error) { return memberOfOps(), nil }, nil)

	rec := patchResource(t, h, map[string]any{"scope": "persona", "scope_id": "ops"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "postgres") {
		t.Errorf("the response leaked the cause: %s", rec.Body.String())
	}
}

// brokenMoveStore fails only the move, so the handler's failure branch is
// reached with every other read intact.
type brokenMoveStore struct{ *mockStore }

func (*brokenMoveStore) Move(context.Context, string, Move) error {
	// A colon in the message is the point: writeError truncates a 5xx body at
	// the first one so an internal chain cannot leak.
	return errors.New("moving resource: postgres is unreachable")
}
