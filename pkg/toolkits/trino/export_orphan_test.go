package trino

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"
)

// racingAssetStore misses the key before the upload and finds the winner's
// asset after the insert loses, which is what a concurrent pair of keyed
// exports sees.
type racingAssetStore struct {
	mockExportAssetStore
	lookups int
}

func (s *racingAssetStore) GetByIdempotencyKey(_ context.Context, _, _ string) (*ExportAssetRef, error) {
	s.lookups++
	if s.lookups == 1 {
		return nil, errors.New("not found")
	}
	return &ExportAssetRef{ID: "winner"}, nil
}

// exportWithStores runs one trino_export end to end against the given stores
// and returns the storage fake so the test can see what was uploaded and what
// was deleted.
func exportWithStores(t *testing.T, assets ExportAssetStore, versions ExportVersionStore, uc *ExportUserContext, args map[string]any) (*mockExportS3Client, bool) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

	s3 := &mockExportS3Client{}
	tk := &Toolkit{name: "test", client: trinoclient.NewWithDB(db, trinoclient.Config{Timeout: time.Minute}), config: Config{ReadOnly: true}}
	tk.SetExportDeps(ExportDeps{
		AssetStore: assets, VersionStore: versions, S3Client: s3,
		S3Bucket: "test-bucket", S3Prefix: "exports",
		Config:         ExportConfig{MaxRows: 100, MaxBytes: 1 << 20, DefaultTimeout: time.Minute, MaxTimeout: time.Minute},
		GetUserContext: func(context.Context) *ExportUserContext { return uc },
	})
	result, _ := callExport(t, tk, args)
	return s3, result.IsError
}

func plainUser() *ExportUserContext {
	return &ExportUserContext{UserID: "u1", UserEmail: "a@example.com", SessionID: "s1"}
}

// TestExport_NoUploadIsLeftWithoutARow covers #1903: every path that writes no
// asset or version row naming the upload deletes it, and a path that does
// keeps it.
func TestExport_NoUploadIsLeftWithoutARow(t *testing.T) {
	args := map[string]any{"sql": "SELECT id FROM t", "format": "csv", "name": "out"}

	t.Run("the insert fails", func(t *testing.T) {
		s3, isErr := exportWithStores(t, &mockExportAssetStore{insertErr: errors.New("db down")}, &mockExportVersionStore{}, plainUser(), args)
		assert.True(t, isErr)
		require.NotEmpty(t, s3.lastKey)
		assert.Equal(t, []string{s3.lastKey}, s3.deleted)
	})

	t.Run("the insert loses a keyed race", func(t *testing.T) {
		keyed := map[string]any{"sql": "SELECT id FROM t", "format": "csv", "name": "out", "idempotency_key": "k1"}
		s3, isErr := exportWithStores(t, &racingAssetStore{mockExportAssetStore: mockExportAssetStore{insertErr: errors.New("unique violation")}},
			&mockExportVersionStore{}, plainUser(), keyed)
		assert.False(t, isErr, "the loser answers with the winner's asset")
		assert.Equal(t, []string{s3.lastKey}, s3.deleted)
	})

	t.Run("a run's version on the existing asset fails", func(t *testing.T) {
		s3, isErr := exportWithStores(t, &mockExportAssetStore{idempotencyHit: &ExportAssetRef{ID: "first"}},
			&mockExportVersionStore{createErr: errors.New("db down")}, runUser(), args)
		assert.True(t, isErr)
		assert.Equal(t, []string{s3.lastKey}, s3.deleted)
	})

	t.Run("a run's first asset is inserted and its version fails", func(t *testing.T) {
		s3, isErr := exportWithStores(t, &mockExportAssetStore{}, &mockExportVersionStore{createErr: errors.New("db down")}, runUser(), args)
		assert.True(t, isErr)
		assert.Empty(t, s3.deleted, "the new asset row names the upload")
	})

	t.Run("a stored export", func(t *testing.T) {
		s3, isErr := exportWithStores(t, &mockExportAssetStore{}, &mockExportVersionStore{}, plainUser(), args)
		assert.False(t, isErr)
		assert.Empty(t, s3.deleted)
	})
}

// A delete that fails is logged and does not change the call's outcome; with
// no storage client there is nothing to delete.
func TestDiscardExport(t *testing.T) {
	s3 := &mockExportS3Client{deleteErr: errors.New("denied")}
	discardExport(context.Background(), &ExportDeps{S3Client: s3, S3Bucket: "b"}, "k")
	assert.Equal(t, []string{"k"}, s3.deleted)
	discardExport(context.Background(), &ExportDeps{}, "k")
}
