package scriptevents

import (
	"database/sql"
	"database/sql/driver"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var handlerTime = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

var handlerCols = []string{"id", "script_id", "source", "enabled", "created_by", "updated_by", "created_at", "updated_at"}

func handlerRow(id, scriptID, source string) []driver.Value {
	return []driver.Value{id, scriptID, source, true, "jane@example.com", "jane@example.com", handlerTime, handlerTime}
}

// TestAddHandler binds a script to a source (#2434): a new binding reports
// created, and a binding the script already holds answers that handler,
// turned on, without a second row.
func TestAddHandler(t *testing.T) {
	insert := regexp.QuoteMeta("ON CONFLICT (script_id, source) DO UPDATE")
	for _, created := range []bool{true, false} {
		s, mock := mockStore(t)
		mock.ExpectQuery(insert).WithArgs("s1", "shop", "jane@example.com").
			WillReturnRows(sqlmock.NewRows(append(append([]string{}, handlerCols...), "created")).
				AddRow(append(handlerRow("wh1", "s1", "shop"), created)...))
		h, got, err := s.AddHandler(t.Context(), "s1", "shop", "jane@example.com")
		require.NoError(t, err)
		assert.Equal(t, created, got)
		assert.Equal(t, Handler{
			ID: "wh1", ScriptID: "s1", Source: "shop", Enabled: true, CreatedBy: "jane@example.com",
			UpdatedBy: "jane@example.com", CreatedAt: handlerTime, UpdatedAt: handlerTime,
		}, h)
		require.NoError(t, mock.ExpectationsWereMet())
	}

	s, mock := mockStore(t)
	mock.ExpectQuery(insert).WillReturnError(errDown)
	_, _, err := s.AddHandler(t.Context(), "s1", "shop", "jane@example.com")
	require.ErrorIs(t, err, errDown)
	assert.Contains(t, err.Error(), "binding script s1 to webhook source shop")
}

func TestHandlerByID(t *testing.T) {
	read := regexp.QuoteMeta("FROM script_webhook_handlers WHERE id = $1")
	s, mock := mockStore(t)
	mock.ExpectQuery(read).WithArgs("wh1").WillReturnRows(sqlmock.NewRows(handlerCols).AddRow(handlerRow("wh1", "s1", "shop")...))
	h, err := s.HandlerByID(t.Context(), "wh1")
	require.NoError(t, err)
	assert.Equal(t, "shop", h.Source)
	assert.Equal(t, "s1", h.ScriptID)

	mock.ExpectQuery(read).WithArgs("gone").WillReturnError(sql.ErrNoRows)
	_, err = s.HandlerByID(t.Context(), "gone")
	require.ErrorIs(t, err, ErrHandlerNotFound)

	mock.ExpectQuery(read).WillReturnError(errDown)
	_, err = s.HandlerByID(t.Context(), "wh1")
	require.ErrorIs(t, err, errDown)
	assert.NotErrorIs(t, err, ErrHandlerNotFound, "a failed read is not a missing handler")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestHandlers lists the handlers a filter matches; a nil script list is no
// filter (bound NULL), an empty one still filters, and no match is [].
func TestHandlers(t *testing.T) {
	list := regexp.QuoteMeta("ORDER BY source, created_at")
	s, mock := mockStore(t)
	mock.ExpectQuery(list).WithArgs(nil, "").
		WillReturnRows(sqlmock.NewRows(handlerCols).AddRow(handlerRow("wh1", "s1", "esp")...).AddRow(handlerRow("wh2", "s2", "shop")...))
	all, err := s.Handlers(t.Context(), HandlerFilter{})
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "wh2", all[1].ID)

	mock.ExpectQuery(list).WithArgs(pq.Array([]string{}), "shop").WillReturnRows(sqlmock.NewRows(handlerCols))
	none, err := s.Handlers(t.Context(), HandlerFilter{ScriptIDs: []string{}, Source: "shop"})
	require.NoError(t, err)
	assert.Equal(t, []Handler{}, none, "no handler is [] and never null")

	mock.ExpectQuery(list).WillReturnError(errDown)
	_, err = s.Handlers(t.Context(), HandlerFilter{})
	require.ErrorIs(t, err, errDown)

	mock.ExpectQuery(list).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("wh1"))
	_, err = s.Handlers(t.Context(), HandlerFilter{})
	require.ErrorContains(t, err, "scanning a webhook handler")

	mock.ExpectQuery(list).
		WillReturnRows(sqlmock.NewRows(handlerCols).AddRow(handlerRow("wh1", "s1", "shop")...).RowError(0, errDown))
	_, err = s.Handlers(t.Context(), HandlerFilter{})
	require.ErrorIs(t, err, errDown)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnableHandler(t *testing.T) {
	update := regexp.QuoteMeta("UPDATE script_webhook_handlers")
	s, mock := mockStore(t)
	mock.ExpectExec(update).WithArgs("wh1", false, "admin@example.com").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.EnableHandler(t.Context(), "wh1", false, "admin@example.com"))

	mock.ExpectExec(update).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.EnableHandler(t.Context(), "gone", true, ""), ErrHandlerNotFound)

	mock.ExpectExec(update).WillReturnError(errDown)
	err := s.EnableHandler(t.Context(), "wh1", true, "")
	require.ErrorIs(t, err, errDown)
	assert.NotErrorIs(t, err, ErrHandlerNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteHandler(t *testing.T) {
	del := regexp.QuoteMeta("DELETE FROM script_webhook_handlers WHERE id = $1")
	s, mock := mockStore(t)
	mock.ExpectExec(del).WithArgs("wh1").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.DeleteHandler(t.Context(), "wh1"))

	mock.ExpectExec(del).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.DeleteHandler(t.Context(), "gone"), ErrHandlerNotFound)

	mock.ExpectExec(del).WillReturnError(errDown)
	err := s.DeleteHandler(t.Context(), "wh1")
	require.ErrorIs(t, err, errDown)
	assert.NotErrorIs(t, err, ErrHandlerNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
