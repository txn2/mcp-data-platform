package portalversions

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/producedby"
)

// recordingProducers captures what CreateVersion noted.
type recordingProducers struct{ writes []producedby.Write }

func (r *recordingProducers) Record(_ context.Context, w producedby.Write) error {
	r.writes = append(r.writes, w)
	return nil
}

func (*recordingProducers) ListByTarget(context.Context, string, string) ([]producedby.Row, error) {
	return nil, nil
}

func (*recordingProducers) ListByProducer(context.Context, string, string, int) ([]producedby.Row, error) {
	return nil, nil
}

// expectCreateVersion drives one successful CreateVersion against a mock,
// numbering the new version at current+1.
func expectCreateVersion(mock sqlmock.Sqlmock, assetID string, current int) {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_version, max_versions FROM portal_assets").
		WithArgs(assetID).
		WillReturnRows(sqlmock.NewRows([]string{"current_version", "max_versions"}).AddRow(current, nil))
	mock.ExpectExec("INSERT INTO portal_asset_versions").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE portal_assets").WillReturnResult(sqlmock.NewResult(1, 1))
	// Nowhere near the retention cap, so the prune issues no statement at all.
	mock.ExpectCommit()
}

func newProducerStore(t *testing.T) (portaldomain.VersionStore, sqlmock.Sqlmock, *recordingProducers) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	rec := &recordingProducers{}
	return NewPostgres(db, nil, nil, rec), mock, rec
}

// TestCreateVersionRecordsTheProducer covers the second asset write funnel: a
// version written after the create is a write of its own, by whoever made it.
func TestCreateVersionRecordsTheProducer(t *testing.T) {
	store, mock, rec := newProducerStore(t)
	expectCreateVersion(mock, "asset-1", 1)

	ctx := producedby.With(context.Background(), producedby.Producer{
		Kind: producedby.KindScript, ID: "script-1", Label: "daily-sales",
	})
	version, err := store.CreateVersion(ctx, portaldomain.AssetVersion{ID: "v", AssetID: "asset-1"})
	require.NoError(t, err)
	assert.Equal(t, 2, version)

	require.Len(t, rec.writes, 1)
	assert.Equal(t, producedby.TargetAsset, rec.writes[0].TargetKind)
	assert.Equal(t, "asset-1", rec.writes[0].TargetID)
	assert.False(t, rec.writes[0].Created, "writing a version is not creating the asset")
	assert.Equal(t, 2, rec.writes[0].Version)
	assert.False(t, rec.writes[0].Uncounted, "a version after the first is a write of its own")
	assert.Equal(t, "script-1", rec.writes[0].Producer.ID)
}

// TestCreateVersionRecordsButDoesNotCountTheFirstVersion is what keeps a single
// save from reading as two writes while still recording the version it wrote:
// version 1 is the content half of the create the asset store has already
// counted, and it is still the version that producer produced.
func TestCreateVersionRecordsButDoesNotCountTheFirstVersion(t *testing.T) {
	store, mock, rec := newProducerStore(t)
	expectCreateVersion(mock, "asset-1", 0)

	ctx := producedby.With(context.Background(), producedby.Producer{
		Kind: producedby.KindSession, ID: "sess-1",
	})
	version, err := store.CreateVersion(ctx, portaldomain.AssetVersion{ID: "v", AssetID: "asset-1"})
	require.NoError(t, err)
	assert.Equal(t, 1, version)

	require.Len(t, rec.writes, 1)
	assert.Equal(t, 1, rec.writes[0].Version)
	assert.True(t, rec.writes[0].Uncounted, "the create already counted this save")
}

// TestCreateVersionWithoutAProducerRecordsNothing covers a write made outside
// any surface that names a producer.
func TestCreateVersionWithoutAProducerRecordsNothing(t *testing.T) {
	store, mock, rec := newProducerStore(t)
	expectCreateVersion(mock, "asset-1", 3)

	_, err := store.CreateVersion(context.Background(), portaldomain.AssetVersion{ID: "v", AssetID: "asset-1"})
	require.NoError(t, err)
	assert.Empty(t, rec.writes)
}
