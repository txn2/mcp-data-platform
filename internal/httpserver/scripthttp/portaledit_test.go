package scripthttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The edit route is the portal mutation that changes what a script DOES
// (#1307). Every save applies to the live row through script.ApplyEdit — the
// one gate every mutation surface crosses — and the saved version is the
// version that runs.

// sourcePath is the route under test, on the global script carol owns.
const sourcePath = "/api/v1/portal/scripts/script_2/source"

// editedSource is the request the tests send: valid Starlark reaching for one
// connection and one export.
const editedSource = "res = platform.query(connection=\"warehouse\", sql=\"SELECT 2\")\nplatform.export(name=\"daily\", rows=res[\"rows\"])\n"

// editedBody is that source as a request body.
var editedBody = `{"source":` + strconv.Quote(editedSource) + `}`

// editStore records what ApplyEdit wrote to the live row and who it recorded
// as the author, which is what every assertion here is about.
type editStore struct {
	*stubStore
	updated   *script.Script
	updatedBy script.Author
	updateErr error
}

// UpdateWithVersion models the real store's contract: an edit that moved a
// versioned field snapshots a new version and ADVANCES sc.Version to it. A
// fake that left the number alone would make every save look like an edit that
// produced nothing.
func (e *editStore) UpdateWithVersion(
	_ context.Context, sc *script.Script, author script.Author,
) error {
	if e.updateErr != nil {
		return e.updateErr
	}
	for i := range e.scripts {
		if e.scripts[i].ID == sc.ID && script.SnapshotChanged(&e.scripts[i], sc) {
			sc.Version++
		}
	}
	e.updated, e.updatedBy = sc, author
	return nil
}

// newEditStore returns the portal fixture with an edit-aware store.
func newEditStore() *editStore {
	return &editStore{stubStore: portalStore()}
}

func editDeps(store *editStore, user *PortalIdentity) Deps {
	deps := portalDeps(store.stubStore, nil, nil, user)
	deps.Scripts, deps.Versions = store, store
	return deps
}

// TestPortalSetSource_AppliesToTheLiveRow is the load-bearing case: the owner
// saves, the live script carries the new source, and the save says it runs now.
func TestPortalSetSource_AppliesToTheLiveRow(t *testing.T) {
	store := newEditStore()
	rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, sourcePath,
		editedBody)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body sourceResponse
	decodeInto(t, rec, &body)
	assert.True(t, body.Applied)
	assert.Contains(t, body.Message, "this version is what runs now")

	require.NotNil(t, store.updated, "the live row was not written")
	assert.Equal(t, editedSource, store.updated.Source)
}

// TestPortalSetSource_RecordsTheAuthorAndTheirRoles pins the authority ceiling:
// a run of the saved version presents the roles its author held, so the version
// has to carry the roles of the person who actually wrote it.
func TestPortalSetSource_RecordsTheAuthorAndTheirRoles(t *testing.T) {
	store := newEditStore()
	author := &PortalIdentity{
		UserID: "u4", Email: "carol@example.com", Persona: "analyst",
		Roles: []string{"dp_analyst"},
	}
	rec := servePortalRequest(t, editDeps(store, author), http.MethodPut, sourcePath,
		editedBody)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "carol@example.com", store.updatedBy.Email)
	assert.Equal(t, []string{"dp_analyst"}, store.updatedBy.Roles)
}

// A caller the platform cannot name authors with no roles rather than
// inheriting somebody else's.
func TestPortalSetSource_AnAuthorWithNoRolesCarriesNone(t *testing.T) {
	store := newEditStore()
	author := &PortalIdentity{UserID: "u4", Email: "carol@example.com"}
	rec := servePortalRequest(t, editDeps(store, author), http.MethodPut, sourcePath,
		editedBody)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{}, store.updatedBy.Roles)
}

// TestPortalSetSource_DoesNotClaimARefusedScriptRuns keeps the save honest
// about what it means for execution: the run gate refuses a disabled or
// deprecated script whatever was just saved, and the save says so in the
// gate's own words.
func TestPortalSetSource_DoesNotClaimARefusedScriptRuns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*script.Script)
		want   string
	}{
		{"disabled", func(sc *script.Script) { sc.Enabled = false }, "disabled"},
		{"deprecated", func(sc *script.Script) { sc.Status = script.StatusDeprecated }, "deprecated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newEditStore()
			tc.mutate(&store.scripts[1])

			rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, sourcePath, editedBody)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var body sourceResponse
			decodeInto(t, rec, &body)
			assert.True(t, body.Applied, "the save itself still lands")
			assert.Contains(t, body.Message, "Nothing executes this script")
			assert.Contains(t, body.Message, tc.want)
			assert.NotContains(t, body.Message, "what runs now")
		})
	}
}

