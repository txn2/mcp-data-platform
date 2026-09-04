package portalstore

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// An asset read carries only its newest captures because nothing bounds how
// many a refreshed asset accumulates (#1623). This is the read that reaches the
// rest: the page is cut in the statement, so asking for twenty never
// materializes three hundred.

func TestListProvenanceCaptures_PagesNewestFirst(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db, nil)
	mock.ExpectQuery("jsonb_array_elements").
		WithArgs("abc123", 20, 20).
		WillReturnRows(sqlmock.NewRows([]string{"total", "captures"}).
			AddRow(329, []byte(`[{"tool":"manage_asset","version":309},{"tool":"manage_asset","version":308}]`)))

	got, total, err := store.ListProvenanceCaptures(context.Background(), "abc123", 20, 20)
	require.NoError(t, err)
	assert.Equal(t, 329, total, "how many the asset holds, not how many the page carries")
	require.Len(t, got, 2)
	assert.Equal(t, 309, got[0].Version, "newest first")
	assert.Equal(t, 308, got[1].Version)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The clamp is applied before the statement runs, so a caller asking for
// everything gets a page and a caller asking for nothing gets the default.
func TestListProvenanceCaptures_ClampsThePageItAsksFor(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db, nil)
	mock.ExpectQuery("jsonb_array_elements").
		WithArgs("abc123", 0, portaldomain.MaxProvenancePageSize).
		WillReturnRows(sqlmock.NewRows([]string{"total", "captures"}).AddRow(0, []byte(`[]`)))

	got, total, err := store.ListProvenanceCaptures(context.Background(), "abc123", -1, 100000)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Zero(t, total)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListProvenanceCaptures_MissingAsset(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db, nil)
	mock.ExpectQuery("jsonb_array_elements").
		WillReturnRows(sqlmock.NewRows([]string{"total", "captures"}))

	_, _, err = store.ListProvenanceCaptures(context.Background(), "gone", 0, 20)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "asset not found or deleted")
}

func TestListProvenanceCaptures_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db, nil)
	mock.ExpectQuery("jsonb_array_elements").WillReturnError(errors.New("connection reset"))

	_, _, err = store.ListProvenanceCaptures(context.Background(), "abc123", 0, 20)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading provenance captures")
}

func TestListProvenanceCaptures_MalformedPage(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db, nil)
	mock.ExpectQuery("jsonb_array_elements").
		WillReturnRows(sqlmock.NewRows([]string{"total", "captures"}).AddRow(1, []byte(`not json`)))

	_, _, err = store.ListProvenanceCaptures(context.Background(), "abc123", 0, 20)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshaling provenance captures")
}

// The summary is what a listing carries in the captures' place, and it is
// computed over whichever column the projection names -- the shared-with-me
// page reads it off the joined table.
func TestProvenanceSummaryExpr_ReadsTheColumnItIsGiven(t *testing.T) {
	assert.Contains(t, provenanceSummaryExpr("pa.provenance"), "pa.provenance -> 'captures'")
	assert.NotContains(t, provenanceSummaryExpr("pa.provenance"), " provenance -> 'captures'")
	for _, key := range []string{
		"'captures'", "'calls'", "'first_captured_at'", "'last_captured_at'", "'last_tool'", "'last_session_id'",
	} {
		assert.Contains(t, provenanceSummaryExpr("provenance"), key)
	}
}

// Every listing projection reads the summary and none of them reads the
// captures: that is the whole of the fix, and a projection that drifts back to
// the column undoes it silently.
func TestListingProjectionsCarryTheSummaryNotTheCaptures(t *testing.T) {
	listing, _, err := buildAssetSelect(portaldomain.AssetFilter{
		Owner: portaldomain.NewAssetOwner("u1", "u@example.com"),
	})
	require.NoError(t, err)

	for name, sql := range map[string]string{
		"list":           listing,
		"search columns": assetSearchColumns,
		"shared with me": buildSharedWithUserSelect(),
	} {
		t.Run(name, func(t *testing.T) {
			assert.Contains(t, sql, "jsonb_build_object", "the summary is projected")
			// The column appears only inside the summary, where it is always
			// followed by an arrow. Selected on its own it ends the item.
			assert.NotRegexp(t, `(?m)provenance\s*(?:,|$)`, sql,
				"the provenance column itself is never selected by a listing")
		})
	}
}
