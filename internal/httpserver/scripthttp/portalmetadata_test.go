package scripthttp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The documentation route is the portal's third mutation (#1369), and it is the
// one that changes nothing about what a script DOES. Everything here turns on
// that: it applies to the live row without touching the code, an omitted field
// is left alone rather than blanked, and only the owner may use it.

// metadataPath is the route under test, on the global script carol owns.
const metadataPath = "/api/v1/portal/scripts/script_2/metadata"

// metadataStore is the edit fixture with the script carrying source, which
// every stored script does: the route validates the whole record, so a fixture
// with no source would be refused for a reason this route is not about.
func metadataStore() *editStore {
	store := newEditStore()
	store.scripts[1].Source = reportSource
	return store
}

// TestPortalSetMetadata_AppliesToTheLiveRow is the route's central claim proved
// at the surface an author uses: documenting a script lands on the live row,
// and the response says the change is about what the script SAYS, not what it
// does.
func TestPortalSetMetadata_AppliesToTheLiveRow(t *testing.T) {
	store := metadataStore()

	rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, metadataPath,
		`{"display_name":"Shared Report","description":"## What it produces\n\nA CSV.","category":"reporting","tags":["sales","weekly"]}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body metadataResponse
	decodeInto(t, rec, &body)
	assert.Contains(t, body.Message, "what the script says about itself")
	assert.Empty(t, body.DescriptionNotice, "an ordinary description is not nagged")

	require.NotNil(t, store.updated, "the live row was not written")
	assert.Equal(t, "Shared Report", store.updated.DisplayName)
	assert.Equal(t, "## What it produces\n\nA CSV.", store.updated.Description)
	assert.Equal(t, "reporting", store.updated.Category)
	assert.Equal(t, []string{"sales", "weekly"}, store.updated.Tags)
	assert.Equal(t, reportSource, store.updated.Source,
		"the code the platform executes is untouched")
}

// TestPortalSetMetadata_IsCapturedAsAVersion proves the change is still
// history. What a script claimed to do is part of explaining what one of its
// runs did, so the four fields are in the snapshot even though none is gated.
func TestPortalSetMetadata_IsCapturedAsAVersion(t *testing.T) {
	store := metadataStore()

	rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, metadataPath,
		`{"category":"reporting"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body metadataResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, 1, body.Version, "the edit moved a versioned field, so it produced a version")
	assert.Equal(t, "carol@example.com", store.updatedBy.Email)
}

// TestPortalSetMetadata_AnOmittedFieldIsLeftAlone pins the pointer semantics:
// a client editing one field must not blank the other three by not mentioning
// them, and an explicitly empty value must clear.
func TestPortalSetMetadata_AnOmittedFieldIsLeftAlone(t *testing.T) {
	store := metadataStore()
	store.scripts[1].DisplayName = "Shared Report"
	store.scripts[1].Description = "The description nobody is editing."
	store.scripts[1].Tags = []string{"sales"}

	rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, metadataPath,
		`{"category":"reporting"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, store.updated)
	assert.Equal(t, "Shared Report", store.updated.DisplayName)
	assert.Equal(t, "The description nobody is editing.", store.updated.Description)
	assert.Equal(t, []string{"sales"}, store.updated.Tags)

	// And an explicitly empty value is a clearing, not an omission.
	rec = servePortalRequest(t, editDeps(store, carol), http.MethodPut, metadataPath,
		`{"description":"","tags":[]}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, store.updated.Description)
	assert.Empty(t, store.updated.Tags)
}

