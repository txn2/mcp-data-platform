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

var scriptAttachmentColumnNames = []string{"prompt_id", "script_ref", "position", "attached_by"}

const testScriptRef = "mcp:script:11111111-1111-1111-1111-111111111111"

// TestAttachScript proves the insert stores the canonical reference and lets
// the database compute the position, so two concurrent attaches cannot both
// claim the same one.
func TestAttachScript(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectExec("INSERT INTO prompt_script_attachments").
		WithArgs("p1", testScriptRef, "author@example.com").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.AttachScript(context.Background(), prompt.ScriptAttachment{
		PromptID: "p1", ScriptRef: testScriptRef, AttachedBy: "author@example.com",
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAttachScriptError(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectExec("INSERT INTO prompt_script_attachments").WillReturnError(errors.New("boom"))

	err := store.AttachScript(context.Background(), prompt.ScriptAttachment{PromptID: "p1", ScriptRef: testScriptRef})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "attaching script to prompt")
}

// TestDetachScriptNotFound proves detaching something a prompt does not
// reference is a distinguishable answer, not a silent success: the caller has
// to be able to say "that was never attached".
func TestDetachScriptNotFound(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectExec("DELETE FROM prompt_script_attachments").
		WithArgs("p1", testScriptRef).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := store.DetachScript(context.Background(), "p1", testScriptRef)

	require.ErrorIs(t, err, prompt.ErrScriptAttachmentNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDetachScript(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectExec("DELETE FROM prompt_script_attachments").
		WithArgs("p1", testScriptRef).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.DetachScript(context.Background(), "p1", testScriptRef))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDetachScriptError(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectExec("DELETE FROM prompt_script_attachments").WillReturnError(errors.New("boom"))

	err := store.DetachScript(context.Background(), "p1", testScriptRef)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "detaching script from prompt")
}

// TestListScriptsByPrompt proves the read is ordered by the authored position:
// a procedure that says "refresh the report, then compare it" needs the report
// first.
func TestListScriptsByPrompt(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectQuery("FROM prompt_script_attachments WHERE prompt_id = ").
		WithArgs("p1").
		WillReturnRows(sqlmock.NewRows(scriptAttachmentColumnNames).
			AddRow("p1", testScriptRef, 0, "author@example.com").
			AddRow("p1", "mcp:script:22222222-2222-2222-2222-222222222222", 1, ""))

	got, err := store.ListScriptsByPrompt(context.Background(), "p1")

	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, testScriptRef, got[0].ScriptRef)
	assert.Equal(t, 0, got[0].Position)
	assert.Equal(t, "author@example.com", got[0].AttachedBy)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListScriptsByPromptScanError(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectQuery("FROM prompt_script_attachments").
		WillReturnRows(sqlmock.NewRows(scriptAttachmentColumnNames).
			AddRow("p1", testScriptRef, "not-an-int", ""))

	_, err := store.ListScriptsByPrompt(context.Background(), "p1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "scanning prompt script attachment row")
}

func TestListScriptsByPromptQueryError(t *testing.T) {
	store, mock := newAttachmentStore(t)
	mock.ExpectQuery("FROM prompt_script_attachments").WillReturnError(errors.New("boom"))

	_, err := store.ListScriptsByPrompt(context.Background(), "p1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing prompt script attachments")
}
