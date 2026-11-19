package library

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/assetsimilar"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

var errBoom = errors.New("boom")

func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return New(db, nil), mock
}

func reach() *portaldomain.Reach {
	return portaldomain.NewReach("u1", "a@example.com", "analyst", portaldomain.ScopeMine)
}

func TestParseKindAndLimits(t *testing.T) {
	k, ok := ParseKind("asset")
	assert.True(t, ok)
	assert.Equal(t, KindAsset, k)
	k, ok = ParseKind("collection")
	assert.True(t, ok)
	assert.Equal(t, KindCollection, k)
	_, ok = ParseKind("prompt")
	assert.False(t, ok)

	assert.Equal(t, DefaultLimit, ClampLimit(0))
	assert.Equal(t, MaxLimit, ClampLimit(MaxLimit+1))
	assert.Equal(t, 7, ClampLimit(7))

	assert.Equal(t, "u1", UserKey(" u1 ", "A@Example.com"))
	assert.Equal(t, "a@example.com", UserKey("", " A@Example.com "))
}

func TestRecordOpen(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("INSERT INTO portal_opens").WithArgs("u1", "asset", "a1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.RecordOpen(context.Background(), "u1", KindAsset, "a1"))

	mock.ExpectExec("INSERT INTO portal_opens").WillReturnError(errBoom)
	require.ErrorIs(t, s.RecordOpen(context.Background(), "u1", KindAsset, "a1"), errBoom)
	require.ErrorIs(t, s.RecordOpen(context.Background(), "", KindAsset, "a1"), ErrNoUser)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A second open within the recount window does not add to the count, so a
// page that reports its open twice while loading counts once.
func TestRecordOpenRecountsAfterAWindow(t *testing.T) {
	assert.Contains(t, recordOpenQuery, "INTERVAL '30 seconds' THEN 1 ELSE 0")
}

func TestStarAndUnstar(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("INSERT INTO portal_favorites").WithArgs("u1", "collection", "c1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM portal_favorites").WithArgs("u1", "asset", "a1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Star(context.Background(), "u1", KindCollection, "c1"))
	require.NoError(t, s.Unstar(context.Background(), "u1", KindAsset, "a1"))
	require.ErrorIs(t, s.Star(context.Background(), "", KindAsset, "a1"), ErrNoUser)
	mock.ExpectExec("DELETE FROM portal_favorites").WillReturnError(errBoom)
	require.ErrorIs(t, s.Unstar(context.Background(), "u1", KindAsset, "a1"), errBoom)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The entries are read under the reach in every scope the request named: a
// recents read is never narrowed to the caller's own items.
func TestBuildItemsJoinsUnderTheReach(t *testing.T) {
	all := portaldomain.NewReach("u1", "a@example.com", "analyst", portaldomain.ScopeAll)
	query, args, err := buildItems("portal_opens", "last_opened_at", "u1", all, 5)
	require.NoError(t, err)
	assert.Contains(t, query, "UNION ALL")
	assert.Contains(t, query, "JOIN portal_assets ON portal_assets.id = e.target_id")
	assert.Contains(t, query, "JOIN portal_collections ON portal_collections.id = e.target_id")
	assert.Contains(t, query, "portal_shares ps")
	assert.Contains(t, query, "ORDER BY at DESC LIMIT $")
	assert.NotContains(t, query, "?")
	assert.Equal(t, 5, args[len(args)-1])
}

func TestRecentsAndFavorites(t *testing.T) {
	s, mock := newMock(t)
	at := time.Date(2026, 11, 19, 1, 0, 0, 0, time.UTC)
	cols := []string{"kind", "id", "name", "content_type", "owner_email", "at"}
	// The request's reach names the caller's own scope; recents widen it to
	// everything the caller can open, shares included.
	mock.ExpectQuery(`FROM portal_opens e .*portal_shares ps`).
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow("asset", "a1", "Report", "text/html", "o@example.com", at).
			AddRow("collection", "c1", "Finance", "", "o@example.com", at.Add(-time.Hour)))
	items, err := s.Recents(context.Background(), "u1", reach(), 0)
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, Item{Kind: KindAsset, ID: "a1", Name: "Report", ContentType: "text/html", OwnerEmail: "o@example.com", At: at}, items[0])
	assert.Equal(t, KindCollection, items[1].Kind)

	mock.ExpectQuery(regexp.QuoteMeta("FROM portal_favorites e")).WillReturnRows(sqlmock.NewRows(cols))
	items, err = s.Favorites(context.Background(), "u1", reach(), 3)
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.NotNil(t, items)

	mock.ExpectQuery(regexp.QuoteMeta("FROM portal_favorites e")).WillReturnError(errBoom)
	_, err = s.Favorites(context.Background(), "u1", reach(), 3)
	require.ErrorIs(t, err, errBoom)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestItemsNamingNobodyReadNothing(t *testing.T) {
	s, mock := newMock(t)
	items, err := s.Recents(context.Background(), "", reach(), 0)
	require.NoError(t, err)
	assert.Empty(t, items)
	items, err = s.Favorites(context.Background(), "u1", portaldomain.NewReach("", "", "", portaldomain.ScopeAll), 0)
	require.NoError(t, err)
	assert.Empty(t, items)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestItemsScanFailure(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("FROM portal_opens e").
		WillReturnRows(sqlmock.NewRows([]string{"kind", "id"}).AddRow("asset", "a1"))
	_, err := s.Recents(context.Background(), "u1", reach(), 0)
	require.Error(t, err)
}

func TestTrash(t *testing.T) {
	s, mock := newMock(t)
	deleted := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta("FROM ((SELECT 'asset' AS kind")).
		WillReturnRows(sqlmock.NewRows([]string{"kind", "id", "name", "content_type", "owner_email", "deleted_at", "count"}).
			AddRow("asset", "a1", "Old", "text/html", "o@example.com", deleted, 2).
			AddRow("collection", "c1", "Q2", "", "o@example.com", deleted.Add(-time.Hour), 2))
	page, err := s.Trash(context.Background(), Scope{Owner: portaldomain.NewAssetOwner("u1", "o@example.com")}, 30*24*time.Hour, 0, -4)
	require.NoError(t, err)
	assert.Equal(t, 2, page.Total)
	require.Len(t, page.Items, 2)
	assert.Equal(t, deleted.Add(30*24*time.Hour), page.Items[0].PurgeAt)
	assert.Equal(t, KindCollection, page.Items[1].Kind)

	mock.ExpectQuery("FROM").WillReturnError(errBoom)
	_, err = s.Trash(context.Background(), Scope{Everyone: true}, time.Hour, 5, 0)
	require.ErrorIs(t, err, errBoom)
	require.NoError(t, mock.ExpectationsWereMet())
}

// An owner's trash reads only their rows; everyone's reads every owner's.
func TestBuildTrashScopes(t *testing.T) {
	q, args, err := buildTrash(Scope{Owner: portaldomain.NewAssetOwner("u1", "o@example.com")}, 10, 0)
	require.NoError(t, err)
	assert.Contains(t, q, "owner_id = $")
	assert.Contains(t, args, "u1")
	q, args, err = buildTrash(Scope{Everyone: true}, 10, 20)
	require.NoError(t, err)
	assert.NotContains(t, q, "owner_id")
	assert.Equal(t, []any{10, 20}, args)
	q, _, err = buildTrash(Scope{Owner: portaldomain.NewAssetOwner("", "o@example.com")}, 10, 0)
	require.NoError(t, err)
	assert.Contains(t, q, "FALSE", "an owner with no id owns no collection")
}

func TestRestore(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE portal_assets SET deleted_at = NULL")).WithArgs("a1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Restore(context.Background(), KindAsset, "a1"))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE portal_collections SET deleted_at = NULL")).WithArgs("c1").
		WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.Restore(context.Background(), KindCollection, "c1"), ErrNotDeleted)
	mock.ExpectExec("UPDATE portal_assets").WillReturnError(errBoom)
	require.ErrorIs(t, s.Restore(context.Background(), KindAsset, "a1"), errBoom)
	mock.ExpectExec("UPDATE portal_assets").WillReturnResult(sqlmock.NewErrorResult(errBoom))
	require.ErrorIs(t, s.Restore(context.Background(), KindAsset, "a1"), errBoom)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUnused(t *testing.T) {
	s, mock := newMock(t)
	created := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)
	last := time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)
	cols := []string{"id", "name", "content_type", "owner_email", "status", "opens", "last_opened", "created_at", "count"}
	mock.ExpectQuery(regexp.QuoteMeta("FROM portal_assets a LEFT JOIN (SELECT target_id")).
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow("a1", "Vendor list", "text/csv", "o@example.com", "draft", 0, nil, created, 2).
			AddRow("a2", "Inventory", "text/html", "o@example.com", "", 3, last, created, 2))
	page, err := s.Unused(context.Background(), Scope{Owner: portaldomain.NewAssetOwner("u1", "")}, time.Now(), 0, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	assert.Nil(t, page.Items[0].LastOpened)
	assert.Equal(t, "draft", page.Items[0].Status)
	require.NotNil(t, page.Items[1].LastOpened)
	assert.Equal(t, last, *page.Items[1].LastOpened)
	assert.Equal(t, int64(3), page.Items[1].Opens)

	mock.ExpectQuery("FROM portal_assets a").WillReturnError(errBoom)
	_, err = s.Unused(context.Background(), Scope{Everyone: true}, time.Now(), 0, 0)
	require.ErrorIs(t, err, errBoom)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Unused counts opens by everyone and never who opened: the statement reads
// no user column.
func TestBuildUnusedCountsNotWho(t *testing.T) {
	q, args, err := buildUnused(Scope{Owner: portaldomain.NewAssetOwner("u1", "")}, time.Unix(0, 0), 10, 0)
	require.NoError(t, err)
	assert.NotContains(t, q, "user_id")
	assert.Contains(t, q, "o.last_opened IS NULL OR o.last_opened <")
	assert.Contains(t, args, "u1")
	q, _, err = buildUnused(Scope{Everyone: true}, time.Unix(0, 0), 10, 0)
	require.NoError(t, err)
	assert.NotContains(t, q, "owner_id")
}

type fakeGrouper struct {
	groups []assetsimilar.Group
	err    error
	asked  assetsimilar.GroupsQuery
}

func (f *fakeGrouper) Groups(_ context.Context, q assetsimilar.GroupsQuery) ([]assetsimilar.Group, error) {
	f.asked = q
	return f.groups, f.err
}

func twoGroups() []assetsimilar.Group {
	t0 := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	return []assetsimilar.Group{
		{Similarity: 0.964, Members: []assetsimilar.Member{
			{AssetID: "a1", Name: "Q3 revenue", CurrentVersion: 4, UpdatedAt: t0},
			{AssetID: "a2", Name: "Q3 revenue (copy)", CurrentVersion: 1, UpdatedAt: t0.Add(-time.Hour)},
		}},
		{Similarity: 0.93, Members: []assetsimilar.Member{
			{AssetID: "b1", Name: "Warehouse", CurrentVersion: 2, UpdatedAt: t0},
			{AssetID: "b2", Name: "Weekly warehouse", CurrentVersion: 1, UpdatedAt: t0},
		}},
		{Similarity: 0.99, Members: []assetsimilar.Member{{AssetID: "lone", CurrentVersion: 1}}},
	}
}

func TestDuplicatesLeavesOutDismissedGroups(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	g := &fakeGrouper{groups: twoGroups()}
	s := New(db, g)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT a.id, COALESCE(st.status, ''), COALESCE(o.opens, 0)")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status", "opens"}).
			AddRow("a1", "published", 41).AddRow("a2", "", 2))
	dismissedDigest := Digest([]DuplicateMember{{ID: "b1", Version: 2}, {ID: "b2", Version: 1}})
	mock.ExpectQuery("FROM portal_duplicate_dismissals").
		WillReturnRows(sqlmock.NewRows([]string{"group_digest"}).AddRow(dismissedDigest))

	owner := portaldomain.NewAssetOwner("u1", "o@example.com")
	groups, err := s.Duplicates(context.Background(), "u1", Scope{Owner: owner})
	require.NoError(t, err)
	require.Len(t, groups, 1, "the dismissed group and the one-member group are left out")
	assert.Equal(t, 96, groups[0].Similarity)
	assert.Equal(t, "published", groups[0].Members[0].Status)
	assert.Equal(t, int64(41), groups[0].Members[0].Opens)
	require.NotNil(t, g.asked.Owner)
	assert.Equal(t, owner, *g.asked.Owner)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDuplicatesEveryoneAsksEveryOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	g := &fakeGrouper{}
	groups, err := New(db, g).Duplicates(context.Background(), "u1", Scope{Everyone: true})
	require.NoError(t, err)
	assert.Empty(t, groups)
	assert.Nil(t, g.asked.Owner)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDuplicatesFailures(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	_, err = New(db, &fakeGrouper{err: errBoom}).Duplicates(context.Background(), "u1", Scope{})
	require.ErrorIs(t, err, errBoom)

	mock.ExpectQuery("SELECT a.id").WillReturnError(errBoom)
	_, err = New(db, &fakeGrouper{groups: twoGroups()}).Duplicates(context.Background(), "u1", Scope{})
	require.ErrorIs(t, err, errBoom)

	mock.ExpectQuery("SELECT a.id").WillReturnRows(sqlmock.NewRows([]string{"id", "status", "opens"}))
	mock.ExpectQuery("FROM portal_duplicate_dismissals").WillReturnError(errBoom)
	_, err = New(db, &fakeGrouper{groups: twoGroups()}).Duplicates(context.Background(), "u1", Scope{})
	require.ErrorIs(t, err, errBoom)

	groups, err := New(db, nil).Duplicates(context.Background(), "u1", Scope{})
	require.NoError(t, err)
	assert.Empty(t, groups)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A group's digest changes when a member gets a new version, which is what
// brings a dismissed group back.
func TestDigestFollowsVersions(t *testing.T) {
	a := Digest([]DuplicateMember{{ID: "a1", Version: 1}, {ID: "a2", Version: 3}})
	b := Digest([]DuplicateMember{{ID: "a2", Version: 3}, {ID: "a1", Version: 1}})
	c := Digest([]DuplicateMember{{ID: "a1", Version: 2}, {ID: "a2", Version: 3}})
	assert.Equal(t, a, b, "member order does not change the digest")
	assert.NotEqual(t, a, c)
	assert.Len(t, a, 64)
	assert.Equal(t, strings.ToLower(a), a)
}

func TestDismissAndUndismiss(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("INSERT INTO portal_duplicate_dismissals").WithArgs("u1", "d1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM portal_duplicate_dismissals").WithArgs("u1", "d1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Dismiss(context.Background(), "u1", "d1"))
	require.NoError(t, s.Undismiss(context.Background(), "u1", "d1"))
	require.ErrorIs(t, s.Dismiss(context.Background(), "", "d1"), ErrNoUser)
	mock.ExpectExec("DELETE FROM portal_duplicate_dismissals").WillReturnError(errBoom)
	require.ErrorIs(t, s.Undismiss(context.Background(), "u1", "d1"), errBoom)
	require.NoError(t, mock.ExpectationsWereMet())
}
