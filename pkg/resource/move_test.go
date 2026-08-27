package resource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// --- MovedURI ---

func TestMovedURIKeepsThePathAndSwapsTheLibrary(t *testing.T) {
	// The stored URI says "samples" while the row's category says "data": a
	// category edit has never rewritten the URI, so this drift already exists
	// in deployed data. A move must not silently repair it, because the person
	// moving the file did not ask for its address to change beyond the library.
	r := &Resource{URI: "mcp://user/sub-1/samples/x.csv", Category: "data", Filename: "x.csv"}

	tests := []struct {
		name    string
		scope   Scope
		scopeID string
		want    string
	}{
		{"to global", ScopeGlobal, "", "mcp://global/samples/x.csv"},
		{"to a persona", ScopePersona, "ops", "mcp://persona/ops/samples/x.csv"},
		{"to another person", ScopeUser, "her@example.com", "mcp://user/her@example.com/samples/x.csv"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MovedURI("mcp", r, tc.scope, tc.scopeID); got != tc.want {
				t.Errorf("MovedURI = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMovedURIFallsBackWhenTheStoredURIWillNotParse(t *testing.T) {
	// A row whose URI predates the scheme, or was written by hand, still has to
	// move: composing the address it would have been minted with today beats
	// refusing over something the mover never chose.
	r := &Resource{URI: "not-a-resource-uri", Category: "data", Filename: "x.csv"}
	if got := MovedURI("mcp", r, ScopeGlobal, ""); got != "mcp://global/data/x.csv" {
		t.Errorf("MovedURI = %q, want the composed address", got)
	}
}

func TestURIInLibraryOfAnUnknownScope(t *testing.T) {
	// Nothing reaches this with an unknown scope -- ValidateScope refuses one at
	// every door -- so the arm exists to produce a URI that is visibly not a
	// library rather than one that reads as a real address.
	if got := URIInLibrary("mcp", Scope("elsewhere"), "x", "data/x.csv"); got != "mcp://unknown/data/x.csv" {
		t.Errorf("URIInLibrary = %q", got)
	}
}

func TestMovedURIUsesTheConfiguredScheme(t *testing.T) {
	r := &Resource{URI: "acme://user/sub-1/data/x.csv", Category: "data", Filename: "x.csv"}
	if got := MovedURI("acme", r, ScopeGlobal, ""); got != "acme://global/data/x.csv" {
		t.Errorf("MovedURI = %q", got)
	}
}

// --- CanMoveToLibrary ---

func TestCanMoveToLibrary(t *testing.T) {
	member := Claims{Sub: "sub-1", Email: "me@example.com", Personas: []string{"ops"}}
	personaAdmin := Claims{Sub: "sub-2", AdminOfPersonas: []string{"finance"}}
	admin := Claims{Sub: "sub-3", IsAdmin: true}

	tests := []struct {
		name    string
		claims  Claims
		scope   Scope
		scopeID string
		want    bool
	}{
		// The one arm that is looser than CanWriteScope: belonging is enough to
		// receive a file you already own, while uploading still takes the role.
		{"member into their own persona", member, ScopePersona, "ops", true},
		{"member into a persona they do not belong to", member, ScopePersona, "finance", false},
		{"member into their own library", member, ScopeUser, "sub-1", true},
		{"member into their own library by address", member, ScopeUser, "me@example.com", true},
		{"member into somebody else's library", member, ScopeUser, "her@example.com", false},
		{"member into global", member, ScopeGlobal, "", false},
		{"persona admin into the persona they administer", personaAdmin, ScopePersona, "finance", true},
		{"persona admin into another persona", personaAdmin, ScopePersona, "ops", false},
		{"platform admin into global", admin, ScopeGlobal, "", true},
		{"platform admin into any persona", admin, ScopePersona, "anything", true},
		{"platform admin into a named person's library", admin, ScopeUser, "her@example.com", true},
		{"an empty persona id names no persona", member, ScopePersona, "", false},
		{"an unknown scope is nobody's", member, Scope("elsewhere"), "ops", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanMoveToLibrary(tc.claims, tc.scope, tc.scopeID); got != tc.want {
				t.Errorf("CanMoveToLibrary = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCanMoveToLibraryDoesNotWidenTheUploadRule is the guard on the whole
// design: widening CanWriteScope would have been the smaller change and would
// have opened an upload door on every persona a caller merely belongs to.
func TestCanMoveToLibraryDoesNotWidenTheUploadRule(t *testing.T) {
	member := Claims{Sub: "sub-1", Personas: []string{"ops"}}
	if CanWriteScope(member, ScopePersona, "ops") {
		t.Fatal("CanWriteScope now grants a persona member: the upload rule was widened")
	}
	if !CanMoveToLibrary(member, ScopePersona, "ops") {
		t.Fatal("CanMoveToLibrary refuses a persona member")
	}
}

// --- Update.Fields ---

func TestUpdateFields(t *testing.T) {
	s, empty := "x", ""
	scope := ScopePersona
	tests := []struct {
		name string
		u    Update
		want bool
	}{
		{"nothing", Update{}, false},
		{"display name", Update{DisplayName: &s}, true},
		{"description", Update{Description: &s}, true},
		{"category", Update{Category: &s}, true},
		{"tags, even empty", Update{Tags: []string{}}, true},
		{"a move alone is not a metadata edit", Update{Scope: &scope, ScopeID: &empty}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.u.Fields(); got != tc.want {
				t.Errorf("Fields() = %v, want %v", got, tc.want)
			}
		})
	}
}

// --- MoveResource ---

// moveStore is a store that records the move it was handed and answers the
// collision read from a fixed map, so MoveResource's own decisions are what a
// test observes.
type moveStore struct {
	*mockStore
	byURI    map[string]*Resource
	moved    *Move
	movedID  string
	moveErr  error
	uriErr   error
	uriCalls int
}

func newMoveStore() *moveStore {
	return &moveStore{mockStore: newMockStore(), byURI: map[string]*Resource{}}
}

func (m *moveStore) GetByURI(_ context.Context, uri string) (*Resource, error) {
	m.uriCalls++
	if m.uriErr != nil {
		return nil, m.uriErr
	}
	if r, ok := m.byURI[uri]; ok {
		return r, nil
	}
	return nil, fmt.Errorf("not found: %w", sql.ErrNoRows)
}

func (m *moveStore) Move(_ context.Context, id string, mv Move) error {
	if m.moveErr != nil {
		return m.moveErr
	}
	m.movedID, m.moved = id, &mv
	return nil
}

// recordingMoves captures the audit events a move produces.
type recordingMoves struct{ events []MoveEvent }

func (r *recordingMoves) RecordMove(_ context.Context, ev MoveEvent) {
	r.events = append(r.events, ev)
}

func ownedResource() *Resource {
	return &Resource{
		ID: "res-1", Scope: ScopeUser, ScopeID: "sub-1",
		Category: "templates", Filename: "report.docx", DisplayName: "Report",
		URI:         "mcp://user/sub-1/templates/report.docx",
		S3Key:       "resources/user/sub-1/res-1/report.docx",
		UploaderSub: "sub-1", UploaderEmail: "me@example.com",
	}
}

func TestMoveResourceRefilesAndRecordsWhatItLeft(t *testing.T) {
	store := newMoveStore()
	moves := &recordingMoves{}
	deps := Deps{Store: store, MoveRecorder: moves}
	claims := &Claims{Sub: "sub-1", Email: "me@example.com", Personas: []string{"ops"}}
	res := ownedResource()

	uri, err := MoveResource(context.Background(), deps, claims, res, ScopeFilter{Scope: ScopePersona, ScopeID: "ops"})
	if err != nil {
		t.Fatalf("MoveResource: %v", err)
	}
	if uri != "mcp://persona/ops/templates/report.docx" {
		t.Errorf("new URI = %q", uri)
	}
	if store.moved == nil {
		t.Fatal("no move reached the store")
	}
	// The address being left is what the store records as an alias; passing it
	// from here rather than re-reading is what makes the alias the address the
	// permission and collision checks were run against.
	if store.moved.FromURI != "mcp://user/sub-1/templates/report.docx" {
		t.Errorf("FromURI = %q", store.moved.FromURI)
	}
	if store.moved.Scope != ScopePersona || store.moved.ScopeID != "ops" {
		t.Errorf("move target = %s/%s", store.moved.Scope, store.moved.ScopeID)
	}
	if len(moves.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(moves.events))
	}
	ev := moves.events[0]
	if ev.FromScope != ScopeUser || ev.FromScopeID != "sub-1" ||
		ev.ToScope != ScopePersona || ev.ToScopeID != "ops" {
		t.Errorf("audit event does not name both libraries: %+v", ev)
	}
	if ev.FromURI == ev.ToURI || ev.ResourceID != "res-1" || ev.UserEmail != "me@example.com" {
		t.Errorf("audit event = %+v", ev)
	}
}

func TestMoveResourceToWhereItAlreadyIsChangesNothing(t *testing.T) {
	store := newMoveStore()
	moves := &recordingMoves{}
	res := ownedResource()
	claims := &Claims{Sub: "sub-1"}

	uri, err := MoveResource(context.Background(), Deps{Store: store, MoveRecorder: moves},
		claims, res, ScopeFilter{Scope: ScopeUser, ScopeID: "sub-1"})
	if err != nil {
		t.Fatalf("MoveResource: %v", err)
	}
	// An idempotent PATCH must not fail, and it must not write: a no-op move
	// recorded in the audit trail reads as somebody having refiled the file.
	if uri != "" || store.moved != nil || len(moves.events) != 0 {
		t.Errorf("a move to the current library wrote something: uri=%q moved=%v events=%d",
			uri, store.moved, len(moves.events))
	}
}

func TestMoveResourceRefusesALibraryTheCallerCannotWrite(t *testing.T) {
	store := newMoveStore()
	res := ownedResource()
	claims := &Claims{Sub: "sub-1", Personas: []string{"ops"}}

	_, err := MoveResource(context.Background(), Deps{Store: store}, claims, res, ScopeFilter{Scope: ScopeGlobal, ScopeID: ""})
	if !errors.Is(err, ErrMoveForbidden) {
		t.Fatalf("err = %v, want ErrMoveForbidden", err)
	}
	if store.moved != nil {
		t.Error("a refused move still wrote")
	}
}

func TestMoveResourceRefusesAnUnusableTarget(t *testing.T) {
	store := newMoveStore()
	claims := &Claims{Sub: "sub-1", IsAdmin: true}

	_, err := MoveResource(context.Background(), Deps{Store: store}, claims, ownedResource(), ScopeFilter{Scope: ScopePersona, ScopeID: ""})
	if !IsInvalidScope(err) {
		t.Fatalf("err = %v, want an invalid-scope error", err)
	}
	if store.uriCalls != 0 {
		t.Error("an unusable target was still checked for a collision")
	}
}

func TestMoveResourceNamesTheResourceItCollidesWith(t *testing.T) {
	store := newMoveStore()
	store.byURI["mcp://persona/ops/templates/report.docx"] = &Resource{
		ID: "res-other", DisplayName: "Quarterly Report",
		URI: "mcp://persona/ops/templates/report.docx",
	}
	claims := &Claims{Sub: "sub-1", Personas: []string{"ops"}}

	_, err := MoveResource(context.Background(), Deps{Store: store}, claims,
		ownedResource(), ScopeFilter{Scope: ScopePersona, ScopeID: "ops"})
	if !IsMoveConflict(err) {
		t.Fatalf("err = %v, want a conflict", err)
	}
	// The mover cannot see the other library, so "that address is taken" alone
	// is an error they can do nothing about.
	if !strings.Contains(err.Error(), "Quarterly Report") {
		t.Errorf("conflict does not name the occupant: %v", err)
	}
	if store.moved != nil {
		t.Error("a refused move still wrote")
	}
}

// TestMoveResourceReclaimsAnAddressOnlyAnAliasHolds is what separates a live
// collision from a previous occupant: GetByURI resolves both, and a hit whose
// own URI is not the address asked about is an alias the move is allowed to
// take over.
func TestMoveResourceReclaimsAnAddressOnlyAnAliasHolds(t *testing.T) {
	store := newMoveStore()
	store.byURI["mcp://persona/ops/templates/report.docx"] = &Resource{
		ID: "res-other", DisplayName: "Moved Away",
		URI: "mcp://global/templates/report.docx", // it lives elsewhere now
	}
	claims := &Claims{Sub: "sub-1", Personas: []string{"ops"}}

	uri, err := MoveResource(context.Background(), Deps{Store: store}, claims,
		ownedResource(), ScopeFilter{Scope: ScopePersona, ScopeID: "ops"})
	if err != nil {
		t.Fatalf("MoveResource: %v", err)
	}
	if uri != "mcp://persona/ops/templates/report.docx" {
		t.Errorf("new URI = %q", uri)
	}
}

func TestMoveResourceSurfacesAFailedCollisionRead(t *testing.T) {
	store := newMoveStore()
	store.uriErr = errors.New("connection refused")
	claims := &Claims{Sub: "sub-1", Personas: []string{"ops"}}

	// A read that failed is not a free address: moving anyway would hit the
	// UNIQUE constraint and report a database error where the answer was "taken".
	_, err := MoveResource(context.Background(), Deps{Store: store}, claims,
		ownedResource(), ScopeFilter{Scope: ScopePersona, ScopeID: "ops"})
	if err == nil || store.moved != nil {
		t.Fatalf("err = %v, moved = %v", err, store.moved)
	}
}

func TestMoveResourceSurfacesTheStoresConflict(t *testing.T) {
	store := newMoveStore()
	store.moveErr = ErrURIConflict
	claims := &Claims{Sub: "sub-1", Personas: []string{"ops"}}

	// The pre-check and the write are not atomic, so the constraint is the last
	// word and has to reach the caller as a conflict rather than as a 500.
	_, err := MoveResource(context.Background(), Deps{Store: store}, claims,
		ownedResource(), ScopeFilter{Scope: ScopePersona, ScopeID: "ops"})
	if !IsMoveConflict(err) {
		t.Fatalf("err = %v, want a conflict", err)
	}
}

func TestMoveResourceWithoutARecorderStillMoves(t *testing.T) {
	store := newMoveStore()
	claims := &Claims{Sub: "sub-1", Personas: []string{"ops"}}

	if _, err := MoveResource(context.Background(), Deps{Store: store}, claims,
		ownedResource(), ScopeFilter{Scope: ScopePersona, ScopeID: "ops"}); err != nil {
		t.Fatalf("MoveResource: %v", err)
	}
	if store.moved == nil {
		t.Error("no move reached the store")
	}
}

func TestIsMoveConflictIgnoresOtherErrors(t *testing.T) {
	if IsMoveConflict(errors.New("connection refused")) {
		t.Error("an unrelated error read as a conflict")
	}
	if IsMoveConflict(nil) {
		t.Error("nil read as a conflict")
	}
}