// TestPortalSetMetadata_RefusesWhatTheDomainRefuses proves the bounds are
// enforced here rather than reaching the store, and that the caller is told
// which field to fix.
func TestPortalSetMetadata_RefusesWhatTheDomainRefuses(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "a category that is not a slug",
			body: `{"category":"Sales Reports"}`,
			want: "category must be at most 31 characters",
		},
		{
			name: "a display name past the limit",
			body: `{"display_name":"` + strings.Repeat("a", script.MaxDisplayNameLen+1) + `"}`,
			want: "display_name must be at most 200 characters",
		},
		{
			name: "a description past the structural ceiling",
			body: `{"description":"` + strings.Repeat("x", script.MaxDescriptionBytes+1) + `"}`,
			want: "over the 65536-byte limit",
		},
		{
			name: "a body that is not JSON",
			body: `not json`,
			want: "invalid request body",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := metadataStore()

			rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, metadataPath, tt.body)

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tt.want)
			assert.Nil(t, store.updated, "nothing may be written on a refusal")
		})
	}
}

// TestPortalSetMetadata_CarriesTheLongDescriptionAdvisory proves the signal is
// advisory in the strongest sense available: the write SUCCEEDED and the notice
// travels with the success.
func TestPortalSetMetadata_CarriesTheLongDescriptionAdvisory(t *testing.T) {
	store := metadataStore()
	long := strings.Repeat("x", 20_000)

	rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, metadataPath,
		`{"description":"`+long+`"}`)

	require.Equal(t, http.StatusOK, rec.Code)
	var body metadataResponse
	decodeInto(t, rec, &body)
	assert.Contains(t, body.DescriptionNotice, "knowledge page")
	require.NotNil(t, store.updated, "the advisory must not have blocked the write")
	assert.Equal(t, long, store.updated.Description)
}

// TestPortalSetMetadata_IsTheOwnersAlone pins the same rule the source route
// applies: not-yours and does-not-exist are one answer, so the difference
// cannot be used to learn that a script exists.
func TestPortalSetMetadata_IsTheOwnersAlone(t *testing.T) {
	store := metadataStore()
	stranger := &PortalIdentity{UserID: "u9", Email: "mallory@example.com", Persona: "analyst"}

	rec := servePortalRequest(t, editDeps(store, stranger), http.MethodPut, metadataPath,
		`{"category":"reporting"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Nil(t, store.updated)
}

// TestPortalListScripts_NarrowsByCategoryAndTag proves the facet axes are
// applied as a query predicate rather than by the page: the listing is capped
// at the store's limit, so a filter the page applied to the rows it received
// would silently answer from a truncated set.
func TestPortalListScripts_NarrowsByCategoryAndTag(t *testing.T) {
	store := portalStore()
	deps := portalDeps(store, nil, nil, carol)

	rec := servePortal(t, deps, "/api/v1/portal/scripts?category=reporting&tag=sales&tag=weekly")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "reporting", store.lastFilter.Category)
	assert.Equal(t, []string{"sales", "weekly"}, store.lastFilter.Tags)

	// An unfiltered listing carries neither axis, so the plain request stays the
	// plain query it has always been.
	rec = servePortal(t, deps, "/api/v1/portal/scripts")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, store.lastFilter.Category)
	assert.Empty(t, store.lastFilter.Tags)

	// An empty value names no tag. Carried through, it would become the
	// predicate "tagged with the empty string", which nothing satisfies — so a
	// URL that reads as unfiltered would answer with nothing at all.
	rec = servePortal(t, deps, "/api/v1/portal/scripts?tag=&category=")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, store.lastFilter.Category)
	assert.Empty(t, store.lastFilter.Tags)
}

// TestPortalListScripts_FacetsNarrowAnAdministratorToo proves the two axes are
// not a visibility rule wearing a filter's clothes. An admin carries no
// visibility predicate — their reach is unrestricted by design — and still gets
// the narrowing they asked for.
func TestPortalListScripts_FacetsNarrowAnAdministratorToo(t *testing.T) {
	store := portalStore()
	admin := &PortalIdentity{UserID: "u1", Email: "admin@example.com", IsAdmin: true}

	rec := servePortal(t, portalDeps(store, nil, nil, admin), "/api/v1/portal/scripts?category=reporting")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "reporting", store.lastFilter.Category)
	assert.Empty(t, store.lastFilter.OwnerEmail, "an administrator carries no visibility predicate")
}
