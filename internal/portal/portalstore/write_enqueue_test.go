package portalstore

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// fakeEnqueuer records the index jobs a bound Producer wrote, so a store test
// asserts on the job the write produced rather than on the notify call.
type fakeEnqueuer struct {
	keys     []indexjobs.Key
	triggers []indexjobs.Trigger
}

func (f *fakeEnqueuer) Enqueue(_ context.Context, key indexjobs.Key, trigger indexjobs.Trigger) (bool, error) {
	f.keys = append(f.keys, key)
	f.triggers = append(f.triggers, trigger)
	return true, nil
}

func boundProducer(kind string) (*indexjobs.Producer, *fakeEnqueuer) {
	enq := &fakeEnqueuer{}
	p := indexjobs.NewProducer(kind)
	p.Bind(enq)
	return p, enq
}

func indexedAssetStore(db *sql.DB) (portaldomain.AssetStore, *fakeEnqueuer) {
	p, enq := boundProducer("portal-assets")
	return NewPostgresAssetStore(db, indexjobs.WithProducer(p)), enq
}

func indexedCollectionStore(db *sql.DB) (portaldomain.CollectionStore, *fakeEnqueuer) {
	p, enq := boundProducer("portal-collections")
	return NewPostgresCollectionStore(db, indexjobs.WithProducer(p)), enq
}

// TestAssetInsertEnqueuesIndexJob: a saved asset produces one TriggerWrite job
// for its own row, so it is searchable in one embed rather than after a sweep.
func TestAssetInsertEnqueuesIndexJob(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store, enq := indexedAssetStore(db)

	mock.ExpectExec("INSERT INTO portal_assets").WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.Insert(context.Background(), portaldomain.Asset{ID: "a1", Name: "Dash"}))
	require.Len(t, enq.keys, 1)
	assert.Equal(t, indexjobs.Key{SourceKind: "portal-assets", SourceID: "a1"}, enq.keys[0])
	assert.Equal(t, indexjobs.TriggerWrite, enq.triggers[0])
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAssetInsertFailureEnqueuesNothing: the enqueue follows the write, so a
// failed insert leaves no job for a row that does not exist.
func TestAssetInsertFailureEnqueuesNothing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store, enq := indexedAssetStore(db)

	mock.ExpectExec("INSERT INTO portal_assets").WillReturnError(errors.New("boom"))

	require.Error(t, store.Insert(context.Background(), portaldomain.Asset{ID: "a1", Name: "Dash"}))
	assert.Empty(t, enq.keys)
}

// TestAssetUpdateEnqueuesOnlyWhenIndexedTextMoves pins the enqueue to the same
// predicate as the embedding clear: name/description/tags owe a re-embed, a
// thumbnail or content-only edit does not.
func TestAssetUpdateEnqueuesOnlyWhenIndexedTextMoves(t *testing.T) {
	name := "Renamed"
	thumb := "thumbs/a1.png"
	tests := []struct {
		name     string
		updates  portaldomain.AssetUpdate
		wantJobs int
	}{
		{name: "name edit", updates: portaldomain.AssetUpdate{Name: &name}, wantJobs: 1},
		{name: "thumbnail only", updates: portaldomain.AssetUpdate{ThumbnailS3Key: &thumb}, wantJobs: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close() //nolint:errcheck // test cleanup
			store, enq := indexedAssetStore(db)

			mock.ExpectExec("UPDATE portal_assets").WillReturnResult(sqlmock.NewResult(0, 1))

			require.NoError(t, store.Update(context.Background(), "a1", tc.updates))
			assert.Len(t, enq.keys, tc.wantJobs)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestAssetUpdateMissingRowEnqueuesNothing: an update that matched no live row
// changed no text, so it owes no job.
func TestAssetUpdateMissingRowEnqueuesNothing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store, enq := indexedAssetStore(db)

	name := "Renamed"
	mock.ExpectExec("UPDATE portal_assets").WillReturnResult(sqlmock.NewResult(0, 0))

	require.Error(t, store.Update(context.Background(), "gone", portaldomain.AssetUpdate{Name: &name}))
	assert.Empty(t, enq.keys)
}

// TestAssetSoftDeleteEnqueuesNothing covers the delete side: an asset leaving
// search queues no unit.
func TestAssetSoftDeleteEnqueuesNothing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store, enq := indexedAssetStore(db)

	mock.ExpectExec("UPDATE portal_assets SET deleted_at").WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.SoftDelete(context.Background(), "a1"))
	assert.Empty(t, enq.keys)
}

