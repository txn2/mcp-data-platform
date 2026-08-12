package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
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

// indexedPromptStore returns a prompt store wired to a bound producer, the
// arrangement the platform builds in production.
func indexedPromptStore(t *testing.T) (*Store, sqlmock.Sqlmock, *fakeEnqueuer) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	enq := &fakeEnqueuer{}
	producer := indexjobs.NewProducer("prompts")
	producer.Bind(enq)
	return New(db, indexjobs.WithProducer(producer)), mock, enq
}

// mustArgsJSON marshals a prompt's arguments the way the store binds them, for
// the locked-row mock the version paths read back.
func mustArgsJSON(t *testing.T, p *prompt.Prompt) []byte {
	t.Helper()
	out, err := json.Marshal(p.Arguments)
	require.NoError(t, err)
	return out
}

// TestCreateEnqueuesIndexJob is the #1256 acceptance for the prompts kind: a
// created prompt produces one TriggerWrite job for its row.
func TestCreateEnqueuesIndexJob(t *testing.T) {
	store, mock, enq := indexedPromptStore(t)
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO prompts").
		WillReturnRows(sqlmock.NewRows([]string{"id", "version", "created_at", "updated_at"}).
			AddRow("uuid-123", 1, now, now))
	mock.ExpectExec("INSERT INTO prompt_versions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, store.Create(context.Background(), newTestPrompt()))

	require.Len(t, enq.keys, 1)
	assert.Equal(t, indexjobs.Key{SourceKind: "prompts", SourceID: "uuid-123"}, enq.keys[0])
	assert.Equal(t, indexjobs.TriggerWrite, enq.triggers[0])
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateFailureEnqueuesNothing proves the enqueue follows the commit: a
// prompt whose transaction rolled back leaves no job behind.
func TestCreateFailureEnqueuesNothing(t *testing.T) {
	store, mock, enq := indexedPromptStore(t)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO prompts").WillReturnError(errors.New("boom"))
	mock.ExpectRollback()

	require.Error(t, store.Create(context.Background(), newTestPrompt()))
	assert.Empty(t, enq.keys)
}

// TestUpdateEnqueuesIndexJob covers the plain update path, which clears the
// embedding when the text hash moves and so owes the row a re-embed.
func TestUpdateEnqueuesIndexJob(t *testing.T) {
	store, mock, enq := indexedPromptStore(t)

	p := newTestPrompt()
	p.ID = "uuid-123"
	p.Status = prompt.StatusDraft
	stored := *p
	stored.Content = "old content"
	argsJSON := mustArgsJSON(t, p)

	mock.ExpectBegin()
	expectLockPrompt(mock, &stored, argsJSON)
	mock.ExpectExec("UPDATE prompts").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, store.Update(context.Background(), p))
	require.Len(t, enq.keys, 1)
	assert.Equal(t, indexjobs.Key{SourceKind: "prompts", SourceID: "uuid-123"}, enq.keys[0])
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdateWithVersionEnqueuesIndexJob covers the versioned edit path: the
// snapshot and the live row move together, so the live row owes a re-embed.
func TestUpdateWithVersionEnqueuesIndexJob(t *testing.T) {
	store, mock, enq := indexedPromptStore(t)

	p := newTestPrompt()
	p.ID = "uuid-123"
	p.Version = 1
	p.Status = prompt.StatusDraft
	stored := *p
	stored.Content = "old content"
	argsJSON := mustArgsJSON(t, p)

	mock.ExpectBegin()
	expectLockPrompt(mock, &stored, argsJSON)
	mock.ExpectQuery("SELECT GREATEST").
		WillReturnRows(sqlmock.NewRows([]string{"next"}).AddRow(2))
	mock.ExpectExec("INSERT INTO prompt_versions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE prompts").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, store.UpdateWithVersion(context.Background(), p, "author@example.com"))
	require.Len(t, enq.keys, 1)
	assert.Equal(t, "uuid-123", enq.keys[0].SourceID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdateWithUnchangedTextEnqueuesNothing pins the enqueue to the same
// predicate as the embedding invalidation. The startup re-ingest of static
// prompts calls Update for every one of them on every boot; a write that moves
// no indexed text keeps its vector, so queuing a job for it would put one row
// per prompt per boot on the admin Indexing dashboard for no work.
func TestUpdateWithUnchangedTextEnqueuesNothing(t *testing.T) {
	store, mock, enq := indexedPromptStore(t)

	p := newTestPrompt()
	p.ID = "uuid-123"
	p.Status = prompt.StatusDraft
	stored := *p // identical indexed text; only metadata differs on the write
	argsJSON := mustArgsJSON(t, p)

	mock.ExpectBegin()
	expectLockPrompt(mock, &stored, argsJSON)
	mock.ExpectExec("UPDATE prompts").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, store.Update(context.Background(), p))
	assert.Empty(t, enq.keys)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestApproveVersionEnqueuesIndexJob covers the approval path, the write that
// makes a reviewed prompt the live one: applying the draft snapshot moves the
// indexed text, so the row owes a re-embed the moment the approval commits.
func TestApproveVersionEnqueuesIndexJob(t *testing.T) {
	store, mock, enq := indexedPromptStore(t)

	live := newTestPrompt()
	live.ID = "uuid-123"
	live.Version = 1
	live.Status = prompt.StatusApproved
	argsJSON := mustArgsJSON(t, live)

	mock.ExpectBegin()
	expectLockPrompt(mock, live, argsJSON)
	draftRows := sqlmock.NewRows(versionSelectColumns)
	draftRows.AddRow(toDriverValues(versionRow("v2", 2, "draft content", prompt.VersionStatusDraft))...)
	mock.ExpectQuery("SELECT .+ FROM prompt_versions WHERE prompt_id = .+ AND version = .+ FOR UPDATE").
		WithArgs("uuid-123", 2).WillReturnRows(draftRows)
	mock.ExpectExec("UPDATE prompts").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE prompt_versions SET status").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE prompt_versions SET status").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	_, err := store.ApproveVersion(context.Background(), "uuid-123", 2, "admin@example.com")
	require.NoError(t, err)
	require.Len(t, enq.keys, 1)
	assert.Equal(t, indexjobs.Key{SourceKind: "prompts", SourceID: "uuid-123"}, enq.keys[0])
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateDraftVersionEnqueuesNothing: a draft leaves the live row untouched,
// and only the live row is indexed, so a draft owes no job.
func TestCreateDraftVersionEnqueuesNothing(t *testing.T) {
	store, mock, enq := indexedPromptStore(t)

	p := newTestPrompt()
	p.ID = "uuid-123"
	argsJSON := mustArgsJSON(t, p)

	mock.ExpectBegin()
	expectLockPrompt(mock, p, argsJSON)
	mock.ExpectQuery("SELECT GREATEST").
		WillReturnRows(sqlmock.NewRows([]string{"next"}).AddRow(2))
	mock.ExpectExec("INSERT INTO prompt_versions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	_, err := store.CreateDraftVersion(context.Background(), "uuid-123", p, "author@example.com")
	require.NoError(t, err)
	assert.Empty(t, enq.keys)
	assert.NoError(t, mock.ExpectationsWereMet())
}
