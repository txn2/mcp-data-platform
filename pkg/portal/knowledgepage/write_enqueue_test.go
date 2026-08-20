package knowledgepage

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// fakeEnqueuer records the index jobs a bound Producer wrote, so a store test
// can assert on the job a page write produced rather than on the notify call.
type fakeEnqueuer struct {
	keys     []indexjobs.Key
	triggers []indexjobs.Trigger
}

func (f *fakeEnqueuer) Enqueue(_ context.Context, key indexjobs.Key, trigger indexjobs.Trigger) (bool, error) {
	f.keys = append(f.keys, key)
	f.triggers = append(f.triggers, trigger)
	return true, nil
}

// indexedStore returns a page store wired to a bound producer, the arrangement
// the platform builds in production.
func indexedStore(t *testing.T, db *sql.DB) (Store, *fakeEnqueuer) {
	t.Helper()
	enq := &fakeEnqueuer{}
	producer := indexjobs.NewProducer("portal-knowledge-pages")
	producer.Bind(enq)
	return NewPostgresStore(db, indexjobs.WithProducer(producer)), enq
}

// TestStoreInsertEnqueuesIndexJob is the #1256 acceptance at the store level:
// creating a page produces one TriggerWrite job for that page, so ranked search
// picks it up in one embed rather than after a reconciler sweep.
func TestStoreInsertEnqueuesIndexJob(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store, enq := indexedStore(t, db)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO portal_knowledge_pages").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO portal_knowledge_page_versions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, store.Insert(context.Background(), Page{
		ID: "kp1", Title: "Fiscal", Body: "body", CreatedBy: "alice@example.com",
	}))

	require.Len(t, enq.keys, 1)
	assert.Equal(t, indexjobs.Key{SourceKind: "portal-knowledge-pages", SourceID: "kp1"}, enq.keys[0])
	assert.Equal(t, indexjobs.TriggerWrite, enq.triggers[0])
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestStoreInsertFailureEnqueuesNothing proves the enqueue follows the commit: a
// page that was never written must not leave a job that resolves to nothing.
func TestStoreInsertFailureEnqueuesNothing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store, enq := indexedStore(t, db)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO portal_knowledge_pages").WillReturnError(errBoom)
	mock.ExpectRollback()

	require.Error(t, store.Insert(context.Background(), Page{ID: "kp1", Title: "T"}))
	assert.Empty(t, enq.keys)
}

// TestStoreUpdateEnqueuesOnlyWhenIndexedTextMoves pins the enqueue to the same
// condition as the embedding invalidation: a title/body/tags edit owes a
// re-embed, a summary-only edit does not (Summary is not part of IndexText).
func TestStoreUpdateEnqueuesOnlyWhenIndexedTextMoves(t *testing.T) {
	newBody, newSummary := "newbody", "news"
	tests := []struct {
		name        string
		update      Update
		dropsChunks bool
		wantJobs    int
	}{
		{
			name:        "body edit",
			update:      Update{Body: &newBody, UpdatedBy: "bob@example.com"},
			dropsChunks: true,
			wantJobs:    1,
		},
		{
			name:     "summary only",
			update:   Update{Summary: &newSummary, UpdatedBy: "bob@example.com"},
			wantJobs: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close() //nolint:errcheck // test cleanup
			store, enq := indexedStore(t, db)

			mock.ExpectBegin()
			mock.ExpectQuery("SELECT title, summary, body, tags, current_version").
				WithArgs("kp1").
				WillReturnRows(sqlmock.NewRows([]string{"title", "summary", "body", "tags", "current_version", "builtin"}).
					AddRow("Old", "olds", "oldbody", []byte(`["a"]`), 1, false))
			mock.ExpectExec("UPDATE portal_knowledge_pages").WillReturnResult(sqlmock.NewResult(0, 1))
			if tc.dropsChunks {
				mock.ExpectExec("DELETE FROM portal_knowledge_page_embedding_chunks").
					WillReturnResult(sqlmock.NewResult(0, 1))
			}
			mock.ExpectExec("INSERT INTO portal_knowledge_page_versions").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			require.NoError(t, store.Update(context.Background(), "kp1", tc.update))
			assert.Len(t, enq.keys, tc.wantJobs)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestStoreSoftDeleteEnqueuesNothing covers the delete side of the contract: a
// page leaving search enqueues no job, so no unit is queued whose only outcome
// would be the Source's clean-completion path.
func TestStoreSoftDeleteEnqueuesNothing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store, enq := indexedStore(t, db)

	mock.ExpectExec("UPDATE portal_knowledge_pages SET deleted_at").
		WithArgs("kp1").WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.SoftDelete(context.Background(), "kp1"))
	assert.Empty(t, enq.keys)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestStoreWithoutProducerWrites covers the no-queue deployment: the same write
// paths run unchanged when no producer was supplied.
func TestStoreWithoutProducerWrites(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO portal_knowledge_pages").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO portal_knowledge_page_versions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, store.Insert(context.Background(), Page{ID: "kp1", Title: "T"}))
	assert.NoError(t, mock.ExpectationsWereMet())
}
