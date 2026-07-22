package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

var versionSelectColumns = []string{
	"id", "prompt_id", "version", "display_name", "description", "content",
	"arguments", "tags", "author", "status", "approved_by", "approved_at", "created_at",
}

// versionRow returns a full prompt_versions row in versionColumns order.
func versionRow(id string, version int, content, status string) []any {
	const promptID = "uuid-123"
	return []any{
		id, promptID, version, "Test Prompt", "A test prompt", content,
		[]byte(`[]`), pq.Array([]string{}), "author@example.com", status, "", nil, testRowTime,
	}
}

// expectLockPrompt mocks the FOR UPDATE read of the full prompt row.
func expectLockPrompt(mock sqlmock.Sqlmock, p *prompt.Prompt, argsJSON []byte) {
	mock.ExpectQuery("SELECT .+ FROM prompts WHERE id = .+ FOR UPDATE").WithArgs(p.ID).
		WillReturnRows(sqlmock.NewRows(selectColumns).AddRow(
			p.ID, p.Name, p.DisplayName, p.Description, p.Content, argsJSON,
			p.Category, p.Scope, pq.Array(p.Personas), p.OwnerEmail, p.Source, p.Enabled,
			pq.Array(p.Tags), p.Status, p.ApprovedBy, p.ApprovedAt, p.DeprecatedAt, p.SupersededBy,
			p.ReviewRequested, p.RequestedScope, pq.Array(p.RequestedPersonas), p.Version,
			testRowTime, testRowTime,
		))
}

