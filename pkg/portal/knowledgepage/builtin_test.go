package knowledgepage

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lockRows is the projection lockLiveBySlug scans.
func lockRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "builtin", "title", "summary", "body", "tags", "current_version",
	})
}

func builtinStore(t *testing.T) (*postgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() }) //nolint:errcheck // test cleanup
	return &postgresStore{db: db}, mock
}

// shipped carries two tags on purpose: jsonb re-serializes a stored
// ["a","b"] as ["a", "b"], so the mock rows below quote jsonb's own spacing
// and pin the compare to decoded values rather than JSON text.
var shipped = BuiltinPage{
	Slug: "platform-topic", Title: "Topic", Summary: "sum", Body: "body",
	Tags: []string{"scripts", "starlark"},
}

// expectPrune is the sweep every reconcile ends with; nothing pruned here.
func expectPrune(mock sqlmock.Sqlmock) {
	mock.ExpectExec("UPDATE portal_knowledge_pages SET deleted_at").
		WillReturnResult(sqlmock.NewResult(0, 0))
}

func TestReconcileBuiltins_CreatesAMissingPage(t *testing.T) {
	store, mock := builtinStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, builtin, title").WithArgs("platform-topic").
		WillReturnRows(lockRows()) // no live row
	mock.ExpectQuery("SELECT EXISTS").WithArgs("platform-topic").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false)) // no tombstone
	mock.ExpectExec("INSERT INTO portal_knowledge_pages").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO portal_knowledge_page_versions").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	// The written body owes the inline-ref derivation (no refs in this body,
	// so the replace clears the inline set and inserts nothing).
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM knowledge_page_entity_refs").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	expectPrune(mock)

	stats, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
	require.NoError(t, err)
	assert.Equal(t, BuiltinReconcileStats{Created: 1}, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The operator hid the page (soft-deleted the builtin row): the reconcile must
// not resurrect it — this is the suppression contract of #1390.
func TestReconcileBuiltins_RespectsAHiddenPage(t *testing.T) {
	store, mock := builtinStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, builtin, title").WithArgs("platform-topic").
		WillReturnRows(lockRows())
	mock.ExpectQuery("SELECT EXISTS").WithArgs("platform-topic").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true)) // tombstone
	mock.ExpectCommit()
	expectPrune(mock)

	stats, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
	require.NoError(t, err)
	assert.Equal(t, BuiltinReconcileStats{Skipped: 1}, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A live NON-builtin page holds the slug: the operator superseded the topic
// with their own page, and theirs wins.
func TestReconcileBuiltins_LeavesAnOperatorPageAlone(t *testing.T) {
	store, mock := builtinStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, builtin, title").WithArgs("platform-topic").
		WillReturnRows(lockRows().AddRow("kp1", false, "Theirs", "", "their body", []byte(`[]`), 3))
	mock.ExpectCommit()
	expectPrune(mock)

	stats, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
	require.NoError(t, err)
	assert.Equal(t, BuiltinReconcileStats{Skipped: 1}, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}

// An unchanged release touches nothing: no UPDATE, no version row, no
// invalidation.
func TestReconcileBuiltins_UnchangedContentTouchesNothing(t *testing.T) {
	store, mock := builtinStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, builtin, title").WithArgs("platform-topic").
		WillReturnRows(lockRows().AddRow("kp1", true, "Topic", "sum", "body", []byte(`["scripts", "starlark"]`), 1))
	mock.ExpectCommit()
	expectPrune(mock)

	stats, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
	require.NoError(t, err)
	assert.Equal(t, BuiltinReconcileStats{Skipped: 1}, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A changed page is rewritten through the same invalidation an ordinary edit
// takes: the row update clears the index marker, the chunks are dropped in the
// same transaction, and a version row records the release change.
func TestReconcileBuiltins_UpdatesAChangedPage(t *testing.T) {
	store, mock := builtinStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, builtin, title").WithArgs("platform-topic").
		WillReturnRows(lockRows().AddRow("kp1", true, "Topic", "sum", "old body", []byte(`["scripts", "starlark"]`), 1))
	mock.ExpectExec("UPDATE portal_knowledge_pages").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM portal_knowledge_page_embedding_chunks").WithArgs("kp1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO portal_knowledge_page_versions").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	// The written body owes the inline-ref derivation (no refs in this body,
	// so the replace clears the inline set and inserts nothing).
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM knowledge_page_entity_refs").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	expectPrune(mock)

	stats, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
	require.NoError(t, err)
	assert.Equal(t, BuiltinReconcileStats{Updated: 1}, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Another replica's identical reconcile won the insert race: the ON CONFLICT
// insert affects zero rows, and this pass records nothing rather than failing.
func TestReconcileBuiltins_InsertRaceIsBenign(t *testing.T) {
	store, mock := builtinStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, builtin, title").WithArgs("platform-topic").
		WillReturnRows(lockRows())
	mock.ExpectQuery("SELECT EXISTS").WithArgs("platform-topic").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec("INSERT INTO portal_knowledge_pages").
		WillReturnResult(sqlmock.NewResult(0, 0)) // conflict: DO NOTHING
	mock.ExpectCommit()
	expectPrune(mock)

	stats, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
	require.NoError(t, err)
	assert.Equal(t, BuiltinReconcileStats{Skipped: 1}, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A slug that left the shipped set is pruned (soft-deleted), and a page-level
// failure earlier in the pass does not stop the sweep.
func TestReconcileBuiltins_PrunesAndJoinsErrors(t *testing.T) {
	store, mock := builtinStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, builtin, title").WithArgs("platform-topic").
		WillReturnError(errBoom)
	mock.ExpectRollback()
	mock.ExpectExec("UPDATE portal_knowledge_pages SET deleted_at").
		WillReturnResult(sqlmock.NewResult(0, 2))

	stats, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
	require.ErrorIs(t, err, errBoom)
	assert.Equal(t, 2, stats.Pruned)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Update refuses a builtin page under the row lock: the platform's own
// documentation is read-only where people edit (#1390).
func TestUpdate_RefusesABuiltinPage(t *testing.T) {
	store, mock := builtinStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT title, summary, body, tags, current_version, builtin").WithArgs("kp1").
		WillReturnRows(sqlmock.NewRows([]string{"title", "summary", "body", "tags", "current_version", "builtin"}).
			AddRow("Topic", "sum", "body", []byte(`[]`), 1, true))
	mock.ExpectRollback()

	title := "New"
	err := store.Update(context.Background(), "kp1", Update{Title: &title, UpdatedBy: "alice@example.com"})
	require.ErrorIs(t, err, ErrBuiltinReadOnly)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Error paths: each write step's failure surfaces with the page's slug and
// leaves the pass running (errors are joined, not short-circuited).
func TestReconcileBuiltins_WriteFailuresSurface(t *testing.T) {
	t.Run("begin fails", func(t *testing.T) {
		store, mock := builtinStore(t)
		mock.ExpectBegin().WillReturnError(errBoom)
		expectPrune(mock)
		_, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
		require.ErrorIs(t, err, errBoom)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("hidden probe fails", func(t *testing.T) {
		store, mock := builtinStore(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id, builtin, title").WillReturnRows(lockRows())
		mock.ExpectQuery("SELECT EXISTS").WillReturnError(errBoom)
		mock.ExpectRollback()
		expectPrune(mock)
		_, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
		require.ErrorIs(t, err, errBoom)
	})

	t.Run("insert fails", func(t *testing.T) {
		store, mock := builtinStore(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id, builtin, title").WillReturnRows(lockRows())
		mock.ExpectQuery("SELECT EXISTS").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectExec("INSERT INTO portal_knowledge_pages").WillReturnError(errBoom)
		mock.ExpectRollback()
		expectPrune(mock)
		_, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
		require.ErrorIs(t, err, errBoom)
	})

	t.Run("insert version-row fails", func(t *testing.T) {
		store, mock := builtinStore(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id, builtin, title").WillReturnRows(lockRows())
		mock.ExpectQuery("SELECT EXISTS").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectExec("INSERT INTO portal_knowledge_pages").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO portal_knowledge_page_versions").WillReturnError(errBoom)
		mock.ExpectRollback()
		expectPrune(mock)
		_, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
		require.ErrorIs(t, err, errBoom)
	})

	t.Run("update fails", func(t *testing.T) {
		store, mock := builtinStore(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id, builtin, title").
			WillReturnRows(lockRows().AddRow("kp1", true, "Topic", "sum", "old", []byte(`["scripts", "starlark"]`), 1))
		mock.ExpectExec("UPDATE portal_knowledge_pages").WillReturnError(errBoom)
		mock.ExpectRollback()
		expectPrune(mock)
		_, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
		require.ErrorIs(t, err, errBoom)
	})

	t.Run("update version-row fails", func(t *testing.T) {
		store, mock := builtinStore(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id, builtin, title").
			WillReturnRows(lockRows().AddRow("kp1", true, "Topic", "sum", "old", []byte(`["scripts", "starlark"]`), 1))
		mock.ExpectExec("UPDATE portal_knowledge_pages").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("DELETE FROM portal_knowledge_page_embedding_chunks").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO portal_knowledge_page_versions").WillReturnError(errBoom)
		mock.ExpectRollback()
		expectPrune(mock)
		_, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
		require.ErrorIs(t, err, errBoom)
	})

	t.Run("commit fails", func(t *testing.T) {
		store, mock := builtinStore(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id, builtin, title").
			WillReturnRows(lockRows().AddRow("kp1", false, "Theirs", "", "b", []byte(`[]`), 1))
		mock.ExpectCommit().WillReturnError(errBoom)
		expectPrune(mock)
		_, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
		require.ErrorIs(t, err, errBoom)
	})

	t.Run("prune fails", func(t *testing.T) {
		store, mock := builtinStore(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id, builtin, title").
			WillReturnRows(lockRows().AddRow("kp1", false, "Theirs", "", "b", []byte(`[]`), 1))
		mock.ExpectCommit()
		mock.ExpectExec("UPDATE portal_knowledge_pages SET deleted_at").WillReturnError(errBoom)
		_, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
		require.ErrorIs(t, err, errBoom)
	})
}

// A release that reworded only the summary re-versions the page but owes no
// re-embed: the indexed text (title/body/tags) did not move, so the chunks
// stay, the index marker stays, and no index job is enqueued.
func TestReconcileBuiltins_SummaryOnlyChangeSkipsReembed(t *testing.T) {
	store, mock := builtinStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, builtin, title").WithArgs("platform-topic").
		WillReturnRows(lockRows().AddRow("kp1", true, "Topic", "old summary", "body", []byte(`["scripts", "starlark"]`), 1))
	mock.ExpectExec("UPDATE portal_knowledge_pages").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// No DELETE FROM portal_knowledge_page_embedding_chunks: the chunks stand.
	mock.ExpectExec("INSERT INTO portal_knowledge_page_versions").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM knowledge_page_entity_refs").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	expectPrune(mock)

	stats, err := store.ReconcileBuiltins(context.Background(), []BuiltinPage{shipped})
	require.NoError(t, err)
	assert.Equal(t, BuiltinReconcileStats{Updated: 1}, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The prune tombstone releases its slug (SET slug = NULL): that is what
// distinguishes a retired page (resurrected when re-shipped) from an operator
// hide (respected forever). The regex pins the slug release into the sweep.
func TestPruneBuiltinPages_ReleasesTheSlug(t *testing.T) {
	store, mock := builtinStore(t)
	mock.ExpectExec("UPDATE portal_knowledge_pages SET deleted_at = NOW\\(\\), updated_at = NOW\\(\\), slug = NULL").
		WillReturnResult(sqlmock.NewResult(0, 1))
	n, err := store.pruneBuiltinPages(context.Background(), []string{"platform-kept"})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NoError(t, mock.ExpectationsWereMet())
}

// RestoreHidden un-hides operator-hidden builtin tombstones, skipping any
// whose slug a live page has since taken (the NOT EXISTS arm is in the SQL;
// its behavior is proven against real Postgres in the RealDB lifecycle test).
func TestRestoreHidden(t *testing.T) {
	store, mock := builtinStore(t)
	mock.ExpectExec("UPDATE portal_knowledge_pages t SET deleted_at = NULL").
		WillReturnResult(sqlmock.NewResult(0, 2))
	n, err := store.RestoreHidden(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	require.NoError(t, mock.ExpectationsWereMet())

	store2, mock2 := builtinStore(t)
	mock2.ExpectExec("UPDATE portal_knowledge_pages t SET deleted_at = NULL").
		WillReturnError(errBoom)
	_, err = store2.RestoreHidden(context.Background())
	require.ErrorIs(t, err, errBoom)
}

// A body that cites an entity in prose lands the citation in the reference
// graph; a probe failure while filtering surfaces instead of half-writing.
func TestReplaceBuiltinInlineRefs(t *testing.T) {
	body := "See mcp:knowledge_page:kp2 for the definitions."

	store, mock := builtinStore(t)
	mock.ExpectQuery("SELECT 1 FROM portal_knowledge_pages").WithArgs("kp2").
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM knowledge_page_entity_refs").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO knowledge_page_entity_refs").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, store.replaceBuiltinInlineRefs(context.Background(), "kp1", body))
	require.NoError(t, mock.ExpectationsWereMet())

	store2, mock2 := builtinStore(t)
	mock2.ExpectQuery("SELECT 1 FROM portal_knowledge_pages").WillReturnError(errBoom)
	require.ErrorIs(t, store2.replaceBuiltinInlineRefs(context.Background(), "kp1", body), errBoom)
}
