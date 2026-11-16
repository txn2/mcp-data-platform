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
		"user", "u-1", "sources", "site.zip", "", "", pq.Array([]string{}), "application/zip", "", int64(100), at).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.insert(ctx, link{
		ID: "l1", UserID: "u-1", UserEmail: "ann@example.com", Claims: person, Scope: resource.ScopeUser, ScopeID: "u-1",
		Path: "sources", Filename: "site.zip", ContentType: "application/zip", MaxBytes: 100, ExpiresAt: at,
	}, "sum"))

	cols := []string{
		"id", "user_id", "user_email", "claims", "scope", "scope_id", "path", "filename", "display_name",
		"description", "tags", "content_type", "if_exists", "max_bytes", "expires_at",
	}
	mock.ExpectQuery(`UPDATE resource_upload_links SET used_at = \$2\s+WHERE token_sha256 = \$1 AND used_at IS NULL AND expires_at > \$2`).
		WithArgs("sum", at).
		WillReturnRows(sqlmock.NewRows(cols).AddRow("l1", "u-1", "ann@example.com", []byte(`{"sub":"u-1","email":"ann@example.com","personas":["analyst"]}`),
			"user", "u-1", "sources", "site.zip", "", "", pq.StringArray{"a"}, "application/zip", "", int64(100), at))
	l, err := st.claim(ctx, "sum", at)
	require.NoError(t, err)
	require.NotNil(t, l)
	assert.Equal(t, resource.ScopeUser, l.Scope)
	assert.Equal(t, []string{"analyst"}, l.Claims.Personas)
	assert.Equal(t, []string{"a"}, l.Tags)

	mock.ExpectQuery(`UPDATE resource_upload_links SET used_at`).WillReturnRows(sqlmock.NewRows(cols))
	_, err = st.claim(ctx, "spent", at)
	require.ErrorIs(t, err, ErrNoLink, "a used or expired link answers nothing")
	mock.ExpectQuery(`UPDATE resource_upload_links SET used_at`).WillReturnError(errors.New("db down"))
	_, err = st.claim(ctx, "x", at)
	require.Error(t, err)
	mock.ExpectQuery(`UPDATE resource_upload_links SET used_at`).
		WillReturnRows(sqlmock.NewRows(cols).AddRow("l1", "u", "", []byte(`{`), "user", "", "", "f", "", "", pq.StringArray{}, "", "", int64(1), at))
	_, err = st.claim(ctx, "bad", at)
	require.Error(t, err, "claims that do not read are an error, never a write as nobody")

	mock.ExpectExec(`UPDATE resource_upload_links SET resource_id = \$2, error = \$3 WHERE id = \$1`).WithArgs("l1", "r1", "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.finish(ctx, "l1", "r1", ""))
	mock.ExpectExec(`DELETE FROM resource_upload_links WHERE expires_at < \$1`).WithArgs(at.Add(-time.Hour)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	require.NoError(t, st.sweep(ctx, at.Add(-time.Hour)))
	mock.ExpectExec(`UPDATE resource_upload_links SET resource_id`).WillReturnError(errors.New("db down"))
	require.Error(t, st.finish(ctx, "l1", "", "x"))
	mock.ExpectExec(`DELETE FROM resource_upload_links`).WillReturnError(errors.New("db down"))
	require.Error(t, st.sweep(ctx, at))
	mock.ExpectExec(`INSERT INTO resource_upload_links`).WillReturnError(errors.New("db down"))
	require.Error(t, st.insert(ctx, link{Claims: person}, "s"))
	require.NoError(t, mock.ExpectationsWereMet())
}
