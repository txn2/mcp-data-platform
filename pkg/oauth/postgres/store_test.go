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

const (
	testClientID     = "test-client"
	testClientSecret = "$2a$10$hashedsecret" //nolint:gosec // Test constant, not a real credential.
	testClientName   = "Test Client"
	testUserID       = "user-123"
	testCodeValue    = "authcode-abc"
	testTokenValue   = "refresh-xyz"
	testScope        = "openid profile"
	testRedirectURI  = "http://localhost/callback"

	// testCleanupCount is the expected number of expired authorization codes removed.
	testCleanupCount = 3
	// testTokenCleanupCount is the expected number of expired refresh tokens removed.
	testTokenCleanupCount = 5
	// testDBError is the error message used for simulated database failures.
	testDBError = "connection refused"
	// testNotFoundError is the error message used for simulated not-found results.
	testNotFoundError = "sql: no rows"
)

func testClient() *oauth.Client {
	return &oauth.Client{
		ID:           "id-1",
		ClientID:     testClientID,
		ClientSecret: testClientSecret,
		Name:         testClientName,
		RedirectURIs: []string{"http://localhost/callback"},
		GrantTypes:   []string{"authorization_code", "refresh_token"},
		RequirePKCE:  true,
		CreatedAt:    time.Now().Truncate(time.Second),
		Active:       true,
	}
}

func testAuthCode() *oauth.AuthorizationCode {
	return &oauth.AuthorizationCode{
		ID:            "code-id-1",
		Code:          testCodeValue,
		ClientID:      testClientID,
		UserID:        testUserID,
		UserClaims:    map[string]any{"sub": testUserID},
		CodeChallenge: "challenge123",
		RedirectURI:   testRedirectURI,
		Scope:         testScope,
		ExpiresAt:     time.Now().Add(time.Minute).Truncate(time.Second),
		Used:          false,
		CreatedAt:     time.Now().Truncate(time.Second),
	}
}

func testRefreshToken() *oauth.RefreshToken {
	return &oauth.RefreshToken{
		ID:         "token-id-1",
		Token:      testTokenValue,
		ClientID:   testClientID,
		UserID:     testUserID,
		UserClaims: map[string]any{"sub": testUserID},
		Scope:      testScope,
		ExpiresAt:  time.Now().Add(time.Hour).Truncate(time.Second),
		CreatedAt:  time.Now().Truncate(time.Second),
	}
}

