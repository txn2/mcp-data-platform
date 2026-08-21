package scriptstore

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptindex"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// recordingEnqueuer stands in for the index-job store the queue binds. It is
// mutex-guarded because NotifyWrite runs on a context detached from the
// caller's, which the race detector notices even though the call is synchronous.
type recordingEnqueuer struct {
	mu   sync.Mutex
	keys []indexjobs.Key
	err  error
}

func (r *recordingEnqueuer) Enqueue(_ context.Context, key indexjobs.Key, _ indexjobs.Trigger) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = append(r.keys, key)
	return true, r.err
}

func (r *recordingEnqueuer) enqueued() []indexjobs.Key {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]indexjobs.Key{}, r.keys...)
}

// newIndexedMock returns a store whose write path notifies a bound producer,
// plus the sqlmock controller and the enqueuer the producer writes to.
func newIndexedMock(t *testing.T) (*Store, sqlmock.Sqlmock, *recordingEnqueuer) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	enq := &recordingEnqueuer{}
	producer := indexjobs.NewProducer(scriptindex.SourceKind)
	producer.Bind(enq)
	return New(db, indexjobs.WithProducer(producer)), mock, enq
}

// TestCreateEnqueuesItsOwnIndexJob is the latency property the write-path
// producer exists for: a script written now is rankable in about the time one
// embed takes, rather than on the reconciler's next sweep.
func TestCreateEnqueuesItsOwnIndexJob(t *testing.T) {
	s, mock, enq := newIndexedMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO scripts")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow("script_1", rowTime, rowTime))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_versions")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, s.Create(context.Background(),
		&script.Script{Name: "daily"}, testAuthor))

	assert.Equal(t, []indexjobs.Key{{SourceKind: "scripts", SourceID: "script_1"}}, enq.enqueued())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateThatFailedEnqueuesNothing proves the enqueue follows the commit: a
// rolled-back write must not leave a job pointing at a row that does not exist.
func TestCreateThatFailedEnqueuesNothing(t *testing.T) {
	s, mock, enq := newIndexedMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO scripts")).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()

	require.Error(t, s.Create(context.Background(),
		&script.Script{Name: "daily"}, testAuthor))

	assert.Empty(t, enq.enqueued())
}

// TestUpdateEnqueuesOnlyWhenTheIndexedTextMoved is the other half: a write that
// left the description card alone (a source edit, a scope change) must not
// re-embed a corpus that did not change. The answer comes from the write's own
// RETURNING clause, so the two definitions of "changed" cannot drift.
func TestUpdateEnqueuesOnlyWhenTheIndexedTextMoved(t *testing.T) {
	tests := []struct {
		name        string
		textChanged bool
		wantJobs    int
	}{
		{name: "a re-described script re-enters the queue", textChanged: true, wantJobs: 1},
		{name: "a source-only edit does not", textChanged: false, wantJobs: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, mock, enq := newIndexedMock(t)
			mock.ExpectBegin()
			expectLiveRowUpdate(mock, tc.textChanged)
			mock.ExpectCommit()

			require.NoError(t, s.Update(context.Background(), &script.Script{ID: "script_1"}))

			assert.Len(t, enq.enqueued(), tc.wantJobs)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestWritesSurviveAnUnwiredQueue is the no-database / no-worker shape: the
// store is built without a producer and every write path must still commit,
// leaving the row for the reconciler.
func TestWritesSurviveAnUnwiredQueue(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectLiveRowUpdate(mock, true)
	mock.ExpectCommit()

	require.NoError(t, s.Update(context.Background(), &script.Script{ID: "script_1"}))
	require.NoError(t, mock.ExpectationsWereMet())
}
