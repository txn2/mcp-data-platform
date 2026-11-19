package assetsimilar

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

func reach() *portaldomain.Reach {
	return portaldomain.NewReach("u1", "ann@example.com", "analyst", portaldomain.ScopeAll)
}

func TestBuildNearest_AppliesReachThresholdAndExclusion(t *testing.T) {
	query, args, err := BuildNearest(NearestQuery{Vector: []float32{1, 0}, Reach: reach(), Exclude: []string{"new"}, Limit: 99})
	require.NoError(t, err)
	assert.Contains(t, query, "1 - (embedding <=> $1) AS similarity")
	assert.Contains(t, query, "deleted_at IS NULL")
	assert.Contains(t, query, "portal_shares", "the reach predicate decides what the caller can open")
	assert.Contains(t, query, "id NOT IN (")
	assert.Contains(t, query, "LIMIT 20", "the limit is clamped")
	assert.InDelta(t, 1-NearDuplicate, args[len(args)-2], 1e-9, "the threshold is bound, not formatted")
	assert.Equal(t, "new", args[len(args)-1])
}

func TestBuildNearest_DefaultLimit(t *testing.T) {
	query, _, err := BuildNearest(NearestQuery{Vector: []float32{1}, Reach: reach()})
	require.NoError(t, err)
	assert.Contains(t, query, "LIMIT 3")
	assert.NotContains(t, query, "NOT IN")
}

func TestNearest_ReturnsMatches(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	mock.ExpectQuery("SELECT id, name, owner_email").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "owner_email", "similarity"}).
			AddRow("a1", "Q3 revenue", "ann@example.com", 0.97))
	got, err := New(db).Nearest(context.Background(), NearestQuery{Vector: []float32{1}, Reach: reach(), Exclude: []string{"new"}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, Match{AssetID: "a1", Name: "Q3 revenue", OwnerEmail: "ann@example.com", Similarity: 0.97}, got[0])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNearest_AnswersNothingWithoutVectorReachOrDB(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	ctx := context.Background()

	got, err := New(db).Nearest(ctx, NearestQuery{Reach: reach()})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.NotNil(t, got, "an empty answer is [] not null")

	got, err = New(db).Nearest(ctx, NearestQuery{Vector: []float32{1}, Reach: portaldomain.NewReach("", "", "", portaldomain.ScopeAll)})
	require.NoError(t, err)
	assert.Empty(t, got)

	got, err = New(nil).Nearest(ctx, NearestQuery{Vector: []float32{1}, Reach: reach()})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestNearest_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	mock.ExpectQuery("SELECT").WillReturnError(errors.New("boom"))
	_, err = New(db).Nearest(context.Background(), NearestQuery{Vector: []float32{1}, Reach: reach()})
	require.Error(t, err)
}

func TestNearest_ScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "owner_email", "similarity"}).
		AddRow("a1", "n", "e", "not-a-number"))
	_, err = New(db).Nearest(context.Background(), NearestQuery{Vector: []float32{1}, Reach: reach()})
	require.Error(t, err)
}

func TestBuildPairs_OwnerScopedAndEveryOwner(t *testing.T) {
	owner := portaldomain.NewAssetOwner("u1", "ann@example.com")
	query, args, err := BuildPairs(GroupsQuery{Owner: &owner, MaxAssets: 10})
	require.NoError(t, err)
	assert.Contains(t, query, "a.owner_key = b.owner_key", "a group is one owner's")
	assert.Contains(t, query, "LIMIT 10")
	assert.Contains(t, query, "owner_id = $1")
	assert.InDelta(t, 1-NearDuplicate, args[len(args)-1], 1e-9)

	query, args, err = BuildPairs(GroupsQuery{})
	require.NoError(t, err)
	assert.NotContains(t, query, "owner_id = $")
	assert.Contains(t, query, "LIMIT 2000")
	assert.Contains(t, query, "<= $1", "the threshold is the only argument for every owner")
	assert.Len(t, args, 1)

	nobody := portaldomain.NewAssetOwner("", "")
	query, _, err = BuildPairs(GroupsQuery{Owner: &nobody})
	require.NoError(t, err)
	assert.Contains(t, query, "FALSE", "an owner naming nobody matches nothing")
}

func pairRow(a, b string, sim float64, at time.Time) []driver.Value {
	return []driver.Value{a, "name " + a, "u1", "ann@example.com", 1, at, b, "name " + b, "u1", "ann@example.com", 2, at.Add(time.Hour), sim}
}

func TestGroups_LinksChainsIntoOneGroup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	cols := []string{"a_id", "a_name", "a_oid", "a_oe", "a_v", "a_u", "b_id", "b_name", "b_oid", "b_oe", "b_v", "b_u", "sim"}
	rows := sqlmock.NewRows(cols).
		AddRow(pairRow("a", "b", 0.99, now)...).
		AddRow(pairRow("b", "c", 0.95, now)...).
		AddRow(pairRow("x", "y", 0.93, now)...)
	mock.ExpectQuery("WITH c AS MATERIALIZED").WillReturnRows(rows)
	owner := portaldomain.NewAssetOwner("u1", "")
	groups, err := New(db).Groups(context.Background(), GroupsQuery{Owner: &owner})
	require.NoError(t, err)
	require.Len(t, groups, 2)
	assert.InDelta(t, 0.99, groups[0].Similarity, 1e-9)
	ids := make([]string, 0, len(groups[0].Members))
	for _, m := range groups[0].Members {
		ids = append(ids, m.AssetID)
	}
	assert.ElementsMatch(t, []string{"a", "b", "c"}, ids, "a chain of close pairs is one group")
	assert.Len(t, groups[1].Members, 2)
	assert.Equal(t, "y", groups[1].Members[0].AssetID, "members newest first")
}

func TestGroups_ErrorsAndEmpty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	mock.ExpectQuery("WITH").WillReturnError(errors.New("boom"))
	_, err = New(db).Groups(context.Background(), GroupsQuery{})
	require.Error(t, err)

	mock.ExpectQuery("WITH").WillReturnRows(sqlmock.NewRows([]string{"a"}).AddRow("only-one-column"))
	_, err = New(db).Groups(context.Background(), GroupsQuery{})
	require.Error(t, err)

	got, err := New(nil).Groups(context.Background(), GroupsQuery{})
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestNearDuplicateSitsAboveTheSearchFloor(t *testing.T) {
	// The search floor marks where the model stops telling related from
	// unrelated; a duplicate must be far closer than merely related.
	assert.Greater(t, NearDuplicate, 0.9)
	assert.Less(t, NearDuplicate, 1.0)
}
