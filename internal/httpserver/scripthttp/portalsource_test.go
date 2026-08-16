package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The edit route is the second mutation on the portal surface (#1307), and the
// only one that changes what a script DOES. Everything here is about the gate
// it crosses to do that: an approved script's edit becomes a draft, an
// unapproved one applies, and neither is an approval.

// sourcePath is the route under test, on the global script carol owns.
const sourcePath = "/api/v1/portal/scripts/script_2/source"

// editedBody is the request the tests send: valid Starlark that reaches for
// the same capabilities the fixture's grant covers, as a JSON body.
const editedSource = "res = platform.query(connection=\"warehouse\", sql=\"SELECT 2\")\nplatform.export(name=\"daily\", rows=res[\"rows\"])\n"

// editedBody is that source as a request body.
var editedBody = `{"source":` + strconv.Quote(editedSource) + `}`

// editStore records what ApplyEdit did with the script: a draft version, or an
// update to the live row. The two outcomes are the whole point of the route, so
// a fake that collapsed them would prove nothing.
type editStore struct {
	*stubStore
	draftFor   *script.Script
	draftBy    script.Author
	updated    *script.Script
	updatedBy  script.Author
	draftErr   error
	updateErr  error
	draftAtNum int
}

func (e *editStore) CreateDraftVersion(
	_ context.Context, _ string, sc *script.Script, author script.Author,
) (int, error) {
	if e.draftErr != nil {
		return 0, e.draftErr
	}
	e.draftFor, e.draftBy = sc, author
	return e.draftAtNum, nil
}

func (e *editStore) UpdateWithVersion(_ context.Context, sc *script.Script, author script.Author) error {
	if e.updateErr != nil {
		return e.updateErr
	}
	e.updated, e.updatedBy = sc, author
	return nil
}

// newEditStore returns the portal fixture with an edit-aware store, with the
// global script carol owns made executable when approved is true.
func newEditStore(approved bool) *editStore {
	store := portalStore()
	if approved {
		store.scripts[1].ApprovedVersionID = "sver_1"
	}
	return &editStore{stubStore: store, draftAtNum: 4}
}

func editDeps(store *editStore, user *PortalIdentity) Deps {
	deps := portalDeps(store.stubStore, nil, nil, user)
	deps.Scripts, deps.Versions = store, store
	return deps
}

// TestPortalSetSource_OnAnApprovedScriptBecomesADraft is the load-bearing case:
// the owner edits, the approved version keeps running, and a reviewer decides.
func TestPortalSetSource_OnAnApprovedScriptBecomesADraft(t *testing.T) {
	store := newEditStore(true)
	rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, sourcePath,
		editedBody)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body sourceResponse
	decodeInto(t, rec, &body)
	assert.False(t, body.Applied, "the live script must not change under its approval")
	assert.Equal(t, 4, body.PendingVersion)
	assert.Contains(t, body.Message, "awaiting review")

	require.NotNil(t, store.draftFor, "the edit was not sent to review")
	assert.Equal(t, editedSource, store.draftFor.Source)
	assert.Nil(t, store.updated, "the live row must be untouched")
}

// TestPortalSetSource_RecordsTheAuthorAndTheirRoles pins the authority ceiling:
// approving a version binds the roles its author held, so the version has to
// carry the roles of the person who actually wrote it.
func TestPortalSetSource_RecordsTheAuthorAndTheirRoles(t *testing.T) {
	store := newEditStore(true)
	author := &PortalIdentity{
		UserID: "u4", Email: "carol@example.com", Persona: "analyst",
		Roles: []string{"dp_analyst"},
	}
	rec := servePortalRequest(t, editDeps(store, author), http.MethodPut, sourcePath,
		editedBody)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "carol@example.com", store.draftBy.Email)
	assert.Equal(t, []string{"dp_analyst"}, store.draftBy.Roles)
}