// TestCollectionWritesEnqueueIndexJobs covers the three collection writes that
// move CollectionIndexText — create, rename/describe, and re-sectioning — each
// of which drops the stored vector and therefore owes a job.
func TestCollectionWritesEnqueueIndexJobs(t *testing.T) {
	tests := []struct {
		name   string
		expect func(m sqlmock.Sqlmock)
		call   func(s portaldomain.CollectionStore) error
	}{
		{
			name: "insert",
			expect: func(m sqlmock.Sqlmock) {
				m.ExpectExec("INSERT INTO portal_collections").WillReturnResult(sqlmock.NewResult(0, 1))
			},
			call: func(s portaldomain.CollectionStore) error {
				return s.Insert(context.Background(), portaldomain.Collection{ID: "c1", Name: "Set"})
			},
		},
		{
			name: "update",
			expect: func(m sqlmock.Sqlmock) {
				m.ExpectExec("UPDATE portal_collections").WillReturnResult(sqlmock.NewResult(0, 1))
			},
			call: func(s portaldomain.CollectionStore) error {
				return s.Update(context.Background(), "c1", "Set", "desc")
			},
		},
		{
			name: "set sections",
			expect: func(m sqlmock.Sqlmock) {
				m.ExpectBegin()
				m.ExpectExec("DELETE FROM portal_collection_sections").WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectPrepare("INSERT INTO portal_collection_sections")
				m.ExpectPrepare("INSERT INTO portal_collection_items")
				m.ExpectExec("UPDATE portal_collections").WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectCommit()
			},
			call: func(s portaldomain.CollectionStore) error {
				return s.SetSections(context.Background(), "c1", nil)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close() //nolint:errcheck // test cleanup
			store, enq := indexedCollectionStore(db)

			tc.expect(mock)
			require.NoError(t, tc.call(store))

			require.Len(t, enq.keys, 1)
			assert.Equal(t, indexjobs.Key{SourceKind: "portal-collections", SourceID: "c1"}, enq.keys[0])
			assert.Equal(t, indexjobs.TriggerWrite, enq.triggers[0])
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestCollectionNonIndexedWritesEnqueueNothing: a config, thumbnail or delete
// write leaves name, description and section text alone, so the stored vector
// stays valid and no job is owed.
func TestCollectionNonIndexedWritesEnqueueNothing(t *testing.T) {
	tests := []struct {
		name   string
		expect func(m sqlmock.Sqlmock)
		call   func(s portaldomain.CollectionStore) error
	}{
		{
			name: "update config",
			expect: func(m sqlmock.Sqlmock) {
				m.ExpectExec("UPDATE portal_collections SET config").WillReturnResult(sqlmock.NewResult(0, 1))
			},
			call: func(s portaldomain.CollectionStore) error {
				return s.UpdateConfig(context.Background(), "c1", portaldomain.CollectionConfig{})
			},
		},
		{
			name: "update thumbnail",
			expect: func(m sqlmock.Sqlmock) {
				m.ExpectExec("UPDATE portal_collections SET thumbnail_s3_key").WillReturnResult(sqlmock.NewResult(0, 1))
			},
			call: func(s portaldomain.CollectionStore) error {
				return s.UpdateThumbnail(context.Background(), "c1", "thumbs/c1.png")
			},
		},
		{
			name: "soft delete",
			expect: func(m sqlmock.Sqlmock) {
				m.ExpectExec("UPDATE portal_collections SET deleted_at").WillReturnResult(sqlmock.NewResult(0, 1))
			},
			call: func(s portaldomain.CollectionStore) error {
				return s.SoftDelete(context.Background(), "c1")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close() //nolint:errcheck // test cleanup
			store, enq := indexedCollectionStore(db)

			tc.expect(mock)
			require.NoError(t, tc.call(store))
			assert.Empty(t, enq.keys)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
