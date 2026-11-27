package resourceupload

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

func TestPostgresStore(t *testing.T) {
	assert.Nil(t, NewStore(nil))
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	st := NewStore(db)
	ctx := context.Background()

	mock.ExpectExec(`INSERT INTO resource_upload_links`).WithArgs("l1", "sum", "u-1", "ann@example.com", sqlmock.AnyArg(),
		"user", "u-1", "sources", "site.zip", "", "", pq.Array([]string{}), "application/zip", "", int64(100), at, 1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.insert(ctx, link{
		ID: "l1", UserID: "u-1", UserEmail: "ann@example.com", Claims: person, Scope: resource.ScopeUser, ScopeID: "u-1",
		Path: "sources", Filename: "site.zip", ContentType: "application/zip", MaxBytes: 100, ExpiresAt: at,
	}, "sum"))

	cols := []string{
		"id", "user_id", "user_email", "claims", "scope", "scope_id", "path", "filename", "display_name",
		"description", "tags", "content_type", "if_exists", "max_bytes", "expires_at", "max_files", "files_used",
	}
	mock.ExpectQuery(`UPDATE resource_upload_links SET used_at = \$2, files_used = files_used \+ 1\s+WHERE token_sha256 = \$1 AND revoked_at IS NULL AND files_used < max_files AND expires_at > \$2`).
		WithArgs("sum", at).
		WillReturnRows(sqlmock.NewRows(cols).AddRow("l1", "u-1", "ann@example.com", []byte(`{"sub":"u-1","email":"ann@example.com","personas":["analyst"]}`),
			"user", "u-1", "sources", "site.zip", "", "", pq.StringArray{"a"}, "application/zip", "", int64(100), at, 3, 1))
	l, err := st.claim(ctx, "sum", at)
	require.NoError(t, err)
	require.NotNil(t, l)
	assert.Equal(t, resource.ScopeUser, l.Scope)
	assert.Equal(t, []string{"analyst"}, l.Claims.Personas)
	assert.Equal(t, []string{"a"}, l.Tags)
	assert.Equal(t, 3, l.MaxFiles)
	assert.Equal(t, 1, l.FilesUsed)

	mock.ExpectQuery(`UPDATE resource_upload_links SET used_at`).WillReturnRows(sqlmock.NewRows(cols))
	_, err = st.claim(ctx, "spent", at)
	require.ErrorIs(t, err, ErrNoLink, "a used or expired link answers nothing")
	mock.ExpectQuery(`UPDATE resource_upload_links SET used_at`).WillReturnError(errors.New("db down"))
	_, err = st.claim(ctx, "x", at)
	require.Error(t, err)
	mock.ExpectQuery(`UPDATE resource_upload_links SET used_at`).
		WillReturnRows(sqlmock.NewRows(cols).AddRow("l1", "u", "", []byte(`{`), "user", "", "", "f", "", "", pq.StringArray{}, "", "", int64(1), at, 1, 1))
	_, err = st.claim(ctx, "bad", at)
	require.Error(t, err, "claims that do not read are an error, never a write as nobody")

	mock.ExpectExec(`UPDATE resource_upload_links SET resource_id = \$2, error = '', resource_ids = array_append\(resource_ids, \$2\)`).
		WithArgs("l1", "r1").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.finish(ctx, "l1", "r1", ""))
	// A PUT that filed nothing gives its place back (#2844).
	mock.ExpectExec(`UPDATE resource_upload_links SET error = \$2, files_used = GREATEST\(files_used - 1, 0\) WHERE id = \$1`).
		WithArgs("l1", "too large").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.finish(ctx, "l1", "", "too large"))
	mock.ExpectExec(`DELETE FROM resource_upload_links WHERE expires_at < \$1`).WithArgs(at.Add(-time.Hour)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	require.NoError(t, st.sweep(ctx, at.Add(-time.Hour)))
	mock.ExpectExec(`UPDATE resource_upload_links SET error`).WillReturnError(errors.New("db down"))
	require.Error(t, st.finish(ctx, "l1", "", "x"))

	// The open links and a revoke (#2844).
	openCols := []string{"id", "scope", "scope_id", "path", "filename", "content_type", "max_bytes", "max_files", "files_used", "created_at", "expires_at"}
	mock.ExpectQuery(`FROM resource_upload_links\s+WHERE \(user_id = \$1 OR \(\$2 <> '' AND user_email = \$2\)\)\s+AND revoked_at IS NULL AND files_used < max_files AND expires_at > \$3`).
		WithArgs("u-1", "ann@example.com", at).
		WillReturnRows(sqlmock.NewRows(openCols).AddRow("l1", "user", "u-1", "renders", "", "image/png", int64(100), 4, 1, at, at.Add(time.Hour)))
	open, err := st.open(ctx, "u-1", "ann@example.com", at)
	require.NoError(t, err)
	require.Len(t, open, 1)
	assert.Equal(t, Open{
		ID: "l1", Scope: "user", ScopeID: "u-1", Path: "renders", ContentType: "image/png", MaxBytes: 100,
		MaxFiles: 4, FilesUsed: 1, CreatedAt: at, ExpiresAt: at.Add(time.Hour),
	}, open[0])
	mock.ExpectQuery(`FROM resource_upload_links`).WillReturnRows(sqlmock.NewRows(openCols))
	open, err = st.open(ctx, "u-2", "", at)
	require.NoError(t, err)
	assert.Equal(t, []Open{}, open, "none is an empty list")
	mock.ExpectQuery(`FROM resource_upload_links`).WillReturnError(errors.New("db down"))
	_, err = st.open(ctx, "u-1", "", at)
	require.Error(t, err)
	mock.ExpectQuery(`FROM resource_upload_links`).WillReturnRows(sqlmock.NewRows(openCols).AddRow("l1", "user", "u-1", "p", "", "x", "not a number", 1, 0, at, at))
	_, err = st.open(ctx, "u-1", "", at)
	require.Error(t, err)

	mock.ExpectExec(`UPDATE resource_upload_links SET revoked_at = \$5\s+WHERE id = \$1 AND revoked_at IS NULL AND \(\$4 OR user_id = \$2`).
		WithArgs("l1", "u-1", "ann@example.com", false, at).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.revoke(ctx, "l1", resource.Claims{Sub: "u-1", Email: "ann@example.com"}, at))
	mock.ExpectExec(`UPDATE resource_upload_links SET revoked_at`).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, st.revoke(ctx, "l1", resource.Claims{Sub: "u-2"}, at), ErrNoLink)
	mock.ExpectExec(`UPDATE resource_upload_links SET revoked_at`).WillReturnError(errors.New("db down"))
	require.Error(t, st.revoke(ctx, "l1", resource.Claims{Sub: "u-1"}, at))
	mock.ExpectExec(`DELETE FROM resource_upload_links`).WillReturnError(errors.New("db down"))
	require.Error(t, st.sweep(ctx, at))
	mock.ExpectExec(`INSERT INTO resource_upload_links`).WillReturnError(errors.New("db down"))
	require.Error(t, st.insert(ctx, link{Claims: person}, "s"))
	require.NoError(t, mock.ExpectationsWereMet())
}
