package postgres

import (
	"context"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

var attachmentColumnNames = []string{"prompt_id", "resource_id", "position", "attached_by"}

// newAttachmentStore builds a Store over a sqlmock connection.
func newAttachmentStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return New(db), mock
}

func TestAttach(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectExec("INSERT INTO prompt_resource_attachments").
		WithArgs("p1", "r1", "author@example.com").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.Attach(context.Background(), prompt.Attachment{
		PromptID: "p1", ResourceID: "r1", AttachedBy: "author@example.com",
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAttachError(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectExec("INSERT INTO prompt_resource_attachments").
		WillReturnError(errors.New("db down"))

	err := store.Attach(context.Background(), prompt.Attachment{PromptID: "p1", ResourceID: "r1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "attaching resource to prompt")
}

// TestDetachMissingLinkIsNotFound proves a delete that matched nothing is
// reported as ErrAttachmentNotFound rather than as success, so the REST layer
// can answer 404 instead of pretending it removed something.
func TestDetachMissingLinkIsNotFound(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectExec("DELETE FROM prompt_resource_attachments").
		WithArgs("p1", "nope").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := store.Detach(context.Background(), "p1", "nope")
	assert.ErrorIs(t, err, prompt.ErrAttachmentNotFound)
}

func TestDetachSuccess(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectExec("DELETE FROM prompt_resource_attachments").
		WithArgs("p1", "r1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.Detach(context.Background(), "p1", "r1"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDetachErrors(t *testing.T) {
	t.Run("exec fails", func(t *testing.T) {
		store, mock := newAttachmentStore(t)
		mock.ExpectExec("DELETE FROM prompt_resource_attachments").WillReturnError(errors.New("db down"))
		require.Error(t, store.Detach(context.Background(), "p1", "r1"))
	})

	t.Run("rows-affected fails", func(t *testing.T) {
		store, mock := newAttachmentStore(t)
		mock.ExpectExec("DELETE FROM prompt_resource_attachments").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("no rows info")))
		require.Error(t, store.Detach(context.Background(), "p1", "r1"))
	})
}

func TestListByPrompt(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectQuery("SELECT .+ FROM prompt_resource_attachments WHERE prompt_id = ").
		WithArgs("p1").
		WillReturnRows(sqlmock.NewRows(attachmentColumnNames).
			AddRow("p1", "tpl", 0, "author@example.com").
			AddRow("p1", "logo", 1, ""))

	got, err := store.ListByPrompt(context.Background(), "p1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "tpl", got[0].ResourceID)
	assert.Equal(t, 0, got[0].Position)
	assert.Equal(t, "author@example.com", got[0].AttachedBy)
	assert.Equal(t, "logo", got[1].ResourceID)
}

func TestListByPromptErrors(t *testing.T) {
	t.Run("query fails", func(t *testing.T) {
		store, mock := newAttachmentStore(t)
		mock.ExpectQuery("SELECT .+ FROM prompt_resource_attachments").WillReturnError(errors.New("db down"))
		_, err := store.ListByPrompt(context.Background(), "p1")
		require.Error(t, err)
	})

	t.Run("scan fails on a malformed row", func(t *testing.T) {
		store, mock := newAttachmentStore(t)
		mock.ExpectQuery("SELECT .+ FROM prompt_resource_attachments").
			WillReturnRows(sqlmock.NewRows(attachmentColumnNames).AddRow("p1", "r1", "not-an-int", ""))
		_, err := store.ListByPrompt(context.Background(), "p1")
		require.Error(t, err)
	})

	t.Run("row iteration error surfaces", func(t *testing.T) {
		store, mock := newAttachmentStore(t)
		mock.ExpectQuery("SELECT .+ FROM prompt_resource_attachments").
			WillReturnRows(sqlmock.NewRows(attachmentColumnNames).
				AddRow("p1", "r1", 0, "").RowError(0, errors.New("mid-scan failure")))
		_, err := store.ListByPrompt(context.Background(), "p1")
		require.Error(t, err)
	})
}

func TestListByResource(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectQuery("SELECT prompt_id FROM prompt_resource_attachments").
		WithArgs("tpl").
		WillReturnRows(sqlmock.NewRows([]string{"prompt_id"}).AddRow("p1").AddRow("p2"))

	got, err := store.ListByResource(context.Background(), "tpl")
	require.NoError(t, err)
	assert.Equal(t, []string{"p1", "p2"}, got)
}

func TestListByResourceErrors(t *testing.T) {
	t.Run("query fails", func(t *testing.T) {
		store, mock := newAttachmentStore(t)
		mock.ExpectQuery("SELECT prompt_id FROM prompt_resource_attachments").WillReturnError(errors.New("db down"))
		_, err := store.ListByResource(context.Background(), "tpl")
		require.Error(t, err)
	})

	t.Run("scan fails", func(t *testing.T) {
		store, mock := newAttachmentStore(t)
		mock.ExpectQuery("SELECT prompt_id FROM prompt_resource_attachments").
			WillReturnRows(sqlmock.NewRows([]string{"prompt_id"}).AddRow(nil))
		_, err := store.ListByResource(context.Background(), "tpl")
		require.Error(t, err)
	})

	t.Run("row iteration error surfaces", func(t *testing.T) {
		store, mock := newAttachmentStore(t)
		mock.ExpectQuery("SELECT prompt_id FROM prompt_resource_attachments").
			WillReturnRows(sqlmock.NewRows([]string{"prompt_id"}).AddRow("p1").RowError(0, errors.New("boom")))
		_, err := store.ListByResource(context.Background(), "tpl")
		require.Error(t, err)
	})
}

// expectReorderRead mocks the in-transaction read of the prompt's current
// attachments that Reorder validates against.
func expectReorderRead(mock sqlmock.Sqlmock, rows *sqlmock.Rows) {
	mock.ExpectQuery("SELECT resource_id, attached_by FROM prompt_resource_attachments").
		WithArgs("p1").WillReturnRows(rows)
}

// TestReorderRewritesPositions proves the new order is written as consecutive
// positions and that the original attributor survives the rewrite.
func TestReorderRewritesPositions(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectBegin()
	expectReorderRead(mock, sqlmock.NewRows([]string{"resource_id", "attached_by"}).
		AddRow("tpl", "author@example.com").AddRow("logo", "other@example.com"))
	mock.ExpectExec("DELETE FROM prompt_resource_attachments").WithArgs("p1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("INSERT INTO prompt_resource_attachments").
		WithArgs("p1", "logo", 0, "other@example.com").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO prompt_resource_attachments").
		WithArgs("p1", "tpl", 1, "author@example.com").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, store.Reorder(context.Background(), "p1", []string{"logo", "tpl"}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReorderRejectsUnattachedResource proves reorder cannot be used as a back
// door to attach: an id that is not already linked is refused, so the scope
// check attach performs cannot be skipped.
func TestReorderRejectsUnattachedResource(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectBegin()
	expectReorderRead(mock, sqlmock.NewRows([]string{"resource_id", "attached_by"}).AddRow("tpl", ""))
	mock.ExpectRollback()

	err := store.Reorder(context.Background(), "p1", []string{"tpl", "smuggled"})
	require.Error(t, err)
	assert.ErrorIs(t, err, prompt.ErrAttachmentNotFound)
	assert.Contains(t, err.Error(), "smuggled")
}

// TestReorderOmissionDetaches documents the contract: an id left out of the
// list is removed, which is what lets the editor save a reordered-and-pruned
// list in one call.
func TestReorderOmissionDetaches(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectBegin()
	expectReorderRead(mock, sqlmock.NewRows([]string{"resource_id", "attached_by"}).
		AddRow("tpl", "").AddRow("logo", ""))
	mock.ExpectExec("DELETE FROM prompt_resource_attachments").WithArgs("p1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("INSERT INTO prompt_resource_attachments").
		WithArgs("p1", "tpl", 0, "").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, store.Reorder(context.Background(), "p1", []string{"tpl"}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReorderErrors(t *testing.T) {
	t.Run("begin fails", func(t *testing.T) {
		store, mock := newAttachmentStore(t)
		mock.ExpectBegin().WillReturnError(errors.New("no tx"))
		require.Error(t, store.Reorder(context.Background(), "p1", nil))
	})

	t.Run("current-attachment read fails", func(t *testing.T) {
		store, mock := newAttachmentStore(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT resource_id, attached_by").WillReturnError(errors.New("db down"))
		mock.ExpectRollback()
		require.Error(t, store.Reorder(context.Background(), "p1", nil))
	})

	t.Run("current-attachment scan fails", func(t *testing.T) {
		store, mock := newAttachmentStore(t)
		mock.ExpectBegin()
		expectReorderRead(mock, sqlmock.NewRows([]string{"resource_id", "attached_by"}).AddRow(nil, nil))
		mock.ExpectRollback()
		require.Error(t, store.Reorder(context.Background(), "p1", nil))
	})

	t.Run("current-attachment iteration fails", func(t *testing.T) {
		store, mock := newAttachmentStore(t)
		mock.ExpectBegin()
		expectReorderRead(mock, sqlmock.NewRows([]string{"resource_id", "attached_by"}).
			AddRow("tpl", "").RowError(0, errors.New("boom")))
		mock.ExpectRollback()
		require.Error(t, store.Reorder(context.Background(), "p1", nil))
	})

	t.Run("delete fails", func(t *testing.T) {
		store, mock := newAttachmentStore(t)
		mock.ExpectBegin()
		expectReorderRead(mock, sqlmock.NewRows([]string{"resource_id", "attached_by"}).AddRow("tpl", ""))
		mock.ExpectExec("DELETE FROM prompt_resource_attachments").WillReturnError(errors.New("db down"))
		mock.ExpectRollback()
		require.Error(t, store.Reorder(context.Background(), "p1", []string{"tpl"}))
	})

	t.Run("insert fails", func(t *testing.T) {
		store, mock := newAttachmentStore(t)
		mock.ExpectBegin()
		expectReorderRead(mock, sqlmock.NewRows([]string{"resource_id", "attached_by"}).AddRow("tpl", ""))
		mock.ExpectExec("DELETE FROM prompt_resource_attachments").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO prompt_resource_attachments").WillReturnError(errors.New("db down"))
		mock.ExpectRollback()
		require.Error(t, store.Reorder(context.Background(), "p1", []string{"tpl"}))
	})

	t.Run("commit fails", func(t *testing.T) {
		store, mock := newAttachmentStore(t)
		mock.ExpectBegin()
		expectReorderRead(mock, sqlmock.NewRows([]string{"resource_id", "attached_by"}).AddRow("tpl", ""))
		mock.ExpectExec("DELETE FROM prompt_resource_attachments").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO prompt_resource_attachments").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit().WillReturnError(errors.New("commit failed"))
		require.Error(t, store.Reorder(context.Background(), "p1", []string{"tpl"}))
	})
}

// TestStoreSatisfiesAttachmentStore is the compile-time contract restated as a
// test so a signature drift names itself.
func TestStoreSatisfiesAttachmentStore(t *testing.T) {
	var _ prompt.AttachmentStore = (*Store)(nil)
	store, _ := newAttachmentStore(t)
	assert.Implements(t, (*prompt.AttachmentStore)(nil), store)
}
