package scriptstore

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The account of a draft execution (#1364). It is written by one path and read
// by one, so what these tests pin is the write's boundedness and the read's
// not-found contract — the two places a store's caller has to trust it.

// dryRunColumnNames is the result-set shape a dry-run SELECT mock must return,
// in dryRunColumns order.
var dryRunColumnNames = []string{
	"id", "script_id", "source_sha256", "requested_by", "status", "error",
	"log", "log_truncated", "metrics", "outputs", "state_written", "created_at",
}

// dryRunRow is one full account row in dryRunColumns order.
func dryRunRow(status string) []driver.Value {
	return []driver.Value{
		"dpx_draft_1", "script_1", script.SourceDigest("x = 1\n"), "jane@example.com",
		status, "", "printed", false,
		[]byte(`{"steps":12,"duration_ms":40,"queries":1,"exports":1}`),
		[]byte(`[{"name":"daily","destination":"portal","format":"csv","row_count":3,"bytes":90}]`),
		nil, rowTime,
	}
}

// TestRecordDryRun_StoresTheAccountAndBoundsTheAuthorsHistory pins both halves
// of the write: the row, and the trim that keeps the table bounded by the
// authoring loop rather than by how many times somebody pressed the button.
func TestRecordDryRun_StoresTheAccountAndBoundsTheAuthorsHistory(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_dry_runs")).
		WithArgs("dpx_draft_1", "script_1", script.SourceDigest("x = 1\n"), "jane@example.com",
			script.RunStatusSucceeded, "", "printed", false, sqlmock.AnyArg(), sqlmock.AnyArg(), nil).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(rowTime))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM script_dry_runs")).
		WithArgs("script_1", "jane@example.com", dryRunHistoryPerAuthor).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	account := &script.DryRun{
		ID: "dpx_draft_1", ScriptID: "script_1", SourceSHA256: script.SourceDigest("x = 1\n"),
		RequestedBy: "jane@example.com", Status: script.RunStatusSucceeded, Log: "printed",
	}
	require.NoError(t, s.RecordDryRun(context.Background(), account))
	assert.Equal(t, rowTime, account.CreatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRecordDryRun_RefusesAnAccountWithNoRun keeps the id meaningful: it is the
// session the run executed under, and an account that names no run cannot be
// traced back to the audit rows it produced.
func TestRecordDryRun_RefusesAnAccountWithNoRun(t *testing.T) {
	s, _ := newMock(t)

	require.Error(t, s.RecordDryRun(context.Background(), &script.DryRun{ScriptID: "script_1"}))
	require.Error(t, s.RecordDryRun(context.Background(), nil))
}

// TestRecordDryRun_RollsBackAFailedTrim keeps the pair atomic: an account
// stored without its trim would leave the bound unenforced for that author.
func TestRecordDryRun_RollsBackAFailedTrim(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_dry_runs")).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(rowTime))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM script_dry_runs")).
		WillReturnError(errors.New("deadlock detected"))
	mock.ExpectRollback()

	err := s.RecordDryRun(context.Background(), &script.DryRun{ID: "dpx_draft_1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trim")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRecordDryRun_ReportsAFailedInsert(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_dry_runs")).
		WillReturnError(errors.New("constraint violated"))
	mock.ExpectRollback()

	require.Error(t, s.RecordDryRun(context.Background(), &script.DryRun{ID: "dpx_draft_1"}))
}

// TestLatestDryRun_ReturnsTheAccountForThatExactSource is the reviewer's
// lookup: keyed by the code, so it describes the version in front of them.
func TestLatestDryRun_ReturnsTheAccountForThatExactSource(t *testing.T) {
	s, mock := newMock(t)
	digest := script.SourceDigest("x = 1\n")
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_dry_runs")).
		WithArgs("script_1", digest).
		WillReturnRows(sqlmock.NewRows(dryRunColumnNames).AddRow(dryRunRow(script.RunStatusSucceeded)...))

	got, err := s.LatestDryRun(context.Background(), "script_1", digest)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.True(t, got.Succeeded())
	assert.Equal(t, "jane@example.com", got.RequestedBy)
	assert.Equal(t, uint64(12), got.Metrics.Steps)
	require.Len(t, got.Outputs, 1)
	assert.Equal(t, "daily", got.Outputs[0].Name)
	assert.Equal(t, 90, got.Outputs[0].Bytes)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestLatestDryRun_AnswersNilWhenNobodyRanIt is the not-found contract every
// caller depends on: most versions were never dry-run, and that is not an
// error.
func TestLatestDryRun_AnswersNilWhenNobodyRanIt(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_dry_runs")).
		WillReturnRows(sqlmock.NewRows(dryRunColumnNames))

	got, err := s.LatestDryRun(context.Background(), "script_1", script.SourceDigest("x = 1\n"))
	require.NoError(t, err)
	assert.Nil(t, got)
}

// TestLatestDryRun_AsksNothingWithoutBothKeys keeps a half-formed lookup off
// the database: neither key alone identifies an account.
func TestLatestDryRun_AsksNothingWithoutBothKeys(t *testing.T) {
	s, mock := newMock(t)

	got, err := s.LatestDryRun(context.Background(), "", script.SourceDigest("x = 1\n"))
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = s.LatestDryRun(context.Background(), "script_1", nil)
	require.NoError(t, err)
	assert.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLatestDryRun_ReportsAFailedRead(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_dry_runs")).
		WillReturnError(errors.New("connection reset"))

	_, err := s.LatestDryRun(context.Background(), "script_1", script.SourceDigest("x = 1\n"))
	require.Error(t, err)
}

// TestLatestDryRun_ReportsUndecodableStoredJSON keeps a corrupt row from
// reading as a successful account of a run.
func TestLatestDryRun_ReportsUndecodableStoredJSON(t *testing.T) {
	s, mock := newMock(t)
	row := dryRunRow(script.RunStatusSucceeded)
	row[8] = []byte("{not json")
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_dry_runs")).
		WillReturnRows(sqlmock.NewRows(dryRunColumnNames).AddRow(row...))

	_, err := s.LatestDryRun(context.Background(), "script_1", script.SourceDigest("x = 1\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metrics")
}

// TestOrEmptyDryRunOutputs keeps the column storing a list rather than null for
// a run that would have written nothing.
func TestOrEmptyDryRunOutputs(t *testing.T) {
	assert.NotNil(t, orEmptyDryRunOutputs(nil))
	assert.Empty(t, orEmptyDryRunOutputs(nil))
	assert.Len(t, orEmptyDryRunOutputs([]script.DryRunOutput{{Name: "daily"}}), 1)
}
