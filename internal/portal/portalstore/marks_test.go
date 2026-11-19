package portalstore

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

func markWhere(t *testing.T, filter portaldomain.AssetFilter) (where string, args []any) {
	t.Helper()
	query, args, err := applyMarkFilter(psq.Select("id").From("portal_assets"), filter).ToSql()
	require.NoError(t, err)
	return query, args
}

func TestApplyMarkFilter(t *testing.T) {
	query, args := markWhere(t, portaldomain.AssetFilter{})
	assert.NotContains(t, query, "WHERE", "no status and not verified filters nothing")
	assert.Empty(t, args)

	query, args = markWhere(t, portaldomain.AssetFilter{Status: portaldomain.StatusNone})
	assert.Contains(t, query, "NOT EXISTS (SELECT 1 FROM portal_asset_status st WHERE st.asset_id = portal_assets.id)")
	assert.Empty(t, args)

	query, args = markWhere(t, portaldomain.AssetFilter{Status: portaldomain.StatusDraft})
	assert.Contains(t, query, "EXISTS (SELECT 1 FROM portal_asset_status st WHERE st.asset_id = portal_assets.id AND st.status = $1)")
	assert.NotContains(t, query, "NOT EXISTS")
	assert.Equal(t, []any{"draft"}, args)

	query, args = markWhere(t, portaldomain.AssetFilter{Status: "archived"})
	assert.NotContains(t, query, "portal_asset_status", "an unknown status filters nothing")
	assert.Empty(t, args)

	query, _ = markWhere(t, portaldomain.AssetFilter{Verified: true})
	assert.Contains(t, query, "FROM portal_asset_verifications v WHERE v.asset_id = portal_assets.id")
	assert.Contains(t, query, "v.expires_at IS NULL OR v.expires_at > NOW()", "an expired verification is not Verified")

	query, args = markWhere(t, portaldomain.AssetFilter{Status: portaldomain.StatusPublished, Verified: true})
	assert.Contains(t, query, "portal_asset_status")
	assert.Contains(t, query, "portal_asset_verifications")
	assert.Equal(t, []any{"published"}, args)
}

func TestListingAppliesTheMarkFilter(t *testing.T) {
	query, args, err := buildAssetSelect(portaldomain.AssetFilter{Status: portaldomain.StatusDeprecated, Limit: 5})
	require.NoError(t, err)
	assert.Contains(t, query, "st.status = $1")
	assert.Contains(t, args, "deprecated")
	assert.Contains(t, query, assetStatusColumn, "every listed row carries its status")
	assert.Contains(t, query, assetVerificationColumn, "and its verification")
}

func TestRankedCandidatesPreferVerified(t *testing.T) {
	query, _, err := rankedCandidates(portaldomain.AssetFilter{Status: portaldomain.StatusNone})
	require.NoError(t, err)
	assert.Contains(t, query, verifiedPredicate+" AS "+preferredColumn)
	assert.Contains(t, query, "NOT EXISTS (SELECT 1 FROM portal_asset_status")
}

func TestJSONIntoScan(t *testing.T) {
	var status *portaldomain.AssetStatus
	require.NoError(t, jsonInto{&status}.Scan(nil))
	assert.Nil(t, status, "NULL leaves no status")

	require.NoError(t, jsonInto{&status}.Scan([]byte(`{"value":"draft","note":"wip","set_by":"a@x","set_at":"2026-11-19T01:00:00+00:00"}`)))
	require.NotNil(t, status)
	assert.Equal(t, "draft", status.Value)
	assert.Equal(t, "wip", status.Note)
	assert.Equal(t, time.Date(2026, 11, 19, 1, 0, 0, 0, time.UTC), status.SetAt.UTC())

	var v *portaldomain.AssetVerification
	require.NoError(t, jsonInto{&v}.Scan(`{"verified_by":"c@x","verified_version":2,"expires_at":null}`))
	require.NotNil(t, v)
	assert.Equal(t, 2, v.VerifiedVersion)
	assert.Nil(t, v.ExpiresAt)

	err := jsonInto{&status}.Scan([]byte(`{not json`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding JSON column")

	err = jsonInto{&status}.Scan(42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected int")
}

func TestGetCarriesTheMarksAndDerivesChangedSinceVerified(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db, nil)
	now := time.Now()
	prov, _ := json.Marshal(portaldomain.Provenance{})
	status := []byte(`{"value":"deprecated","note":"see Q4","set_by":"a@x","set_at":"2026-11-19T01:00:00+00:00"}`)
	verification := []byte(`{"verified_by":"c@x","verified_at":"2026-11-18T01:00:00+00:00","verified_version":2,"note":"ok","expires_at":null}`)
	rows := sqlmock.NewRows([]string{
		"id", "owner_id", "owner_email", "name", "description", "content_type", "s3_bucket", "s3_key",
		"thumbnail_s3_key", "thumbnail_dark_s3_key", "thumbnail_version", "thumbnail_dark_version", "thumbnail_renderer", "thumbnail_failure", "thumbnail_failed_version", "size_bytes", "tags", "provenance", "session_id", "current_version", "created_at", "updated_at", "deleted_at", "idempotency_key", "max_versions", "released_version", "status", "verification",
	}).AddRow(
		"abc123", "user1", "user1@example.com", "Q3", "", "text/html", "portal", "key1",
		"", "", 0, 0, 0, "", 0, int64(42), []byte("[]"), prov, "", 3, now, now, nil, "", nil, 0, status, verification,
	)
	// The point read selects the marks, in assetScanDest order.
	mock.ExpectQuery(regexp.QuoteMeta(assetMarkColumns) + `\s+FROM portal_assets WHERE id`).WithArgs("abc123").WillReturnRows(rows)

	asset, err := store.Get(context.Background(), "abc123")
	require.NoError(t, err)
	require.NotNil(t, asset.Status)
	assert.Equal(t, "deprecated", asset.Status.Value)
	assert.Equal(t, "see Q4", asset.Status.Note)
	require.NotNil(t, asset.Verification)
	assert.Equal(t, "c@x", asset.Verification.VerifiedBy)
	assert.True(t, asset.Verification.ChangedSinceVerified, "verified at 2, current is 3")
	require.NoError(t, mock.ExpectationsWereMet())
}