func TestCreateClient(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	client := testClient()

	mock.ExpectExec("INSERT INTO oauth_clients").
		WithArgs(client.ID, client.ClientID, client.ClientSecret, client.Name,
			sqlmock.AnyArg(), sqlmock.AnyArg(), client.RequirePKCE, client.CreatedAt, client.Active).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.CreateClient(context.Background(), client)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateClient_UpsertOnConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	client := testClient()

	// First insert succeeds
	mock.ExpectExec("INSERT INTO oauth_clients").
		WithArgs(client.ID, client.ClientID, client.ClientSecret, client.Name,
			sqlmock.AnyArg(), sqlmock.AnyArg(), client.RequirePKCE, client.CreatedAt, client.Active).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.CreateClient(context.Background(), client)
	require.NoError(t, err)

	// Second insert with same client_id but updated fields succeeds (upsert)
	updatedClient := testClient()
	updatedClient.ClientSecret = "$2a$10$newsecret" //nolint:gosec // Test constant, not a real credential.
	updatedClient.Name = "Updated Client"
	updatedClient.RedirectURIs = []string{"http://localhost/callback", "http://localhost/new-callback"}

	mock.ExpectExec("INSERT INTO oauth_clients").
		WithArgs(updatedClient.ID, updatedClient.ClientID, updatedClient.ClientSecret, updatedClient.Name,
			sqlmock.AnyArg(), sqlmock.AnyArg(), updatedClient.RequirePKCE, updatedClient.CreatedAt, updatedClient.Active).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.CreateClient(context.Background(), updatedClient)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetClient(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	client := testClient()
	redirectJSON, _ := json.Marshal(client.RedirectURIs)
	grantJSON, _ := json.Marshal(client.GrantTypes)

	rows := sqlmock.NewRows([]string{
		"id", "client_id", "client_secret", "name", "redirect_uris",
		"grant_types", "require_pkce", "created_at", "active",
	}).AddRow(
		client.ID, client.ClientID, client.ClientSecret, client.Name,
		redirectJSON, grantJSON, client.RequirePKCE, client.CreatedAt, client.Active,
	)

	mock.ExpectQuery("SELECT .+ FROM oauth_clients").
		WithArgs(testClientID).
		WillReturnRows(rows)

	result, err := store.GetClient(context.Background(), testClientID)
	assert.NoError(t, err)
	assert.Equal(t, client.ClientID, result.ClientID)
	assert.Equal(t, client.Name, result.Name)
	assert.Equal(t, client.RedirectURIs, result.RedirectURIs)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateClient(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	client := testClient()

	mock.ExpectExec("UPDATE oauth_clients").
		WithArgs(client.ClientSecret, client.Name, sqlmock.AnyArg(), sqlmock.AnyArg(),
			client.RequirePKCE, client.Active, client.ClientID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.UpdateClient(context.Background(), client)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateClient_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	client := testClient()

	mock.ExpectExec("UPDATE oauth_clients").
		WithArgs(client.ClientSecret, client.Name, sqlmock.AnyArg(), sqlmock.AnyArg(),
			client.RequirePKCE, client.Active, client.ClientID).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.UpdateClient(context.Background(), client)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "client not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteClient(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("UPDATE oauth_clients SET active = false").
		WithArgs(testClientID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.DeleteClient(context.Background(), testClientID)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListClients(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	client := testClient()
	redirectJSON, _ := json.Marshal(client.RedirectURIs)
	grantJSON, _ := json.Marshal(client.GrantTypes)

	rows := sqlmock.NewRows([]string{
		"id", "client_id", "client_secret", "name", "redirect_uris",
		"grant_types", "require_pkce", "created_at", "active",
	}).AddRow(
		client.ID, client.ClientID, client.ClientSecret, client.Name,
		redirectJSON, grantJSON, client.RequirePKCE, client.CreatedAt, client.Active,
	)

	mock.ExpectQuery("SELECT .+ FROM oauth_clients").WillReturnRows(rows)

	clients, err := store.ListClients(context.Background())
	assert.NoError(t, err)
	assert.Len(t, clients, 1)
	assert.Equal(t, testClientID, clients[0].ClientID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveAuthorizationCode(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	code := testAuthCode()

	mock.ExpectExec("INSERT INTO oauth_authorization_codes").
		WithArgs(code.ID, code.Code, code.ClientID, code.UserID,
			sqlmock.AnyArg(), code.CodeChallenge, code.RedirectURI,
			code.Scope, code.ExpiresAt, code.Used, code.CreatedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.SaveAuthorizationCode(context.Background(), code)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAuthorizationCode(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	code := testAuthCode()
	claimsJSON, _ := json.Marshal(code.UserClaims)

	rows := sqlmock.NewRows([]string{
		"id", "code", "client_id", "user_id", "user_claims",
		"code_challenge", "redirect_uri", "scope", "expires_at", "used", "created_at",
	}).AddRow(
		code.ID, code.Code, code.ClientID, code.UserID, claimsJSON,
		code.CodeChallenge, code.RedirectURI, code.Scope,
		code.ExpiresAt, code.Used, code.CreatedAt,
	)

	mock.ExpectQuery("SELECT .+ FROM oauth_authorization_codes").
		WithArgs(testCodeValue).
		WillReturnRows(rows)

	result, err := store.GetAuthorizationCode(context.Background(), testCodeValue)
	assert.NoError(t, err)
	assert.Equal(t, code.Code, result.Code)
	assert.Equal(t, code.ClientID, result.ClientID)
	assert.Equal(t, code.UserID, result.UserID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteAuthorizationCode(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("DELETE FROM oauth_authorization_codes").
		WithArgs(testCodeValue).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.DeleteAuthorizationCode(context.Background(), testCodeValue)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupExpiredCodes(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("DELETE FROM oauth_authorization_codes WHERE expires_at").
		WillReturnResult(sqlmock.NewResult(0, testCleanupCount))

	err = store.CleanupExpiredCodes(context.Background())
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveRefreshToken(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	token := testRefreshToken()

	mock.ExpectExec("INSERT INTO oauth_refresh_tokens").
		WithArgs(token.ID, token.Token, token.ClientID, token.UserID,
			sqlmock.AnyArg(), token.Scope, token.ExpiresAt, token.CreatedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.SaveRefreshToken(context.Background(), token)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRefreshToken(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	token := testRefreshToken()
	claimsJSON, _ := json.Marshal(token.UserClaims)

	rows := sqlmock.NewRows([]string{
		"id", "token", "client_id", "user_id", "user_claims",
		"scope", "expires_at", "created_at",
	}).AddRow(
		token.ID, token.Token, token.ClientID, token.UserID,
		claimsJSON, token.Scope, token.ExpiresAt, token.CreatedAt,
	)

	mock.ExpectQuery("SELECT .+ FROM oauth_refresh_tokens").
		WithArgs(testTokenValue).
		WillReturnRows(rows)

	result, err := store.GetRefreshToken(context.Background(), testTokenValue)
	assert.NoError(t, err)
	assert.Equal(t, token.Token, result.Token)
	assert.Equal(t, token.ClientID, result.ClientID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteRefreshToken(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("DELETE FROM oauth_refresh_tokens WHERE token").
		WithArgs(testTokenValue).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.DeleteRefreshToken(context.Background(), testTokenValue)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteRefreshTokensForClient(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("DELETE FROM oauth_refresh_tokens WHERE client_id").
		WithArgs(testClientID).
		WillReturnResult(sqlmock.NewResult(0, 2))

	err = store.DeleteRefreshTokensForClient(context.Background(), testClientID)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupExpiredTokens(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("DELETE FROM oauth_refresh_tokens WHERE expires_at").
		WillReturnResult(sqlmock.NewResult(0, testTokenCleanupCount))

	err = store.CleanupExpiredTokens(context.Background())
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupRoutineLifecycle(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	store.StartCleanupRoutine(time.Hour) // long interval so it won't fire

	err = store.Close()
	assert.NoError(t, err)
}

func TestClose_WithoutCleanupRoutine(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	err = store.Close()
	assert.NoError(t, err)
}

func TestCreateClient_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	client := testClient()

	mock.ExpectExec("INSERT INTO oauth_clients").
		WillReturnError(errors.New(testDBError))

	err = store.CreateClient(context.Background(), client)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting client")
}

func TestGetClient_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectQuery("SELECT .+ FROM oauth_clients").
		WithArgs("nonexistent").
		WillReturnError(errors.New(testNotFoundError))

	_, err = store.GetClient(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scanning client")
}

func TestUpdateClient_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	client := testClient()

	mock.ExpectExec("UPDATE oauth_clients").
		WillReturnError(errors.New(testDBError))

	err = store.UpdateClient(context.Background(), client)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "updating client")
}

func TestDeleteClient_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("UPDATE oauth_clients SET active = false").
		WillReturnError(errors.New(testDBError))

	err = store.DeleteClient(context.Background(), testClientID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deleting client")
}

func TestListClients_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectQuery("SELECT .+ FROM oauth_clients").
		WillReturnError(errors.New(testDBError))

	_, err = store.ListClients(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "querying clients")
}

func TestSaveAuthorizationCode_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	code := testAuthCode()

	mock.ExpectExec("INSERT INTO oauth_authorization_codes").
		WillReturnError(errors.New(testDBError))

	err = store.SaveAuthorizationCode(context.Background(), code)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting authorization code")
}

func TestGetAuthorizationCode_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectQuery("SELECT .+ FROM oauth_authorization_codes").
		WithArgs("nonexistent").
		WillReturnError(errors.New(testNotFoundError))

	_, err = store.GetAuthorizationCode(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scanning authorization code")
}

func TestDeleteAuthorizationCode_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("DELETE FROM oauth_authorization_codes").
		WillReturnError(errors.New(testDBError))

	err = store.DeleteAuthorizationCode(context.Background(), testCodeValue)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deleting authorization code")
}

func TestCleanupExpiredCodes_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("DELETE FROM oauth_authorization_codes").
		WillReturnError(errors.New(testDBError))

	err = store.CleanupExpiredCodes(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cleaning up expired codes")
}

func TestSaveRefreshToken_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)
	token := testRefreshToken()

	mock.ExpectExec("INSERT INTO oauth_refresh_tokens").
		WillReturnError(errors.New(testDBError))

	err = store.SaveRefreshToken(context.Background(), token)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting refresh token")
}

func TestGetRefreshToken_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectQuery("SELECT .+ FROM oauth_refresh_tokens").
		WithArgs("nonexistent").
		WillReturnError(errors.New(testNotFoundError))

	_, err = store.GetRefreshToken(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scanning refresh token")
}

func TestDeleteRefreshToken_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("DELETE FROM oauth_refresh_tokens WHERE token").
		WillReturnError(errors.New(testDBError))

	err = store.DeleteRefreshToken(context.Background(), testTokenValue)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deleting refresh token")
}

func TestDeleteRefreshTokensForClient_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("DELETE FROM oauth_refresh_tokens WHERE client_id").
		WillReturnError(errors.New(testDBError))

	err = store.DeleteRefreshTokensForClient(context.Background(), testClientID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deleting refresh tokens for client")
}

func TestCleanupExpiredTokens_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := New(db)

	mock.ExpectExec("DELETE FROM oauth_refresh_tokens WHERE expires_at").
		WillReturnError(errors.New(testDBError))

	err = store.CleanupExpiredTokens(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cleaning up expired tokens")
}
