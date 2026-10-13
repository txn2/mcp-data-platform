package resource

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// indexedResourceStore returns a resource store wired to bound producers for
// the resource and its folder, the arrangement the platform builds in
// production. One enqueuer records both, so a test reads the kind of each job.
func indexedResourceStore(t *testing.T) (Store, sqlmock.Sqlmock, *fakeEnqueuer) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	enq := &fakeEnqueuer{}
	producer := indexjobs.NewProducer("resources")
	producer.Bind(enq)
	folders := indexjobs.NewProducer(FolderIndexKind)
	folders.Bind(enq)
	return NewPostgresStore(db, indexjobs.WithProducer(producer), indexjobs.WithAlsoProducer(folders)), mock, enq
}

func testResource() Resource {
	return Resource{
		ID: "res-1", Scope: ScopeGlobal, Path: "samples", Filename: "test.csv",
		DisplayName: "Test", Description: "A test resource", MIMEType: "text/csv",
		SizeBytes: 100, S3Key: "s3/key", URI: "mcp://global/samples/test.csv",
		Tags: []string{"tag1"}, UploaderSub: "sub-1", UploaderEmail: "user@example.com",
	}
}

// TestResourceInsertEnqueuesIndexJob is the #1256 acceptance for the resources
// kind: an uploaded resource produces one TriggerWrite job for its row.
func TestResourceInsertEnqueuesIndexJob(t *testing.T) {
	store, mock, enq := indexedResourceStore(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO resources").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO resource_folders").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, store.Insert(context.Background(), testResource()))
	require.Len(t, enq.keys, 1)
	assert.Equal(t, indexjobs.Key{SourceKind: "resources", SourceID: "res-1"}, enq.keys[0])
	assert.Equal(t, indexjobs.TriggerWrite, enq.triggers[0])
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestResourceInsertFailureEnqueuesNothing: the enqueue follows the write, so a
// failed insert leaves no job for a row that does not exist.
func TestResourceInsertFailureEnqueuesNothing(t *testing.T) {
	store, mock, enq := indexedResourceStore(t)

	mock.ExpectExec("INSERT INTO resources").WillReturnError(errors.New("boom"))

	require.Error(t, store.Insert(context.Background(), testResource()))
	assert.Empty(t, enq.keys)
}

