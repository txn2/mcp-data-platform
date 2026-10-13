package resourcereclass

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/indexpolicy"
	"github.com/txn2/mcp-data-platform/internal/platform/resourceindex"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// recordingQueue records the jobs a bound producer enqueued.
type recordingQueue struct{ keys []indexjobs.Key }

func (q *recordingQueue) Enqueue(_ context.Context, key indexjobs.Key, _ indexjobs.Trigger) (bool, error) {
	q.keys = append(q.keys, key)
	return true, nil
}

var reclassCols = []string{
	"id", "index_policy", "index_policy_explicit", "path", "mime_type",
	"size_bytes", "scope", "scope_id", "uploader_sub", "has_text",
}

// TestReclassifyAppliesTheRulesToWhatNoWriterChose is #2111's reclassification
// at PMGSC's shape: the pipeline backfill under pipelines/** drops out of
// search with its kept text, posters a script wrote stay searchable on their
// metadata, a writer's own choice is left alone, and a table of values kept
// under metadata loses the text it no longer reads.
func TestReclassifyAppliesTheRulesToWhatNoWriterChose(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	resolver, err := indexpolicy.NewResolver(indexpolicy.Config{Rules: []indexpolicy.Rule{
		{Path: "pipelines/**", Index: "none"},
		{Path: "brand/**", MIME: []string{"image/*"}, Index: "metadata"},
	}})
	require.NoError(t, err)
	queue := &recordingQueue{}
	resources, folders := indexjobs.NewProducer(resourceindex.SourceKind), indexjobs.NewProducer(resourceindex.FolderKind)
	resources.Bind(queue)
	folders.Bind(queue)
	r := NewReclassifier(db, resolver, resources, folders)
	r.pause = 0

	mock.ExpectQuery("SELECT pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(true))
	mock.ExpectQuery("SELECT id, index_policy, index_policy_explicit").WithArgs("").
		WillReturnRows(sqlmock.NewRows(reclassCols).
			AddRow("r1", "metadata", false, "pipelines/nltv", "text/csv", 100, "global", "", "script:nltv", true).
			AddRow("r2", "full", false, "brand/posters", "image/png", 100, "global", "", "script:posters", false).
			AddRow("r3", "full", true, "pipelines/nltv", "text/csv", 100, "global", "", "u", true).
			AddRow("r4", "metadata", false, "reports", "text/csv", 100, "global", "", "u", true).
			AddRow("r5", "full", false, "guides", "application/pdf", 100, "global", "", "u", true))
	mock.ExpectExec("UPDATE resources SET index_policy = \\$1").
		WithArgs("none", pq.Array([]string{"r1"})).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE resources SET index_policy = \\$1").
		WithArgs("metadata", pq.Array([]string{"r2"})).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE resources SET content_text = ''").
		WithArgs(pq.Array([]string{"r4"})).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id, index_policy, index_policy_explicit").WithArgs("r5").
		WillReturnRows(sqlmock.NewRows(reclassCols))
	mock.ExpectExec("SELECT pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.MatchExpectationsInOrder(false)

	res, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, ReclassifyResult{Examined: 5, Changed: 2, Cleared: 1}, res)
	assert.ElementsMatch(t, []indexjobs.Key{
		{SourceKind: resourceindex.SourceKind, SourceID: "r2"},
		{SourceKind: resourceindex.FolderKind, SourceID: "global::pipelines/nltv"},
		{SourceKind: resourceindex.FolderKind, SourceID: "global::brand/posters"},
	}, queue.keys, "the posters are re-embedded on their metadata; the archive's folder is summarized")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReclassifyRefusesWhileAnotherRuns(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(false))
	_, err = NewReclassifier(db, indexpolicy.Default(), nil, nil).Run(context.Background())
	assert.ErrorIs(t, err, ErrReclassifyRunning)
}

func TestReclassifyReportsAFailedWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(true))
	mock.ExpectQuery("SELECT id, index_policy").WillReturnRows(sqlmock.NewRows(reclassCols).
		AddRow("r1", "full", false, "x", "text/csv", 1, "global", "", "u", false))
	mock.ExpectExec("UPDATE resources SET index_policy").WillReturnError(errors.New("db down"))
	mock.ExpectExec("SELECT pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 0))
	_, err = NewReclassifier(db, indexpolicy.Default(), nil, nil).Run(context.Background())
	assert.ErrorContains(t, err, "reclassify write")
}

func TestReclassifyStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, sleepCtx(ctx, 1))
	assert.NoError(t, sleepCtx(context.Background(), 0))
}

func TestOriginOfUploader(t *testing.T) {
	assert.Equal(t, indexpolicy.OriginScript, originOfUploader("script:nltv"))
	assert.Equal(t, indexpolicy.OriginWebhook, originOfUploader("system:webhooks"))
	assert.Equal(t, indexpolicy.OriginUpload, originOfUploader("550e8400"))
}

func TestSleepCtx(t *testing.T) {
	assert.NoError(t, sleepCtx(context.Background(), 0))
	assert.NoError(t, sleepCtx(context.Background(), time.Millisecond))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, sleepCtx(canceled, time.Hour), context.Canceled)
	waiting, stop := context.WithCancel(context.Background())
	time.AfterFunc(time.Millisecond, stop)
	assert.ErrorIs(t, sleepCtx(waiting, time.Hour), context.Canceled, "a pause is cut short by its context")
}

// TestReclassifyLeavesAChoiceMadeWhileItRuns: a writer who sets a file's
// policy between the batch read and its write keeps it, and the file is not
// counted as changed.
func TestReclassifyLeavesAChoiceMadeWhileItRuns(t *testing.T) {
	for name, result := range map[string]driver.Result{
		"chosen meanwhile":         sqlmock.NewResult(0, 0),
		"rows affected unreadable": sqlmock.NewErrorResult(errors.New("no count")),
	} {
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			resolver, err := indexpolicy.NewResolver(indexpolicy.Config{Rules: []indexpolicy.Rule{{Path: "pipelines/**", Index: "none"}}})
			require.NoError(t, err)
			r := NewReclassifier(db, resolver, nil, nil)
			r.pause = 0
			mock.ExpectQuery("SELECT pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(true))
			mock.ExpectQuery("SELECT id, index_policy").WillReturnRows(sqlmock.NewRows(reclassCols).
				AddRow("r1", "full", false, "pipelines/a", "text/csv", 1, "global", "", "u", false))
			mock.ExpectExec("UPDATE resources SET index_policy = \\$1(.|\n)*AND NOT index_policy_explicit").WillReturnResult(result)
			mock.ExpectQuery("SELECT id, index_policy").WillReturnRows(sqlmock.NewRows(reclassCols))
			mock.ExpectExec("SELECT pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.MatchExpectationsInOrder(false)
			res, err := r.Run(context.Background())
			if name == "chosen meanwhile" {
				require.NoError(t, err)
				assert.Equal(t, 0, res.Changed)
				return
			}
			assert.ErrorContains(t, err, "reclassify write")
		})
	}
}
