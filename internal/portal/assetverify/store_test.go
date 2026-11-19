package assetverify

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/assetstatus"
)

var fixedNow = time.Date(2026, 11, 19, 2, 0, 0, 0, time.UTC)

func newMock(t *testing.T) (*Postgres, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	p := New(db)
	p.now = func() time.Time { return fixedNow }
	return p, mock
}

func TestVerificationNormalize(t *testing.T) {
	future := fixedNow.Add(48 * time.Hour)
	past := fixedNow.Add(-time.Minute)
	cases := map[string]struct {
		in      Verification
		wantErr string
	}{
		"ok, no expiry": {in: Verification{Actor: "c@x", Version: 3, Note: "  checked  "}},
		"ok, future":    {in: Verification{Actor: "c@x", Version: 1, ExpiresAt: &future}},
		"note too long": {in: Verification{Actor: "c@x", Version: 1, Note: strings.Repeat("n", 501)}, wantErr: "longer than 500"},
		"past expiry":   {in: Verification{Actor: "c@x", Version: 1, ExpiresAt: &past}, wantErr: "in the future"},
		"expiry is now": {in: Verification{Actor: "c@x", Version: 1, ExpiresAt: &fixedNow}, wantErr: "in the future"},
		"no version":    {in: Verification{Actor: "c@x"}, wantErr: "no version"},
		"no actor":      {in: Verification{Actor: " ", Version: 1}, wantErr: "no actor"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := tc.in.Normalize(fixedNow)
			if tc.wantErr != "" {
				require.ErrorIs(t, err, ErrInvalid)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, strings.TrimSpace(tc.in.Note), got.Note)
		})
	}
}

func TestVerifyUpsertsAndRecordsTheChange(t *testing.T) {
	store, mock := newMock(t)
	expires := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_verifications")).
		WithArgs("a1", "carol@example.com", 3, "ledger checked", &expires).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_mark_events")).
		WithArgs("a1", assetstatus.KindVerification, "", valueVerified, "ledger checked (expires 2026-12-31)", "carol@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := store.Verify(context.Background(), "a1", Verification{
		Actor: "carol@example.com", Note: "ledger checked", Version: 3, ExpiresAt: &expires,
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVerifyWithNoExpiryRecordsTheNoteAlone(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_verifications")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_mark_events")).
		WithArgs("a1", assetstatus.KindVerification, "", valueVerified, "", "c@x").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	require.NoError(t, store.Verify(context.Background(), "a1", Verification{Actor: "c@x", Version: 1}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVerifyRefusesAPastExpiryWithoutWriting(t *testing.T) {
	store, mock := newMock(t)
	past := fixedNow.Add(-time.Hour)
	err := store.Verify(context.Background(), "a1", Verification{Actor: "c@x", Version: 1, ExpiresAt: &past})
	require.ErrorIs(t, err, ErrInvalid)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVerifyReportsEachFailedStep(t *testing.T) {
	ok := Verification{Actor: "c@x", Version: 1}

	store, mock := newMock(t)
	mock.ExpectBegin().WillReturnError(errors.New("no conn"))
	assert.ErrorContains(t, store.Verify(context.Background(), "a1", ok), "starting transaction")

	store, mock = newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_verifications")).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	assert.ErrorContains(t, store.Verify(context.Background(), "a1", ok), "verifying asset")

	store, mock = newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_verifications")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_mark_events")).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	assert.ErrorContains(t, store.Verify(context.Background(), "a1", ok), "recording verification change")

	store, mock = newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_verifications")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_mark_events")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit().WillReturnError(errors.New("commit"))
	assert.ErrorContains(t, store.Verify(context.Background(), "a1", ok), "committing verification")
}

func TestUnverifyRemovesAndRecords(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(deleteVerificationSQL)).WithArgs("a1").
		WillReturnRows(sqlmock.NewRows([]string{"verified_by"}).AddRow("carol@example.com"))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_mark_events")).
		WithArgs("a1", assetstatus.KindVerification, valueVerified, "", "", "dan@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	had, err := store.Unverify(context.Background(), "a1", "dan@example.com")
	require.NoError(t, err)
	assert.True(t, had)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUnverifyWithNoVerificationRecordsNothing(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(deleteVerificationSQL)).WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	had, err := store.Unverify(context.Background(), "a1", "dan@example.com")
	require.NoError(t, err)
	assert.False(t, had)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUnverifyReportsEachFailedStep(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin().WillReturnError(errors.New("no conn"))
	_, err := store.Unverify(context.Background(), "a1", "d")
	assert.ErrorContains(t, err, "starting transaction")

	store, mock = newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(deleteVerificationSQL)).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	_, err = store.Unverify(context.Background(), "a1", "d")
	assert.ErrorContains(t, err, "removing verification")

	store, mock = newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(deleteVerificationSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"verified_by"}).AddRow("c"))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_mark_events")).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	_, err = store.Unverify(context.Background(), "a1", "d")
	assert.ErrorContains(t, err, "recording verification change")

	store, mock = newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(deleteVerificationSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"verified_by"}).AddRow("c"))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_mark_events")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit().WillReturnError(errors.New("commit"))
	_, err = store.Unverify(context.Background(), "a1", "d")
	assert.ErrorContains(t, err, "committing removal")
}