// TestResourceUpdateEnqueuesIndexJob: every mutable field is part of the indexed
// text, which is why Update clears the vector unconditionally — so it owes a job
// unconditionally too.
func TestResourceUpdateEnqueuesIndexJob(t *testing.T) {
	store, mock, enq := indexedResourceStore(t)

	name := "Renamed"
	mock.ExpectQuery("UPDATE resources SET").WillReturnRows(sqlmock.NewRows([]string{"index_policy", "scope", "scope_id", "path"}).AddRow("full", "global", "", "samples"))

	require.NoError(t, store.Update(context.Background(), "res-1", Update{DisplayName: &name}))
	require.Len(t, enq.keys, 1)
	assert.Equal(t, "res-1", enq.keys[0].SourceID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestResourceUpdateMissingRowEnqueuesNothing: an update that matched no row
// changed no text, so it owes no job.
func TestResourceUpdateMissingRowEnqueuesNothing(t *testing.T) {
	store, mock, enq := indexedResourceStore(t)

	name := "Renamed"
	mock.ExpectQuery("UPDATE resources SET").WillReturnRows(sqlmock.NewRows([]string{"index_policy", "scope", "scope_id", "path"}))

	require.Error(t, store.Update(context.Background(), "gone", Update{DisplayName: &name}))
	assert.Empty(t, enq.keys)
}

// TestResourceAddRevisionEnqueuesIndexJob: a new revision replaces the blob, so
// the row owes both a fresh content extract and a re-embed.
func TestResourceAddRevisionEnqueuesIndexJob(t *testing.T) {
	store, mock, enq := indexedResourceStore(t)
	versions, ok := store.(VersionStore)
	require.True(t, ok)

	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM resources WHERE id = \\$1 FOR UPDATE").
		WithArgs("res-1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("res-1"))
	mock.ExpectQuery("INSERT INTO resource_versions").
		WillReturnRows(sqlmock.NewRows([]string{
			"resource_id", "version", "mime_type", "size_bytes", "s3_key",
			"uploader_sub", "uploader_email", "restored_from", "change_summary", "content_sha256", "metadata", "created_at",
		}).AddRow("res-1", 2, "text/csv", int64(12), "k/v/rev1/f.csv", "sub", "u@example.com", nil, "", nil, []byte(`{}`), now))
	mock.ExpectQuery("UPDATE resources").WillReturnRows(sqlmock.NewRows([]string{"index_policy", "scope", "scope_id", "path"}).AddRow("full", "global", "", "samples"))
	mock.ExpectCommit()

	_, err := versions.AddRevision(context.Background(), Revision{
		ResourceID: "res-1", MIMEType: "text/csv", SizeBytes: 12,
		S3Key: "k/v/rev1/f.csv", UploaderSub: "sub", UploaderEmail: "u@example.com",
	})
	require.NoError(t, err)
	require.Len(t, enq.keys, 1)
	assert.Equal(t, indexjobs.Key{SourceKind: "resources", SourceID: "res-1"}, enq.keys[0])
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestResourceDeleteEnqueuesNothing covers the delete side of the contract: a
// deleted resource queues no unit whose only outcome would be the Source's
// gone-resolution path.
func TestResourceDeleteEnqueuesNothing(t *testing.T) {
	store, mock, enq := indexedResourceStore(t)

	mock.ExpectQuery("DELETE FROM resources").WillReturnRows(sqlmock.NewRows([]string{"index_policy", "scope", "scope_id", "path"}).AddRow("full", "global", "", "samples"))

	require.NoError(t, store.Delete(context.Background(), "res-1"))
	assert.Empty(t, enq.keys)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// folderJob is the job a file that is not indexed itself owes its folder.
var folderJob = indexjobs.Key{SourceKind: FolderIndexKind, SourceID: "global::samples"}

// TestResourceInsertNotIndexedFeedsItsFolder is the #2111 cost contract: a
// file written with index none enqueues no job of its own, only its folder's,
// and the queue coalesces a folder's jobs while one is pending.
func TestResourceInsertNotIndexedFeedsItsFolder(t *testing.T) {
	store, mock, enq := indexedResourceStore(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO resources").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO resource_folders").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	r := testResource()
	r.IndexPolicy = "none"
	require.NoError(t, store.Insert(context.Background(), r))
	assert.Equal(t, []indexjobs.Key{folderJob}, enq.keys)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestResourceUpdateOfPolicyFeedsBoth: a file made searchable again owes its
// own job, and the folder it stops being summarized in owes one too.
func TestResourceUpdateOfPolicyFeedsBoth(t *testing.T) {
	store, mock, enq := indexedResourceStore(t)

	policy := "metadata"
	mock.ExpectQuery("UPDATE resources SET .*index_policy = \\$1, index_policy_explicit = TRUE, content_text = ''").
		WithArgs("metadata", "res-1").
		WillReturnRows(sqlmock.NewRows([]string{"index_policy", "scope", "scope_id", "path"}).AddRow("metadata", "global", "", "samples"))

	require.NoError(t, store.Update(context.Background(), "res-1", Update{Index: &policy}))
	assert.Equal(t, []indexjobs.Key{{SourceKind: "resources", SourceID: "res-1"}, folderJob}, enq.keys)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestResourceDeleteOfUnindexedFileFeedsItsFolder: the folder's summary loses
// a file.
func TestResourceDeleteOfUnindexedFileFeedsItsFolder(t *testing.T) {
	store, mock, enq := indexedResourceStore(t)

	mock.ExpectQuery("DELETE FROM resources").WillReturnRows(
		sqlmock.NewRows([]string{"index_policy", "scope", "scope_id", "path"}).AddRow("none", "global", "", "samples"))

	require.NoError(t, store.Delete(context.Background(), "res-1"))
	assert.Equal(t, []indexjobs.Key{folderJob}, enq.keys)
}

// TestResourceMoveOfUnindexedFileFeedsBothFolders: the folder it left and the
// one it joined each owe a summary; the file owes none.
func TestResourceMoveOfUnindexedFileFeedsBothFolders(t *testing.T) {
	store, mock, enq := indexedResourceStore(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE resources SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO resource_uri_aliases").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM resource_uri_aliases").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO resource_folders").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := store.Move(context.Background(), []Move{{
		ID: "res-1", Scope: ScopeGlobal, Path: "archive", URI: "mcp://global/archive/test.csv",
		FromURI: "mcp://global/samples/test.csv",
		From:    From{Library: ScopeFilter{Scope: ScopeGlobal}, Path: "samples", IndexPolicy: "none"},
	}})
	require.NoError(t, err)
	assert.Equal(t, []indexjobs.Key{folderJob, {SourceKind: FolderIndexKind, SourceID: "global::archive"}}, enq.keys)
}

func TestFolderKeyRoundTrips(t *testing.T) {
	for _, c := range []struct {
		lib  ScopeFilter
		path string
	}{
		{ScopeFilter{Scope: ScopeGlobal}, "pipelines/nltv"},
		{ScopeFilter{Scope: ScopeUser, ScopeID: "a:b/c@example.com"}, "x:y"},
	} {
		lib, path, err := ParseFolderKey(FolderKey(c.lib, c.path))
		require.NoError(t, err)
		assert.Equal(t, c.lib, lib)
		assert.Equal(t, c.path, path)
	}
	for _, bad := range []string{"global", "global:%zz:path", "user:only"} {
		_, _, err := ParseFolderKey(bad)
		assert.Error(t, err, bad)
	}
}