// A caller the platform cannot name authors with no roles rather than
// inheriting somebody else's.
func TestPortalSetSource_AnAuthorWithNoRolesCarriesNone(t *testing.T) {
	store := newEditStore(true)
	author := &PortalIdentity{UserID: "u4", Email: "carol@example.com"}
	rec := servePortalRequest(t, editDeps(store, author), http.MethodPut, sourcePath,
		editedBody)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{}, store.draftBy.Roles)
}

// TestPortalSetSource_OnAnUnapprovedScriptApplies pins the other half: nothing
// executes an unapproved script, so its edits are pure authoring.
func TestPortalSetSource_OnAnUnapprovedScriptApplies(t *testing.T) {
	store := newEditStore(false)
	rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, sourcePath,
		editedBody)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body sourceResponse
	decodeInto(t, rec, &body)
	assert.True(t, body.Applied)
	assert.Zero(t, body.PendingVersion)
	assert.Contains(t, body.Message, "nothing executes it unattended")

	require.NotNil(t, store.updated)
	assert.Equal(t, editedSource, store.updated.Source)
	assert.Nil(t, store.draftFor)
}

// TestPortalSetSource_RefusesSourceThatDoesNotParse pins that the static read
// happens before anything is stored, rather than at the next run with nobody
// watching.
func TestPortalSetSource_RefusesSourceThatDoesNotParse(t *testing.T) {
	store := newEditStore(true)
	rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, sourcePath,
		`{"source":"def broken(:\n"}`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "does not parse")
	assert.Nil(t, store.draftFor)
	assert.Nil(t, store.updated)
}

func TestPortalSetSource_RefusesAnEmptySource(t *testing.T) {
	store := newEditStore(true)
	rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, sourcePath, `{"source":""}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Nil(t, store.draftFor)
}

// The edit is the owner's, and a caller who may only SEE the script is refused
// exactly as one who may not see it at all.
func TestPortalSetSource_RefusedForANonOwner(t *testing.T) {
	store := newEditStore(true)
	notYours := servePortalRequest(t, editDeps(store, stranger), http.MethodPut, sourcePath,
		editedBody)
	require.Equal(t, http.StatusNotFound, notYours.Code)
	assert.Nil(t, store.draftFor)

	missing := servePortalRequest(t, editDeps(store, stranger), http.MethodPut,
		"/api/v1/portal/scripts/nope/source", editedBody)
	require.Equal(t, http.StatusNotFound, missing.Code)
	assert.Equal(t, missing.Body.String(), notYours.Body.String())
}

func TestPortalSetSource_AdminIsUnrestricted(t *testing.T) {
	store := newEditStore(true)
	rec := servePortalRequest(t, editDeps(store, admin), http.MethodPut, sourcePath,
		editedBody)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotNil(t, store.draftFor, "an administrator edits like any owner, and still cannot approve")
}

func TestPortalSetSource_Failures(t *testing.T) {
	t.Run("an unreadable body", func(t *testing.T) {
		rec := servePortalRequest(t, editDeps(newEditStore(true), carol), http.MethodPut,
			sourcePath, "{not json")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("a body past the bound", func(t *testing.T) {
		body := `{"source":"` + strings.Repeat("x", maxSourceBodyBytes) + `"}`
		rec := servePortalRequest(t, editDeps(newEditStore(true), carol), http.MethodPut,
			sourcePath, body)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("the draft could not be written", func(t *testing.T) {
		store := newEditStore(true)
		store.draftErr = errors.New("boom")
		rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, sourcePath,
			editedBody)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), "boom", "a store failure's detail stays in the log")
	})

	t.Run("the live update could not be written", func(t *testing.T) {
		store := newEditStore(false)
		store.updateErr = errors.New("boom")
		rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, sourcePath,
			editedBody)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestPortalSetSource_RequiresAuthentication(t *testing.T) {
	rec := servePortalRequest(t, editDeps(newEditStore(true), nil), http.MethodPut, sourcePath,
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
