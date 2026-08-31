package portalstore

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/producedby"
)

// recordingProducers captures what Insert noted.
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

// TestInsertRecordsTheCreatingProducer is acceptance criterion 1 at the asset
// write funnel: the asset a script's output writer creates carries a producer
// row naming that script, marked as having created it.
func TestInsertRecordsTheCreatingProducer(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	rec := &recordingProducers{}
	store := NewPostgresAssetStore(db, rec)
	mock.ExpectExec("INSERT INTO portal_assets").WillReturnResult(sqlmock.NewResult(0, 1))

	ctx := producedby.With(context.Background(), producedby.Producer{
		Kind: producedby.KindScript, ID: "script-1", Label: "daily-sales",
	})
	require.NoError(t, store.Insert(ctx, portaldomain.Asset{ID: "asset-1", OwnerID: "script:daily-sales"}))

	require.Len(t, rec.writes, 1)
	assert.Equal(t, producedby.TargetAsset, rec.writes[0].TargetKind)
	assert.Equal(t, "asset-1", rec.writes[0].TargetID)
	assert.True(t, rec.writes[0].Created)
	assert.Equal(t, "script-1", rec.writes[0].Producer.ID)
	assert.Equal(t, "daily-sales", rec.writes[0].Producer.Label)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInsertRecordsNothingForAnUnnamedProducer covers a create made outside any
// surface that names one.
func TestInsertRecordsNothingForAnUnnamedProducer(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	rec := &recordingProducers{}
	store := NewPostgresAssetStore(db, rec)
	mock.ExpectExec("INSERT INTO portal_assets").WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.Insert(context.Background(), portaldomain.Asset{ID: "asset-1"}))
	assert.Empty(t, rec.writes)
}

// TestInsertFailureRecordsNoProducer keeps the record honest: a create that did
// not happen is not something a producer produced.
func TestInsertFailureRecordsNoProducer(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	rec := &recordingProducers{}
	store := NewPostgresAssetStore(db, rec)
	mock.ExpectExec("INSERT INTO portal_assets").WillReturnError(assert.AnError)

	ctx := producedby.With(context.Background(), producedby.Producer{
		Kind: producedby.KindSession, ID: "sess-1",
	})
	assert.Error(t, store.Insert(ctx, portaldomain.Asset{ID: "asset-1"}))
	assert.Empty(t, rec.writes)
}
