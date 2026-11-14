package scripthandler

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var viewCols = []string{"id", "kind", "type", "script_id", "name", "owner_email", "target", "enabled", "created_by", "created_at"}

func viewRow(id, kind, typ, target string) []driver.Value {
	return []driver.Value{id, kind, typ, "s1", "daily", "jane@example.com", target, true, "jane@example.com", fakeEpoch}
}

func mockView(t *testing.T) (View, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	return View{DB: db}, mock
}

// TestView_List binds each filter field to its parameter, a zero field as
// the value its clause reads as "no filter", and reads the total from the
// window count.
func TestView_List(t *testing.T) {
	list := regexp.QuoteMeta("FROM script_handlers h JOIN scripts s ON s.id = h.script_id")
	withCount := append(append([]string{}, viewCols...), "count")

	t.Run("no filter", func(t *testing.T) {
		v, mock := mockView(t)
		mock.ExpectQuery(list).WithArgs(nil, nil, "", sql.NullBool{}, "", MaxListLimit, 0).
			WillReturnRows(sqlmock.NewRows(withCount).
				AddRow(append(viewRow("ep_1", KindEndpoint, TypeForm, "contact"), 7)...).
				AddRow(append(viewRow("sched_1", KindSchedule, KindSchedule, "@daily UTC"), 7)...))
		got, total, err := v.List(context.Background(), Filter{Limit: 9999, Offset: -4})
		require.NoError(t, err)
		assert.Equal(t, 7, total, "the total counts every match, not the page")
		require.Len(t, got, 2)
		assert.Equal(t, Handler{
			ID: "ep_1", Kind: KindEndpoint, Type: TypeForm, ScriptID: "s1", ScriptName: "daily", OwnerEmail: "jane@example.com",
			Target: "contact", Enabled: true, CreatedBy: "jane@example.com", CreatedAt: fakeEpoch,
		}, got[0])
	})

	t.Run("every field", func(t *testing.T) {
		v, mock := mockView(t)
		on := false
		mock.ExpectQuery(list).
			WithArgs(pq.Array([]string{"schedule", "form"}), pq.Array([]string{}), "Jane@example.com",
				sql.NullBool{Bool: false, Valid: true}, "daily", 25, 50).
			WillReturnRows(sqlmock.NewRows(withCount))
		got, total, err := v.List(context.Background(), Filter{
			Types: []string{"schedule", "form"}, ScriptIDs: []string{}, OwnerEmail: "Jane@example.com",
			Enabled: &on, Search: "daily", Limit: 25, Offset: 50,
		})
		require.NoError(t, err)
		assert.Equal(t, []Handler{}, got, "no match is [] and never null")
		assert.Zero(t, total)
	})

	t.Run("failures", func(t *testing.T) {
		v, mock := mockView(t)
		mock.ExpectQuery(list).WillReturnError(errDown)
		_, _, err := v.List(context.Background(), Filter{})
		require.ErrorIs(t, err, errDown)
		assert.Contains(t, err.Error(), "listing handlers")

		mock.ExpectQuery(list).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("ep_1"))
		_, _, err = v.List(context.Background(), Filter{})
		require.ErrorContains(t, err, "scanning a handler")

		mock.ExpectQuery(list).WillReturnRows(sqlmock.NewRows(withCount).
			AddRow(append(viewRow("ep_1", KindEndpoint, KindEndpoint, "x"), 1)...).RowError(0, errDown))
		_, _, err = v.List(context.Background(), Filter{})
		require.ErrorIs(t, err, errDown)
	})
}

func TestView_Get(t *testing.T) {
	get := regexp.QuoteMeta("WHERE h.id::text = $1")
	v, mock := mockView(t)
	mock.ExpectQuery(get).WithArgs("wh_1").
		WillReturnRows(sqlmock.NewRows(viewCols).AddRow(viewRow("wh_1", KindWebhook, KindWebhook, "shop")...))
	h, err := v.Get(context.Background(), "wh_1")
	require.NoError(t, err)
	assert.Equal(t, "shop", h.Target)

	mock.ExpectQuery(get).WithArgs("nope").WillReturnError(sql.ErrNoRows)
	_, err = v.Get(context.Background(), "nope")
	require.ErrorIs(t, err, ErrNotFound)

	mock.ExpectQuery(get).WillReturnError(errDown)
	_, err = v.Get(context.Background(), "wh_1")
	require.ErrorIs(t, err, errDown)
	assert.NotErrorIs(t, err, ErrNotFound, "a failed read is not a missing handler")
}

func TestView_TypesOf(t *testing.T) {
	types := regexp.QuoteMeta("SELECT DISTINCT script_id, type FROM script_handlers")
	v, mock := mockView(t)

	got, err := v.TypesOf(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{}, got, "no scripts asks nothing")

	mock.ExpectQuery(types).WithArgs(pq.Array([]string{"s1", "s2", "s3"})).
		WillReturnRows(sqlmock.NewRows([]string{"script_id", "type"}).
			AddRow("s1", "form").AddRow("s1", "schedule").AddRow("s2", "webhook"))
	got, err = v.TypesOf(context.Background(), []string{"s1", "s2", "s3"})
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"s1": {"form", "schedule"}, "s2": {"webhook"}}, got)

	mock.ExpectQuery(types).WillReturnError(errDown)
	_, err = v.TypesOf(context.Background(), []string{"s1"})
	require.ErrorIs(t, err, errDown)

	mock.ExpectQuery(types).WillReturnRows(sqlmock.NewRows([]string{"script_id"}).AddRow("s1"))
	_, err = v.TypesOf(context.Background(), []string{"s1"})
	require.ErrorContains(t, err, "scanning a script's handler type")

	mock.ExpectQuery(types).WillReturnRows(sqlmock.NewRows([]string{"script_id", "type"}).AddRow("s1", "form").RowError(0, errDown))
	_, err = v.TypesOf(context.Background(), []string{"s1"})
	require.ErrorIs(t, err, errDown)
}
