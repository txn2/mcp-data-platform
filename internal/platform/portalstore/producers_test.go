package portalstore

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/assetindex"
	"github.com/txn2/mcp-data-platform/internal/platform/collectionindex"
	"github.com/txn2/mcp-data-platform/internal/platform/knowledgepageindex"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// recordingEnqueuer stands in for the queue's job store so a layer test can see
// which producer a store write reached.
type recordingEnqueuer struct {
	keys []indexjobs.Key
}

func (r *recordingEnqueuer) Enqueue(_ context.Context, key indexjobs.Key, _ indexjobs.Trigger) (bool, error) {
	r.keys = append(r.keys, key)
	return true, nil
}

// TestIndexProducersCoverEveryIndexedKind pins the set the queue binds: one
// producer per indexed portal kind, and nothing for the stores that carry no
// index (shares, versions, threads).
func TestIndexProducersCoverEveryIndexedKind(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	h := New(db, nil, nil, Config{Name: "portal"})
	require.NotNil(t, h)

	kinds := make([]string, 0, 3)
	for _, p := range h.IndexProducers() {
		kinds = append(kinds, p.Kind())
	}
	assert.ElementsMatch(t,
		[]string{assetindex.SourceKind, collectionindex.SourceKind, knowledgepageindex.SourceKind},
		kinds)
}

// TestIndexProducersAreTheOnesTheStoresNotify is the assertion that the exposed
// producers are the same objects the stores hold: binding what IndexProducers
// returns is what makes an asset write enqueue its own job. A test that only
// checked the kinds would pass even if New handed the stores different producers.
func TestIndexProducersAreTheOnesTheStoresNotify(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	h := New(db, nil, nil, Config{Name: "portal"})
	require.NotNil(t, h)

	enq := &recordingEnqueuer{}
	for _, p := range h.IndexProducers() {
		p.Bind(enq)
	}

	mock.ExpectExec("INSERT INTO portal_assets").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO portal_collections").WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, h.AssetStore().Insert(context.Background(), portal.Asset{ID: "a1", Name: "Dash"}))
	require.NoError(t, h.CollectionStore().Insert(context.Background(), portal.Collection{ID: "c1", Name: "Set"}))

	assert.Equal(t, []indexjobs.Key{
		{SourceKind: assetindex.SourceKind, SourceID: "a1"},
		{SourceKind: collectionindex.SourceKind, SourceID: "c1"},
	}, enq.keys)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIndexProducersNilAndInjectedStores covers the two shapes that expose none:
// a nil Handle (no database) and one assembled from injected stores, which bring
// their own indexing arrangements.
func TestIndexProducersNilAndInjectedStores(t *testing.T) {
	t.Parallel()
	var nilHandle *Handle
	assert.Nil(t, nilHandle.IndexProducers())
	assert.Empty(t, NewFromStores(Stores{}, nil, Config{}).IndexProducers())
}
