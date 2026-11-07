package reachstore

import (
	"context"
	"fmt"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

func render(t *testing.T, s interface {
	ToSql() (string, []any, error)
},
) (query string, args []any) {
	t.Helper()
	q, args, err := s.ToSql()
	require.NoError(t, err)
	return q, args
}

func TestOwnerPredicateArms(t *testing.T) {
	q, args := render(t, OwnerPredicate(portaldomain.NewAssetOwner("u1", "a@example.com")))
	assert.Equal(t, "(owner_id = ? OR LOWER(owner_email) = LOWER(?))", q)
	assert.Equal(t, []any{"u1", "a@example.com"}, args)

	q, _ = render(t, OwnerPredicate(portaldomain.NewAssetOwner("u1", "")))
	assert.Equal(t, "owner_id = ?", q)
	q, _ = render(t, OwnerPredicate(portaldomain.NewAssetOwner("", "a@example.com")))
	assert.Equal(t, "LOWER(owner_email) = LOWER(?)", q)
	q, args = render(t, OwnerPredicate(portaldomain.AssetOwner{}))
	assert.Equal(t, "FALSE", q)
	assert.Empty(t, args)
}

// A blank identifier addresses nothing, so a reach naming only a persona
// binds only the persona, and one naming nobody matches nothing at all.
func TestAssetPredicateScopes(t *testing.T) {
	r := portaldomain.NewReach("u1", "a@example.com", "analyst", portaldomain.ScopeMine)
	mine, _ := render(t, AssetPredicate(r))
	assert.NotContains(t, mine, "portal_shares", "mine is the owned arm alone")

	r.Scope = portaldomain.ScopeShared
	shared, args := render(t, AssetPredicate(r))
	assert.True(t, strings.HasPrefix(shared, "(NOT ("), shared)
	assert.Contains(t, shared, "pc.deleted_at IS NULL", "a deleted collection grants nothing")
	assert.Contains(t, args, "analyst")

	r.Scope = portaldomain.ScopeAll
	all, _ := render(t, AssetPredicate(r))
	assert.Contains(t, all, " OR EXISTS")

	personaOnly, args := render(t, AssetPredicate(portaldomain.NewReach("", "", "analyst", portaldomain.ScopeAll)))
	assert.NotContains(t, personaOnly, "shared_with_user_id")
	assert.NotContains(t, personaOnly, "shared_with_email")
	assert.NotContains(t, args, "")

	nobody, _ := render(t, AssetPredicate(portaldomain.NewReach("", "", "", portaldomain.ScopeAll)))
	assert.Equal(t, "FALSE", nobody)
	nobody, _ = render(t, AssetPredicate(nil))
	assert.Equal(t, "FALSE", nobody)
}

func TestCollectionPredicate(t *testing.T) {
	mine, args := render(t, CollectionPredicate(portaldomain.NewReach("u1", "a@example.com", "", portaldomain.ScopeMine)))
	assert.Equal(t, "portal_collections.owner_id = ?", mine)
	assert.Equal(t, []any{"u1"}, args)

	shared, _ := render(t, CollectionPredicate(portaldomain.NewReach("", "a@example.com", "", portaldomain.ScopeShared)))
	assert.Contains(t, shared, "NOT (FALSE)", "an id-less caller owns no collection")
	assert.Contains(t, shared, "ps.collection_id = portal_collections.id")

	nobody, _ := render(t, CollectionPredicate(nil))
	assert.Equal(t, "FALSE", nobody)
}

func TestOwnersPredicateNormalizes(t *testing.T) {
	q, args := render(t, OwnersPredicate("owner_email", []string{" B@Example.com ", "c@example.com,b@example.com"}))
	assert.Equal(t, "LOWER(owner_email) = ANY(?)", q)
	require.Len(t, args, 1)
}

func TestFacet(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	assert.Contains(t, BuildTagFacet("SELECT tags FROM t"), "jsonb_array_elements_text(a.tags)")
	assert.Contains(t, BuildAuthorFacet("SELECT owner_email FROM t"), "a.owner_email <> ''")

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"v", "c"}).AddRow("finance", 2))
	got, err := Facet(context.Background(), db, "SELECT 1", nil)
	require.NoError(t, err)
	assert.Equal(t, []portaldomain.FacetCount{{Value: "finance", Count: 2}}, got)

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"v", "c"}))
	got, err = Facet(context.Background(), db, "SELECT 1", nil)
	require.NoError(t, err)
	assert.NotNil(t, got, "an empty facet is a list")

	mock.ExpectQuery("SELECT").WillReturnError(assert.AnError)
	_, err = Facet(context.Background(), db, "SELECT 1", nil)
	require.Error(t, err)

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"v", "c"}).AddRow("x", "not-a-number"))
	_, err = Facet(context.Background(), db, "SELECT 1", nil)
	require.Error(t, err)
}

func TestPermissions(t *testing.T) {
	r := portaldomain.NewReach("u1", "a@example.com", "analyst", portaldomain.ScopeAll)
	q, args, err := BuildPermissions([]string{"a1"}, r)
	require.NoError(t, err)
	assert.NotContains(t, q, "?", "every placeholder is numbered")
	assert.Contains(t, q, "UNION ALL")
	assert.Contains(t, q, "pc.deleted_at IS NULL")
	assert.Contains(t, q, fmt.Sprintf("$%d", len(args)), "the last argument is numbered")

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	mock.ExpectQuery("BOOL_OR").WillReturnRows(sqlmock.NewRows([]string{"id", "editor"}).AddRow("a1", true).AddRow("a2", false))
	got, err := Permissions(context.Background(), db, []string{"a1", "a2"}, r)
	require.NoError(t, err)
	assert.Equal(t, map[string]portaldomain.SharePermission{"a1": portaldomain.PermissionEditor, "a2": portaldomain.PermissionViewer}, got)

	mock.ExpectQuery("BOOL_OR").WillReturnError(assert.AnError)
	_, err = Permissions(context.Background(), db, []string{"a1"}, r)
	require.Error(t, err)

	mock.ExpectQuery("BOOL_OR").WillReturnRows(sqlmock.NewRows([]string{"id", "editor"}).AddRow("a1", "maybe"))
	_, err = Permissions(context.Background(), db, []string{"a1"}, r)
	require.Error(t, err)

	none, err := Permissions(context.Background(), db, nil, r)
	require.NoError(t, err)
	assert.Empty(t, none)
	none, err = Permissions(context.Background(), db, []string{"a1"}, nil)
	require.NoError(t, err)
	assert.Empty(t, none)
	require.NoError(t, mock.ExpectationsWereMet())
}
