package resourceshare

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

func mockStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	return NewStore(db), mock
}

var errDown = errors.New("database down")

// A share with no origin is written as an explicit share, and its creation
// time is the row's.
func TestStoreInsertDefaultsOriginAndReadsCreatedAt(t *testing.T) {
	s, mock := mockStore(t)
	at := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	share := &portaldomain.Share{
		ID: "s1", ResourceID: "r1", Token: "tok", CreatedBy: "owner@example.com",
		SharedWithEmail: "reader@example.com", Permission: "viewer", AccessMode: "authenticated",
	}
	mock.ExpectQuery(insertQuery).
		WithArgs("s1", "r1", "tok", "owner@example.com", "", "reader@example.com", "", "viewer",
			string(portaldomain.OriginExplicit), "authenticated", "", "").
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(at))
	require.NoError(t, s.Insert(context.Background(), share))
	assert.Equal(t, at, share.CreatedAt)

	mock.ExpectQuery(insertQuery).WillReturnError(errDown)
	err := s.Insert(context.Background(), &portaldomain.Share{ID: "s2", Origin: portaldomain.OriginChannelPost})
	assert.ErrorIs(t, err, errDown)
}

var listCols = []string{
	"id", "created_by", "shared_with_user_id", "shared_with_email", "shared_with_persona",
	"permission", "origin", "access_mode", "posted_to", "created_at",
}

func TestStoreList(t *testing.T) {
	s, mock := mockStore(t)
	at := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	mock.ExpectQuery(listQuery).WithArgs("r1").WillReturnRows(sqlmock.NewRows(listCols).
		AddRow("s1", "owner@example.com", "", "reader@example.com", "", "editor", "explicit", "authenticated", "", at))
	got, err := s.List(context.Background(), "r1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "r1", got[0].ResourceID)
	assert.Equal(t, "reader@example.com", got[0].SharedWithEmail)
	assert.Equal(t, "editor", string(got[0].Permission))

	// No shares is an empty list, never nil.
	mock.ExpectQuery(listQuery).WithArgs("r2").WillReturnRows(sqlmock.NewRows(listCols))
	got, err = s.List(context.Background(), "r2")
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)

	mock.ExpectQuery(listQuery).WithArgs("r3").WillReturnError(errDown)
	_, err = s.List(context.Background(), "r3")
	assert.ErrorIs(t, err, errDown)

	mock.ExpectQuery(listQuery).WithArgs("r4").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("s1"))
	_, err = s.List(context.Background(), "r4")
	assert.Error(t, err)

	mock.ExpectQuery(listQuery).WithArgs("r5").WillReturnRows(sqlmock.NewRows(listCols).
		AddRow("s1", "o", "", "", "", "viewer", "explicit", "authenticated", "", at).RowError(0, errDown))
	_, err = s.List(context.Background(), "r5")
	assert.ErrorIs(t, err, errDown)
}

func TestStorePermission(t *testing.T) {
	s, mock := mockStore(t)
	rcpt := Recipient{UserID: "u1", Email: "u1@example.com", Persona: "staff", Internal: true}
	mock.ExpectQuery(permissionQuery).WithArgs("r1", "u1", "u1@example.com", "staff", true).
		WillReturnRows(sqlmock.NewRows([]string{"permission"}).AddRow("editor"))
	perm, err := s.Permission(context.Background(), "r1", rcpt)
	require.NoError(t, err)
	assert.Equal(t, "editor", perm)

	// No share is no permission, not an error.
	mock.ExpectQuery(permissionQuery).WillReturnError(sql.ErrNoRows)
	perm, err = s.Permission(context.Background(), "r2", rcpt)
	require.NoError(t, err)
	assert.Empty(t, perm)

	mock.ExpectQuery(permissionQuery).WillReturnError(errDown)
	_, err = s.Permission(context.Background(), "r3", rcpt)
	assert.ErrorIs(t, err, errDown)
}

var sharedCols = []string{"resource_id", "id", "created_by", "created_at", "permission", "via_persona"}

func TestStoreSharedWith(t *testing.T) {
	s, mock := mockStore(t)
	at := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	rcpt := Recipient{UserID: "u1", Email: "u1@example.com", Persona: "staff"}
	mock.ExpectQuery(sharedWithQuery).WithArgs("u1", "u1@example.com", "staff").
		WillReturnRows(sqlmock.NewRows(sharedCols).AddRow("r1", "s1", "owner@example.com", at, "viewer", "staff"))
	got, err := s.SharedWith(context.Background(), rcpt)
	require.NoError(t, err)
	assert.Equal(t, []Ref{{ResourceID: "r1", ShareID: "s1", SharedBy: "owner@example.com", SharedAt: at, Permission: "viewer", ViaPersona: "staff"}}, got)

	mock.ExpectQuery(sharedWithQuery).WillReturnRows(sqlmock.NewRows(sharedCols))
	got, err = s.SharedWith(context.Background(), rcpt)
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)

	mock.ExpectQuery(sharedWithQuery).WillReturnError(errDown)
	_, err = s.SharedWith(context.Background(), rcpt)
	assert.ErrorIs(t, err, errDown)

	mock.ExpectQuery(sharedWithQuery).WillReturnRows(sqlmock.NewRows([]string{"resource_id"}).AddRow("r1"))
	_, err = s.SharedWith(context.Background(), rcpt)
	assert.Error(t, err)

	mock.ExpectQuery(sharedWithQuery).WillReturnRows(sqlmock.NewRows(sharedCols).
		AddRow("r1", "s1", "o", at, "viewer", "").RowError(0, errDown))
	_, err = s.SharedWith(context.Background(), rcpt)
	assert.ErrorIs(t, err, errDown)
}

func TestStoreRevoke(t *testing.T) {
	s, mock := mockStore(t)
	mock.ExpectExec(revokeQuery).WithArgs("s1", "r1").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Revoke(context.Background(), "r1", "s1"))

	// A share already revoked, or on another resource, is not found.
	mock.ExpectExec(revokeQuery).WithArgs("s2", "r1").WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, s.Revoke(context.Background(), "r1", "s2"), ErrNotFound)

	mock.ExpectExec(revokeQuery).WithArgs("s3", "r1").WillReturnError(errDown)
	assert.ErrorIs(t, s.Revoke(context.Background(), "r1", "s3"), errDown)
}

func TestStoreSetPermission(t *testing.T) {
	s, mock := mockStore(t)
	mock.ExpectExec(permissionSetQuery).WithArgs("s1", "r1", "editor").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.SetPermission(context.Background(), "r1", "s1", portaldomain.PermissionEditor))

	// A share revoked, or on another resource, is not found.
	mock.ExpectExec(permissionSetQuery).WithArgs("s2", "r1", "viewer").WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, s.SetPermission(context.Background(), "r1", "s2", portaldomain.PermissionViewer), ErrNotFound)

	mock.ExpectExec(permissionSetQuery).WithArgs("s3", "r1", "viewer").WillReturnError(errDown)
	assert.ErrorIs(t, s.SetPermission(context.Background(), "r1", "s3", portaldomain.PermissionViewer), errDown)
}