// TestPortalSetSource_RefusesSourceThatDoesNotParse pins that the static read
// happens before anything is stored, rather than at the next run with nobody
// watching.
func TestPortalSetSource_RefusesSourceThatDoesNotParse(t *testing.T) {
	store := newEditStore()
	rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, sourcePath,
		`{"source":"def broken(:\n"}`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "does not parse")
	assert.Nil(t, store.updated)
}

func TestPortalSetSource_RefusesAnEmptySource(t *testing.T) {
	store := newEditStore()
	rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, sourcePath, `{"source":""}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Nil(t, store.updated)
}

// The edit is the owner's, and a caller who may only SEE the script is refused
// exactly as one who may not see it at all.
func TestPortalSetSource_RefusedForANonOwner(t *testing.T) {
	store := newEditStore()
	notYours := servePortalRequest(t, editDeps(store, stranger), http.MethodPut, sourcePath,
		editedBody)
	require.Equal(t, http.StatusNotFound, notYours.Code)
	assert.Nil(t, store.updated)

	missing := servePortalRequest(t, editDeps(store, stranger), http.MethodPut,
		"/api/v1/portal/scripts/nope/source", editedBody)
	require.Equal(t, http.StatusNotFound, missing.Code)
	assert.Equal(t, missing.Body.String(), notYours.Body.String())
}

func TestPortalSetSource_AdminIsUnrestricted(t *testing.T) {
	store := newEditStore()
	rec := servePortalRequest(t, editDeps(store, admin), http.MethodPut, sourcePath,
		editedBody)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotNil(t, store.updated, "an administrator edits like any owner")
}

// TestPortalSetSource_AVersionConflictIsA409 pins the one edit failure a
// caller can act on: somebody else saved first, and re-reading is the fix.
func TestPortalSetSource_AVersionConflictIsA409(t *testing.T) {
	store := newEditStore()
	store.updateErr = fmt.Errorf("the script moved underneath the edit: %w", script.ErrVersionConflict)
	rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, sourcePath,
		editedBody)

	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
}

func TestPortalSetSource_Failures(t *testing.T) {
	t.Run("an unreadable body", func(t *testing.T) {
		rec := servePortalRequest(t, editDeps(newEditStore(), carol), http.MethodPut,
			sourcePath, "{not json")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("a body past the bound", func(t *testing.T) {
		body := `{"source":"` + strings.Repeat("x", maxSourceBodyBytes) + `"}`
		rec := servePortalRequest(t, editDeps(newEditStore(), carol), http.MethodPut,
			sourcePath, body)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("the live update could not be written", func(t *testing.T) {
		store := newEditStore()
		store.updateErr = errors.New("boom")
		rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, sourcePath,
			editedBody)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), "boom", "a store failure's detail stays in the log")
	})
}

func TestPortalSetSource_RequiresAuthentication(t *testing.T) {
	rec := servePortalRequest(t, editDeps(newEditStore(), nil), http.MethodPut, sourcePath,
		editedBody)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestPortalGetScript_CarriesTheLiveSourceForItsOwner pins where the editor
// gets what it opens, and that it is not served to a reader who may only see
// the script.
func TestPortalGetScript_CarriesTheLiveSourceForItsOwner(t *testing.T) {
	store := portalStore()
	store.scripts[1].Source = reportSource
	contracts := &stubContracts{contract: &script.Contract{
		ID: "script_2", Name: "shared-report", Scope: script.ScopeGlobal,
		OwnerEmail: "carol@example.com",
	}}

	rec := servePortal(t, portalDeps(store, nil, contracts, carol), "/api/v1/portal/scripts/script_2")
	require.Equal(t, http.StatusOK, rec.Code)
	var owned portalScriptResponse
	decodeInto(t, rec, &owned)
	assert.Equal(t, reportSource, owned.Source)

	rec = servePortal(t, portalDeps(store, nil, contracts, stranger), "/api/v1/portal/scripts/script_2")
	require.Equal(t, http.StatusOK, rec.Code)
	var seen portalScriptResponse
	decodeInto(t, rec, &seen)
	assert.Empty(t, seen.Source, "the code is the owner's, not everyone who may see the script")
}
