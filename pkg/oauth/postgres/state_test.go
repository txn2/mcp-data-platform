package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/oauth"
)

const testStateKey = "upstream-state-1"

func testAuthorizationState() *oauth.AuthorizationState {
	return &oauth.AuthorizationState{
		ClientID:            "client-1",
		RedirectURI:         "http://localhost:8080/callback",
		State:               "client-state",
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
		Scope:               "openid",
		UpstreamState:       testStateKey,
		CreatedAt:           time.Now().UTC().Truncate(time.Second),
	}
}

func TestSaveState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	state := testAuthorizationState()

	mock.ExpectExec("INSERT INTO oauth_authorization_states").
		WithArgs(testStateKey, sqlmock.AnyArg(), state.CreatedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.SaveState(context.Background(), testStateKey, state)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveState_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("INSERT INTO oauth_authorization_states").
		WillReturnError(errors.New("db down"))

	err = store.SaveState(context.Background(), testStateKey, testAuthorizationState())
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	state := testAuthorizationState()
	payload, _ := json.Marshal(state)

	mock.ExpectQuery("SELECT payload FROM oauth_authorization_states").
		WithArgs(testStateKey).
		WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow(payload))

	got, err := store.GetState(context.Background(), testStateKey)
	require.NoError(t, err)
	assert.Equal(t, state.ClientID, got.ClientID)
	assert.Equal(t, state.CodeChallenge, got.CodeChallenge)
	assert.Equal(t, state.UpstreamState, got.UpstreamState)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetState_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectQuery("SELECT payload FROM oauth_authorization_states").
		WithArgs(testStateKey).
		WillReturnRows(sqlmock.NewRows([]string{"payload"}))

	_, err = store.GetState(context.Background(), testStateKey)
	assert.ErrorIs(t, err, oauth.ErrStateNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetState_BadPayload(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectQuery("SELECT payload FROM oauth_authorization_states").
		WithArgs(testStateKey).
		WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow([]byte("not-json")))

	_, err = store.GetState(context.Background(), testStateKey)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("DELETE FROM oauth_authorization_states WHERE state_key").
		WithArgs(testStateKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.DeleteState(context.Background(), testStateKey)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteState_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("DELETE FROM oauth_authorization_states WHERE state_key").
		WillReturnError(errors.New("db down"))

	err = store.DeleteState(context.Background(), testStateKey)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupExpiredStates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("DELETE FROM oauth_authorization_states").
		WithArgs(float64(3600)).
		WillReturnResult(sqlmock.NewResult(0, 2))

	err = store.CleanupExpiredStates(context.Background(), time.Hour)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupExpiredStates_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("DELETE FROM oauth_authorization_states").
		WillReturnError(errors.New("db down"))

	err = store.CleanupExpiredStates(context.Background(), time.Hour)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
