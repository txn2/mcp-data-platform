package assetrefstore

import (
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
)

const (
	assetID  = "asset_1"
	refToken = "tok-logo"
	logoURI  = "mcp://global/brand/logo.png"
)

var errDB = errors.New("connection refused")

func newMock(t *testing.T) (assetrefs.Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return New(db), mock
}

func sampleRef() assetrefs.Ref {
	return assetrefs.Ref{
		AssetID: assetID, TargetKind: assetrefs.TargetResource,
		TargetID: "res-logo", URI: logoURI,
		RefToken: refToken, DeclaredBy: "analyst@example.com",
	}
}

func refRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"asset_id", "target_kind", "target_id", "uri", "ref_token",
		"position", "declared_by", "created_at",
	})
}

// TestReplaceClearsThenWritesInOneTransaction is the property the delete and
// the insert must share: a delete that committed without its insert would leave
// a rendered asset with every image broken and no record of what it named.
func TestReplaceClearsThenWritesInOneTransaction(t *testing.T) {
	store, mock := newMock(t)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM portal_asset_refs").
		WithArgs(assetID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO portal_asset_refs").
		WithArgs(assetID, "resource", "res-logo", logoURI, refToken, 0, "analyst@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, store.Replace(t.Context(), assetID, []assetrefs.Ref{sampleRef()}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReplaceStampsDeclaredOrder proves the stored position is the caller's
// order, not whatever the slice happened to carry: the rewrite reads it to
// decide which of two overlapping URIs wins.
func TestReplaceStampsDeclaredOrder(t *testing.T) {
	store, mock := newMock(t)
	second := sampleRef()
	second.TargetID = "res-photo"
	second.URI = "mcp://global/brand/photo.jpg"
	second.RefToken = "tok-photo"
	second.Position = 99 // deliberately wrong; the store assigns the real one

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM portal_asset_refs").
		WithArgs(assetID).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO portal_asset_refs").
		WithArgs(assetID, "resource", "res-logo", logoURI, refToken, 0, "analyst@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO portal_asset_refs").
		WithArgs(assetID, "resource", "res-photo", second.URI, "tok-photo", 1, "analyst@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, store.Replace(t.Context(), assetID,
		[]assetrefs.Ref{sampleRef(), second}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReplaceEmptyRemovesEveryReference proves clearing is expressible: an
// author who removes the last image from a report leaves it referencing nothing.
func TestReplaceEmptyRemovesEveryReference(t *testing.T) {
	store, mock := newMock(t)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM portal_asset_refs").
		WithArgs(assetID).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	require.NoError(t, store.Replace(t.Context(), assetID, nil))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReplaceRollsBackOnInsertFailure is the reason the write is a transaction:
// a failed insert must leave the previous references standing.
func TestReplaceRollsBackOnInsertFailure(t *testing.T) {
	store, mock := newMock(t)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM portal_asset_refs").
		WithArgs(assetID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO portal_asset_refs").WillReturnError(errDB)
	mock.ExpectRollback()

	err := store.Replace(t.Context(), assetID, []assetrefs.Ref{sampleRef()})
	require.ErrorIs(t, err, errDB)
	assert.Contains(t, err.Error(), logoURI, "the failure must name the reference that could not be recorded")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReplaceReportsFailures covers the remaining transaction boundaries.
func TestReplaceReportsFailures(t *testing.T) {
	tests := map[string]func(sqlmock.Sqlmock){
		"begin fails": func(m sqlmock.Sqlmock) { m.ExpectBegin().WillReturnError(errDB) },
		"delete fails": func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec("DELETE FROM portal_asset_refs").WillReturnError(errDB)
			m.ExpectRollback()
		},
		"commit fails": func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec("DELETE FROM portal_asset_refs").
				WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectCommit().WillReturnError(errDB)
		},
	}
	for name, expect := range tests {
		t.Run(name, func(t *testing.T) {
			store, mock := newMock(t)
			expect(mock)
			assert.ErrorIs(t, store.Replace(t.Context(), assetID, nil), errDB)
		})
	}
}

// TestListByAssetReturnsDeclaredOrder proves serving reads the references in
// the order they were declared, which is what the rewrite depends on.
func TestListByAssetReturnsDeclaredOrder(t *testing.T) {
	store, mock := newMock(t)
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT asset_id, target_kind, target_id, uri, ref_token, position, declared_by, created_at").
		WithArgs(assetID).
		WillReturnRows(refRows().
			AddRow(assetID, "resource", "res-logo", logoURI, refToken, 0, "analyst@example.com", now).
			AddRow(assetID, "resource", "res-photo", "mcp://global/brand/photo.jpg", "tok-photo", 1, "", now))

	got, err := store.ListByAsset(t.Context(), assetID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "res-logo", got[0].TargetID)
	assert.Equal(t, refToken, got[0].RefToken)
	assert.Equal(t, 1, got[1].Position)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestListByAssetReportsFailures pins that a read fault is an error rather than
// an empty list, so a caller cannot mistake an outage for "no references".
func TestListByAssetReportsFailures(t *testing.T) {
	t.Run("query fails", func(t *testing.T) {
		store, mock := newMock(t)
		mock.ExpectQuery("SELECT asset_id").WillReturnError(errDB)
		_, err := store.ListByAsset(t.Context(), assetID)
		assert.ErrorIs(t, err, errDB)
	})
	t.Run("scan fails", func(t *testing.T) {
		store, mock := newMock(t)
		mock.ExpectQuery("SELECT asset_id").WillReturnRows(
			refRows().AddRow(assetID, "resource", "res-logo", logoURI, refToken, "not-an-int", "", time.Now()))
		_, err := store.ListByAsset(t.Context(), assetID)
		assert.Error(t, err)
	})
	t.Run("rows error", func(t *testing.T) {
		store, mock := newMock(t)
		mock.ExpectQuery("SELECT asset_id").WillReturnRows(refRows().RowError(0, errDB).
			AddRow(assetID, "resource", "res-logo", logoURI, refToken, 0, "", time.Now()))
		_, err := store.ListByAsset(t.Context(), assetID)
		assert.ErrorIs(t, err, errDB)
	})
}

// TestGetByTokenRequiresBothAssetAndToken pins the query's two-column match:
// possession of a token grants the resource through the asset it was minted
// for, and through no other.
func TestGetByTokenRequiresBothAssetAndToken(t *testing.T) {
	store, mock := newMock(t)
	now := time.Now().UTC()

	mock.ExpectQuery("WHERE asset_id = \\$1 AND ref_token = \\$2").
		WithArgs(assetID, refToken).
		WillReturnRows(refRows().AddRow(assetID, "resource", "res-logo", logoURI, refToken, 0, "", now))

	got, err := store.GetByToken(t.Context(), assetID, refToken)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "res-logo", got.TargetID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetByTokenNotFoundIsNotAnError pins the contract the serving handler and
// every fake depend on: no such reference is (nil, nil), so a wrong token and a
// database fault stay distinguishable.
func TestGetByTokenNotFoundIsNotAnError(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("WHERE asset_id").WithArgs(assetID, "tok-nope").WillReturnRows(refRows())

	got, err := store.GetByToken(t.Context(), assetID, "tok-nope")
	require.NoError(t, err)
	assert.Nil(t, got)
}

// TestGetByTokenReportsRealFailures is the other half of the contract above.
func TestGetByTokenReportsRealFailures(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("WHERE asset_id").WillReturnError(errDB)

	got, err := store.GetByToken(t.Context(), assetID, refToken)
	require.ErrorIs(t, err, errDB)
	assert.Nil(t, got)
}

// TestListByTargetFiltersOnKindAndID is the read the resource's own detail
// view makes: every asset naming one file, whoever owns them.
func TestListByTargetFiltersOnKindAndID(t *testing.T) {
	store, mock := newMock(t)
	now := time.Now()

	mock.ExpectQuery("SELECT .+ FROM portal_asset_refs WHERE target_kind = .+ AND target_id = ").
		WithArgs("resource", "res-logo", 50).
		WillReturnRows(refRows().
			AddRow(assetID, "resource", "res-logo", logoURI, refToken, 0, "analyst@example.com", now).
			AddRow("asset_2", "resource", "res-logo", logoURI, "tok-2", 0, "analyst@example.com", now))

	refs, err := store.ListByTarget(t.Context(), assetrefs.TargetResource, "res-logo", 50)
	require.NoError(t, err)
	require.Len(t, refs, 2)
	assert.Equal(t, assetID, refs[0].AssetID)
	assert.Equal(t, "asset_2", refs[1].AssetID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A resource nothing references is an empty answer, not an error: the section
// that reads this renders nothing rather than a failure.
func TestListByTargetNoReferences(t *testing.T) {
	store, mock := newMock(t)

	mock.ExpectQuery("SELECT .+ FROM portal_asset_refs WHERE target_kind = .+ AND target_id = ").
		WithArgs("resource", "res-orphan", 50).WillReturnRows(refRows())

	refs, err := store.ListByTarget(t.Context(), assetrefs.TargetResource, "res-orphan", 50)
	require.NoError(t, err)
	assert.Empty(t, refs)
}

func TestListByTargetQueryFailure(t *testing.T) {
	store, mock := newMock(t)

	mock.ExpectQuery("SELECT .+ FROM portal_asset_refs WHERE target_kind = .+ AND target_id = ").
		WillReturnError(errDB)

	_, err := store.ListByTarget(t.Context(), assetrefs.TargetResource, "res-logo", 50)
	require.Error(t, err)
	assert.ErrorIs(t, err, errDB)
}

// A row whose columns do not match the scan is an error rather than a partial
// list, so a schema drift is reported instead of silently shortening the answer.
func TestListByTargetScanFailure(t *testing.T) {
	store, mock := newMock(t)

	mock.ExpectQuery("SELECT .+ FROM portal_asset_refs WHERE target_kind = .+ AND target_id = ").
		WillReturnRows(refRows().
			AddRow(assetID, "resource", "res-logo", logoURI, refToken, "not-an-int", "a@example.com", time.Now()))

	_, err := store.ListByTarget(t.Context(), assetrefs.TargetResource, "res-logo", 50)
	assert.Error(t, err)
}

// TestAttachInsertsAtTheEndAndReportsDuplicate is the property that keeps a
// person's add from clobbering an agent's save: the write touches one row and
// derives its own position, and the primary key -- not a read the handler did
// first -- decides whether the asset already names the file.
func TestAttachInsertsAtTheEndAndReportsDuplicate(t *testing.T) {
	store, mock := newMock(t)

	mock.ExpectExec("INSERT INTO portal_asset_refs").
		WithArgs(assetID, "resource", "res-logo", logoURI, refToken, "analyst@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))

	added, err := store.Attach(t.Context(), sampleRef())
	require.NoError(t, err)
	assert.True(t, added)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAttachAlreadyReferenced(t *testing.T) {
	store, mock := newMock(t)

	// ON CONFLICT DO NOTHING: no error, no row.
	mock.ExpectExec("INSERT INTO portal_asset_refs").
		WillReturnResult(sqlmock.NewResult(0, 0))

	added, err := store.Attach(t.Context(), sampleRef())
	require.NoError(t, err)
	assert.False(t, added, "an asset that already names the file gains nothing")
}

func TestAttachFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectExec("INSERT INTO portal_asset_refs").WillReturnError(errDB)

	_, err := store.Attach(t.Context(), sampleRef())
	assert.ErrorIs(t, err, errDB)
}

func TestDetachRemovesOneAndReportsIt(t *testing.T) {
	store, mock := newMock(t)

	mock.ExpectExec("DELETE FROM portal_asset_refs").
		WithArgs(assetID, "resource", "res-logo").WillReturnResult(sqlmock.NewResult(0, 1))

	removed, err := store.Detach(t.Context(), assetID, assetrefs.TargetResource, "res-logo")
	require.NoError(t, err)
	assert.True(t, removed)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A reference that is not there is (false, nil): the caller turns that into a
// not-found, and a database fault must not arrive in the same shape.
func TestDetachNothingToRemove(t *testing.T) {
	store, mock := newMock(t)

	mock.ExpectExec("DELETE FROM portal_asset_refs").
		WillReturnResult(sqlmock.NewResult(0, 0))

	removed, err := store.Detach(t.Context(), assetID, assetrefs.TargetResource, "res-nope")
	require.NoError(t, err)
	assert.False(t, removed)
}

func TestDetachFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectExec("DELETE FROM portal_asset_refs").WillReturnError(errDB)

	_, err := store.Detach(t.Context(), assetID, assetrefs.TargetResource, "res-logo")
	assert.ErrorIs(t, err, errDB)
}

// The limit reaches the SQL rather than being applied to the rows after they
// arrive: narrowing the answer to what a reader may open costs a query per
// asset, so the rows this read returns are the work the route is bounded by.
func TestListByTargetAppliesTheLimitInSQL(t *testing.T) {
	store, mock := newMock(t)

	mock.ExpectQuery("SELECT .+ FROM portal_asset_refs WHERE target_kind = .+ AND target_id = .+ LIMIT ").
		WithArgs("resource", "res-logo", 7).WillReturnRows(refRows())

	_, err := store.ListByTarget(t.Context(), assetrefs.TargetResource, "res-logo", 7)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAttachAndDetachKeyOnTheKind is what makes the two id spaces safe to share
// one table (#1488): a resource and an asset whose ids happen to match are two
// references, and detaching one must not remove the other.
func TestAttachAndDetachKeyOnTheKind(t *testing.T) {
	store, mock := newMock(t)
	assetRef := sampleRef()
	assetRef.TargetKind = assetrefs.TargetAsset
	assetRef.TargetID = "res-logo" // deliberately the same string as the resource
	assetRef.URI = "mcp:asset:res-logo"
	assetRef.RefToken = "tok-asset"

	mock.ExpectExec("INSERT INTO portal_asset_refs").
		WithArgs(assetID, "asset", "res-logo", "mcp:asset:res-logo", "tok-asset", "analyst@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("DELETE FROM portal_asset_refs").
		WithArgs(assetID, "asset", "res-logo").WillReturnResult(sqlmock.NewResult(0, 1))

	added, err := store.Attach(t.Context(), assetRef)
	require.NoError(t, err)
	assert.True(t, added)

	removed, err := store.Detach(t.Context(), assetID, assetrefs.TargetAsset, "res-logo")
	require.NoError(t, err)
	assert.True(t, removed)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReadsCarryTheKindBack proves the stored kind survives the round trip: a
// row read back as the wrong kind would be served from the wrong store.
func TestReadsCarryTheKindBack(t *testing.T) {
	store, mock := newMock(t)
	now := time.Now()

	mock.ExpectQuery("SELECT .+ FROM portal_asset_refs").
		WithArgs(assetID).
		WillReturnRows(refRows().
			AddRow(assetID, "asset", "ast-target", "mcp:asset:ast-target", refToken, 0, "", now))

	got, err := store.ListByAsset(t.Context(), assetID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, assetrefs.TargetAsset, got[0].TargetKind)
	assert.Equal(t, "ast-target", got[0].TargetID)
}