func TestVerifiedBy(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(verifiedBySQL)).WithArgs("a1").
		WillReturnRows(sqlmock.NewRows([]string{"verified_by"}).AddRow("carol@example.com"))
	by, err := store.VerifiedBy(context.Background(), "a1")
	require.NoError(t, err)
	assert.Equal(t, "carol@example.com", by)

	mock.ExpectQuery(regexp.QuoteMeta(verifiedBySQL)).WithArgs("a2").WillReturnError(sql.ErrNoRows)
	by, err = store.VerifiedBy(context.Background(), "a2")
	require.NoError(t, err)
	assert.Empty(t, by, "an unverified (or expired) asset has no verifier")

	mock.ExpectQuery(regexp.QuoteMeta(verifiedBySQL)).WithArgs("a3").WillReturnError(errors.New("boom"))
	_, err = store.VerifiedBy(context.Background(), "a3")
	assert.ErrorContains(t, err, "reading verification")
}

func TestClaimDueReturnsTheClaimedRows(t *testing.T) {
	store, mock := newMock(t)
	horizon := fixedNow.Add(ReminderLead)
	exp := fixedNow.Add(72 * time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE portal_asset_verifications SET reminded_at")).
		WithArgs(fixedNow, horizon, 10).
		WillReturnRows(sqlmock.NewRows([]string{"asset_id", "verified_by", "expires_at"}).
			AddRow("a1", "carol@example.com", exp))
	due, err := store.ClaimDue(context.Background(), fixedNow, horizon, 10)
	require.NoError(t, err)
	assert.Equal(t, []Due{{AssetID: "a1", VerifiedBy: "carol@example.com", ExpiresAt: exp}}, due)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClaimDueReportsFailures(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE portal_asset_verifications")).WillReturnError(errors.New("boom"))
	_, err := store.ClaimDue(context.Background(), fixedNow, fixedNow, 1)
	assert.ErrorContains(t, err, "claiming reminders")

	mock.ExpectQuery(regexp.QuoteMeta("UPDATE portal_asset_verifications")).
		WillReturnRows(sqlmock.NewRows([]string{"asset_id"}).AddRow("a1"))
	_, err = store.ClaimDue(context.Background(), fixedNow, fixedNow, 1)
	assert.ErrorContains(t, err, "scanning reminder")

	mock.ExpectQuery(regexp.QuoteMeta("UPDATE portal_asset_verifications")).
		WillReturnRows(sqlmock.NewRows([]string{"asset_id", "verified_by", "expires_at"}).
			AddRow("a1", "c", fixedNow).RowError(0, errors.New("row")))
	_, err = store.ClaimDue(context.Background(), fixedNow, fixedNow, 1)
	assert.ErrorContains(t, err, "iterating reminders")
}