func TestUpdateWithVersion_SnapshotChangedInsertsAppliedVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	p := newTestPrompt()
	p.ID = "uuid-123"
	p.Version = 1
	// A draft shared prompt: content edits apply directly (no review gate),
	// which is the legitimate content path through UpdateWithVersion —
	// approved shared content only changes via ApproveVersion.
	p.Status = prompt.StatusDraft

	stored := *p
	stored.Content = "old content"
	argsJSON, err := json.Marshal(p.Arguments)
	require.NoError(t, err)

	mock.ExpectBegin()
	expectLockPrompt(mock, &stored, argsJSON)
	mock.ExpectQuery("SELECT GREATEST").WithArgs(p.ID).
		WillReturnRows(sqlmock.NewRows([]string{"next"}).AddRow(2))
	mock.ExpectExec("INSERT INTO prompt_versions").WithArgs(
		p.ID, 2, p.DisplayName, p.Description, p.Content, argsJSON,
		pq.Array(p.Tags), "editor@example.com", prompt.VersionStatusApplied, "", nil,
	).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE prompts").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = store.UpdateWithVersion(context.Background(), p, "editor@example.com")
	assert.NoError(t, err)
	assert.Equal(t, 2, p.Version, "the live row advances to the new snapshot")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateWithVersion_NoSnapshotChangeSkipsVersionRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	p := newTestPrompt()
	p.ID = "uuid-123"
	p.Version = 3
	p.Status = prompt.StatusApproved

	argsJSON, err := json.Marshal(p.Arguments)
	require.NoError(t, err)

	// The stored row matches p on every snapshot field: no INSERT expected.
	mock.ExpectBegin()
	expectLockPrompt(mock, p, argsJSON)
	mock.ExpectExec("UPDATE prompts").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = store.UpdateWithVersion(context.Background(), p, "editor@example.com")
	assert.NoError(t, err)
	assert.Equal(t, 3, p.Version, "no snapshot change leaves the version untouched")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateWithVersion_SystemRowSkipsVersioning(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	p := newTestPrompt()
	p.ID = "uuid-sys"
	p.Source = prompt.SourceSystem
	p.Status = prompt.StatusApproved
	p.Version = 1

	stored := *p
	stored.Content = "old config content"
	argsJSON, err := json.Marshal(p.Arguments)
	require.NoError(t, err)

	mock.ExpectBegin()
	expectLockPrompt(mock, &stored, argsJSON)
	mock.ExpectExec("UPDATE prompts").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = store.UpdateWithVersion(context.Background(), p, "ingest")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateWithVersion_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	p := newTestPrompt()
	p.ID = "missing"

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .+ FROM prompts WHERE id = .+ FOR UPDATE").WithArgs(p.ID).
		WillReturnRows(sqlmock.NewRows(selectColumns))
	mock.ExpectRollback()

	err = store.UpdateWithVersion(context.Background(), p, "editor@example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateDraftVersion_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	proposed := newTestPrompt()
	proposed.Content = "proposed content"
	argsJSON, err := json.Marshal(proposed.Arguments)
	require.NoError(t, err)

	locked := newTestPrompt()
	locked.ID = "uuid-123"

	mock.ExpectBegin()
	expectLockPrompt(mock, locked, argsJSON)
	mock.ExpectQuery("SELECT GREATEST").WithArgs("uuid-123").
		WillReturnRows(sqlmock.NewRows([]string{"next"}).AddRow(4))
	mock.ExpectExec("INSERT INTO prompt_versions").WithArgs(
		"uuid-123", 4, proposed.DisplayName, proposed.Description, proposed.Content,
		argsJSON, pq.Array(proposed.Tags), "author@example.com", prompt.VersionStatusDraft, "", nil,
	).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	n, err := store.CreateDraftVersion(context.Background(), "uuid-123", proposed, "author@example.com")
	assert.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateDraftVersion_PromptNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .+ FROM prompts WHERE id = .+ FOR UPDATE").WithArgs("missing").
		WillReturnRows(sqlmock.NewRows(selectColumns))
	mock.ExpectRollback()

	_, err = store.CreateDraftVersion(context.Background(), "missing", newTestPrompt(), "a@example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateDraftVersion_RefusesSystemRow: system rows are read-only config
// mirrors; the store refuses drafts for them regardless of which surface asks.
func TestCreateDraftVersion_RefusesSystemRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	locked := newTestPrompt()
	locked.ID = "uuid-sys"
	locked.Source = prompt.SourceSystem
	argsJSON, err := json.Marshal(locked.Arguments)
	require.NoError(t, err)

	mock.ExpectBegin()
	expectLockPrompt(mock, locked, argsJSON)
	mock.ExpectRollback()

	_, err = store.CreateDraftVersion(context.Background(), "uuid-sys", newTestPrompt(), "a@example.com")
	require.ErrorIs(t, err, prompt.ErrVersionConflict)
	assert.Contains(t, err.Error(), "read-only system prompt")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdateWithVersion_RejectsRacingGatedEdit: the review gate is re-checked
// against the locked row, so an edit that raced an approval conflicts instead
// of silently replacing just-approved content.
func TestUpdateWithVersion_RejectsRacingGatedEdit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	p := newTestPrompt()
	p.ID = "uuid-123"
	p.Version = 1
	// The caller read the prompt as a draft (no gate) and edited content...
	p.Status = prompt.StatusDraft
	p.Content = "editor's new content"

	// ...but by lock time an admin approved it.
	locked := *p
	locked.Status = prompt.StatusApproved
	locked.Content = "the approved content"
	argsJSON, err := json.Marshal(p.Arguments)
	require.NoError(t, err)

	mock.ExpectBegin()
	expectLockPrompt(mock, &locked, argsJSON)
	mock.ExpectRollback()

	err = store.UpdateWithVersion(context.Background(), p, "editor@example.com")
	require.ErrorIs(t, err, prompt.ErrVersionConflict)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListVersions_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	rows := sqlmock.NewRows(versionSelectColumns)
	r2 := versionRow("v2", 2, "new content", prompt.VersionStatusDraft)
	r1 := versionRow("v1", 1, "old content", prompt.VersionStatusApplied)
	rows.AddRow(toDriverValues(r2)...)
	rows.AddRow(toDriverValues(r1)...)

	mock.ExpectQuery("SELECT .+ FROM prompt_versions WHERE prompt_id = .+ ORDER BY version DESC").
		WithArgs("uuid-123").WillReturnRows(rows)

	out, err := store.ListVersions(context.Background(), "uuid-123")
	assert.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, 2, out[0].Version)
	assert.Equal(t, prompt.VersionStatusDraft, out[0].Status)
	assert.Equal(t, "author@example.com", out[0].Author)
	assert.Equal(t, 1, out[1].Version)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetVersion_FoundAndNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)

	rows := sqlmock.NewRows(versionSelectColumns)
	rows.AddRow(toDriverValues(versionRow("v1", 1, "old content", prompt.VersionStatusApplied))...)
	mock.ExpectQuery("SELECT .+ FROM prompt_versions WHERE prompt_id = .+ AND version = ").
		WithArgs("uuid-123", 1).WillReturnRows(rows)

	v, err := store.GetVersion(context.Background(), "uuid-123", 1)
	assert.NoError(t, err)
	require.NotNil(t, v)
	assert.Equal(t, "old content", v.Content)

	mock.ExpectQuery("SELECT .+ FROM prompt_versions WHERE prompt_id = .+ AND version = ").
		WithArgs("uuid-123", 9).WillReturnRows(sqlmock.NewRows(versionSelectColumns))

	v, err = store.GetVersion(context.Background(), "uuid-123", 9)
	assert.NoError(t, err)
	assert.Nil(t, v)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestApproveVersion_AppliesSnapshotAndStamps(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	live := newTestPrompt()
	live.ID = "uuid-123"
	live.Version = 1
	live.Status = prompt.StatusApproved
	argsJSON, err := json.Marshal(live.Arguments)
	require.NoError(t, err)

	mock.ExpectBegin()
	expectLockPrompt(mock, live, argsJSON)
	draftRows := sqlmock.NewRows(versionSelectColumns)
	draftRows.AddRow(toDriverValues(versionRow("v2", 2, "draft content", prompt.VersionStatusDraft))...)
	mock.ExpectQuery("SELECT .+ FROM prompt_versions WHERE prompt_id = .+ AND version = .+ FOR UPDATE").
		WithArgs("uuid-123", 2).WillReturnRows(draftRows)
	mock.ExpectExec("UPDATE prompts").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE prompt_versions SET status").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE prompt_versions SET status").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	updated, err := store.ApproveVersion(context.Background(), "uuid-123", 2, "admin@example.com")
	require.NoError(t, err)
	assert.Equal(t, "draft content", updated.Content, "the draft snapshot is applied to the live row")
	assert.Equal(t, 2, updated.Version)
	assert.Equal(t, prompt.StatusApproved, updated.Status)
	assert.Equal(t, "admin@example.com", updated.ApprovedBy)
	require.NotNil(t, updated.ApprovedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestApproveVersion_RejectsNonDraft(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	live := newTestPrompt()
	live.ID = "uuid-123"
	live.Status = prompt.StatusApproved
	argsJSON, err := json.Marshal(live.Arguments)
	require.NoError(t, err)

	mock.ExpectBegin()
	expectLockPrompt(mock, live, argsJSON)
	rows := sqlmock.NewRows(versionSelectColumns)
	rows.AddRow(toDriverValues(versionRow("v1", 1, "content", prompt.VersionStatusApplied))...)
	mock.ExpectQuery("SELECT .+ FROM prompt_versions WHERE prompt_id = .+ AND version = .+ FOR UPDATE").
		WithArgs("uuid-123", 1).WillReturnRows(rows)
	mock.ExpectRollback()

	_, err = store.ApproveVersion(context.Background(), "uuid-123", 1, "admin@example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a pending draft")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestApproveVersion_RejectsRetiredPrompt(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	live := newTestPrompt()
	live.ID = "uuid-123"
	live.Status = prompt.StatusDeprecated
	argsJSON, err := json.Marshal(live.Arguments)
	require.NoError(t, err)

	mock.ExpectBegin()
	expectLockPrompt(mock, live, argsJSON)
	mock.ExpectRollback()

	_, err = store.ApproveVersion(context.Background(), "uuid-123", 2, "admin@example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deprecated")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestApproveVersion_MissingVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	live := newTestPrompt()
	live.ID = "uuid-123"
	live.Status = prompt.StatusApproved
	argsJSON, err := json.Marshal(live.Arguments)
	require.NoError(t, err)

	mock.ExpectBegin()
	expectLockPrompt(mock, live, argsJSON)
	mock.ExpectQuery("SELECT .+ FROM prompt_versions WHERE prompt_id = .+ AND version = .+ FOR UPDATE").
		WithArgs("uuid-123", 9).WillReturnRows(sqlmock.NewRows(versionSelectColumns))
	mock.ExpectRollback()

	_, err = store.ApproveVersion(context.Background(), "uuid-123", 9, "admin@example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no version 9")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRejectVersion_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status FROM prompts WHERE id").WithArgs("uuid-123").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(prompt.StatusApproved))
	mock.ExpectExec("UPDATE prompt_versions SET status").WithArgs(
		"uuid-123", 2, prompt.VersionStatusRejected, prompt.VersionStatusDraft,
	).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = store.RejectVersion(context.Background(), "uuid-123", 2)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRejectVersion_NoPendingDraft(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status FROM prompts WHERE id").WithArgs("uuid-123").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(prompt.StatusApproved))
	mock.ExpectExec("UPDATE prompt_versions SET status").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err = store.RejectVersion(context.Background(), "uuid-123", 2)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no pending draft")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListVersions_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	mock.ExpectQuery("SELECT .+ FROM prompt_versions").WillReturnError(errors.New("db down"))

	_, err = store.ListVersions(context.Background(), "uuid-123")
	assert.Error(t, err)
}

func TestListVersions_ScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	// A malformed arguments payload fails the version scanner's unmarshal.
	row := versionRow("v1", 1, "content", prompt.VersionStatusApplied)
	row[6] = []byte(`{not json`)
	rows := sqlmock.NewRows(versionSelectColumns)
	rows.AddRow(toDriverValues(row)...)
	mock.ExpectQuery("SELECT .+ FROM prompt_versions").WillReturnRows(rows)

	_, err = store.ListVersions(context.Background(), "uuid-123")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal version arguments")
}

func TestCreateDraftVersion_InsertError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)

	locked := newTestPrompt()
	locked.ID = "uuid-123"
	lockedArgs, err := json.Marshal(locked.Arguments)
	require.NoError(t, err)

	mock.ExpectBegin()
	expectLockPrompt(mock, locked, lockedArgs)
	mock.ExpectQuery("SELECT GREATEST").WithArgs("uuid-123").
		WillReturnRows(sqlmock.NewRows([]string{"next"}).AddRow(2))
	mock.ExpectExec("INSERT INTO prompt_versions").WillReturnError(errors.New("constraint"))
	mock.ExpectRollback()

	_, err = store.CreateDraftVersion(context.Background(), "uuid-123", newTestPrompt(), "a@example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insert prompt version")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRejectVersion_PromptNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status FROM prompts WHERE id").WithArgs("missing").
		WillReturnRows(sqlmock.NewRows([]string{"status"}))
	mock.ExpectRollback()

	err = store.RejectVersion(context.Background(), "missing", 2)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRejectVersion_ExecError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status FROM prompts WHERE id").WithArgs("uuid-123").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(prompt.StatusApproved))
	mock.ExpectExec("UPDATE prompt_versions SET status").WillReturnError(errors.New("db down"))
	mock.ExpectRollback()

	err = store.RejectVersion(context.Background(), "uuid-123", 2)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWithTx_BeginError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db)
	mock.ExpectBegin().WillReturnError(errors.New("db down"))

	err = store.withTx(context.Background(), "test op", func(*sql.Tx) error { return nil })
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "begin test op")
}

// toDriverValues converts an []any row to sqlmock driver values.
func toDriverValues(row []any) []driver.Value {
	out := make([]driver.Value, len(row))
	for i, v := range row {
		out[i] = v
	}
	return out
}
