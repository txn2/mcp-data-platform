package endpointapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprefusal"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// bindings is the binding store with the reads the routes make.
type bindings struct {
	epbinding.Store
	list     []epbinding.Binding
	listErr  error
	failures []epbinding.Failure
	filter   epbinding.Filter
}

// List models the store: the filter's matches by name, one page of them, and
// how many match in all.
func (b *bindings) List(_ context.Context, f epbinding.Filter) ([]epbinding.Binding, int, error) {
	b.filter = f
	if b.listErr != nil {
		return nil, 0, b.listErr
	}
	matched := []epbinding.Binding{}
	for _, e := range b.list {
		if (f.AuthMode == "" || e.AuthMode == f.AuthMode) && (f.Enabled == nil || e.Enabled == *f.Enabled) {
			matched = append(matched, e)
		}
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	lo := min(max(f.Offset, 0), len(matched))
	return matched[lo:min(lo+limit, len(matched))], len(matched), nil
}

func (b *bindings) GetByName(_ context.Context, name string) (*epbinding.Binding, error) {
	for i := range b.list {
		if b.list[i].Name == name {
			return &b.list[i], nil
		}
	}
	return nil, epbinding.ErrNotFound
}

func (b *bindings) Failures(context.Context, string, int) ([]epbinding.Failure, error) {
	return b.failures, nil
}

var now = time.Date(2026, 10, 19, 12, 30, 0, 0, time.UTC)

func contactBinding() epbinding.Binding {
	return epbinding.Binding{
		ID: "11111111-1111-1111-1111-111111111111", ScriptID: "s1", Name: "contact", Methods: []string{"POST"},
		AuthMode: epbinding.AuthPublic, Enabled: true, RateLimit: 60, UpdatedBy: "admin@example.com",
	}
}

func serve(t *testing.T, b *bindings, path string, expect func(sqlmock.Sqlmock)) *httptest.ResponseRecorder {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	expect(mock)
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{
		DB: db, Bindings: b, BaseURL: "https://data.example.com", Now: func() time.Time { return now },
		Script: func(_ context.Context, id string) (*script.Script, error) {
			if id != "s1" {
				return nil, errors.New("no such script")
			}
			return &script.Script{ID: "s1", Name: "contact-form", OwnerEmail: "jane@example.com"}, nil
		},
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	require.NoError(t, mock.ExpectationsWereMet())
	return rec
}

var trafficCols = []string{"endpoint", "requests", "s2", "s3", "s4", "s5", "failures", "p50", "p95", "callers", "last"}

func expectTraffic(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT substr(tool_name, 10)")).
		WillReturnRows(sqlmock.NewRows(trafficCols).AddRow("contact", 10, 7, 0, 2, 1, 1, 12.5, 80.0, 0, now.Add(-time.Minute)))
}

func expectRefusals(mock sqlmock.Sqlmock, endpoint string) {
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_endpoint_refusals")).WithArgs(sqlmock.AnyArg(), endpoint).
		WillReturnRows(sqlmock.NewRows([]string{"endpoint_id", "hour", "reason", "count"}).
			AddRow(contactBinding().ID, now.Truncate(time.Hour), eprefusal.ReasonBotCheck, 40).
			AddRow(contactBinding().ID, now.Truncate(time.Hour).Add(-time.Hour), eprefusal.ReasonRateLimit, 2))
}

// TestList_EveryBindingWithItsScriptOwnerAndTraffic is #2208's list: every
// binding with its script and owner, access, and over the range its requests
// by status class and its refusals by reason.
func TestList_EveryBindingWithItsScriptOwnerAndTraffic(t *testing.T) {
	orphan := contactBinding()
	orphan.ID, orphan.Name, orphan.ScriptID, orphan.AuthMode, orphan.Methods = "2", "orders", "gone", epbinding.AuthSignedIn, nil
	b := &bindings{list: []epbinding.Binding{contactBinding(), orphan}}
	rec := serve(t, b, "/api/v1/admin/endpoints?range=week", func(mock sqlmock.Sqlmock) {
		expectTraffic(mock)
		expectRefusals(mock, "")
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out List
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, RangeWeek, out.Range)
	assert.Equal(t, now.Add(-7*24*time.Hour), out.Since)
	require.Len(t, out.Endpoints, 2)

	c := out.Endpoints[0]
	assert.Equal(t, "https://data.example.com/endpoints/contact", c.URL)
	assert.Equal(t, epbinding.AuthPublic, c.AuthMode)
	assert.Equal(t, Script{ID: "s1", Name: "contact-form", Owner: "jane@example.com"}, c.Script)
	assert.Equal(t, int64(10), c.Traffic.Requests)
	assert.Equal(t, StatusClasses{S2xx: 7, S4xx: 2, S5xx: 1}, c.Traffic.ByStatusClass)
	assert.InDelta(t, 80.0, c.Traffic.P95MS, 0.001)
	assert.Equal(t, int64(42), c.Refused)
	assert.Equal(t, map[string]int64{"bot_check": 40, "rate_limit": 2}, c.RefusedByReason)

	o := out.Endpoints[1]
	assert.Equal(t, epbinding.AuthSignedIn, o.AuthMode)
	assert.Zero(t, o.Traffic.Requests, "an endpoint with no requests reads as none")
	assert.Equal(t, []string{}, o.Methods)
	assert.Equal(t, map[string]int64{}, o.RefusedByReason)
	assert.Equal(t, "gone", o.Script.ID, "a script that cannot be read is named by its id")
}

// TestList_NoBindingsIsAnEmptyList: the route answers [] rather than null.
func TestList_NoBindingsIsAnEmptyList(t *testing.T) {
	rec := serve(t, &bindings{}, "/api/v1/admin/endpoints", func(mock sqlmock.Sqlmock) {
		mock.ExpectQuery("SELECT substr").WillReturnRows(sqlmock.NewRows(trafficCols))
		mock.ExpectQuery("FROM script_endpoint_refusals").WillReturnRows(sqlmock.NewRows([]string{"endpoint_id", "hour", "reason", "count"}))
	})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `[]`, string(mustField(t, rec.Body.Bytes(), "endpoints")))
	assert.JSONEq(t, `"day"`, string(mustField(t, rec.Body.Bytes(), "range")))
}

// TestList_PagesAndFiltersOnTheServer: more endpoints than a page holds are
// reached a page at a time, with the total of every match; access and status
// narrow the listing before it is paged, and an unknown one is refused (#2251).
func TestList_PagesAndFiltersOnTheServer(t *testing.T) {
	list := make([]epbinding.Binding, 0, 226)
	for i := range 226 {
		e := contactBinding()
		e.ID, e.Name, e.Enabled = fmt.Sprint(i), fmt.Sprintf("ep-%03d", i), i%2 == 0
		if i%3 == 0 {
			e.AuthMode = epbinding.AuthSignedIn
		}
		list = append(list, e)
	}
	b := &bindings{list: list}
	read := func(path string) List {
		t.Helper()
		rec := serve(t, b, path, func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery("SELECT substr").WillReturnRows(sqlmock.NewRows(trafficCols))
			mock.ExpectQuery("FROM script_endpoint_refusals").WillReturnRows(sqlmock.NewRows([]string{"endpoint_id", "hour", "reason", "count"}))
		})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out List
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out
	}

	first := read("/api/v1/admin/endpoints")
	assert.Equal(t, 226, first.Total)
	assert.Equal(t, 1, first.Page)
	assert.Equal(t, defaultPerPage, first.PerPage)
	assert.Len(t, first.Endpoints, defaultPerPage)

	last := read("/api/v1/admin/endpoints?page=2&per_page=200")
	assert.Equal(t, 2, last.Page)
	require.Len(t, last.Endpoints, 26, "the endpoints past the first 200 are reached")
	assert.Equal(t, "ep-225", last.Endpoints[25].Name)

	capped := read("/api/v1/admin/endpoints?per_page=5000")
	assert.Equal(t, maxPerPage, capped.PerPage)

	public := read("/api/v1/admin/endpoints?access=public&status=on&per_page=200")
	assert.Equal(t, epbinding.AuthPublic, b.filter.AuthMode)
	require.NotNil(t, b.filter.Enabled)
	assert.True(t, *b.filter.Enabled)
	for _, e := range public.Endpoints {
		assert.Equal(t, epbinding.AuthPublic, e.AuthMode)
		assert.True(t, e.Enabled)
	}
	assert.Equal(t, len(public.Endpoints), public.Total)

	off := read("/api/v1/admin/endpoints?access=signed_in&status=off")
	require.NotNil(t, b.filter.Enabled)
	assert.False(t, *b.filter.Enabled)
	assert.Positive(t, off.Total)

	for _, bad := range []string{"access=everyone", "status=maybe"} {
		rec := serve(t, b, "/api/v1/admin/endpoints?"+bad, func(sqlmock.Sqlmock) {})
		assert.Equal(t, http.StatusBadRequest, rec.Code, bad)
	}
}

func mustField(t *testing.T, body []byte, key string) json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &m))
	return m[key]
}

// TestDetail_TheChartCallersAndFailures is #2208's endpoint page: requests
// and refusals in hourly buckets, the busiest callers, the failed requests.
func TestDetail_TheChartCallersAndFailures(t *testing.T) {
	failure := epbinding.Failure{ID: "f1", Status: 500, Error: "boom"}
	b := &bindings{list: []epbinding.Binding{contactBinding()}, failures: []epbinding.Failure{failure}}
	hour := now.Truncate(time.Hour).Unix() / 3600
	rec := serve(t, b, "/api/v1/admin/endpoints/contact", func(mock sqlmock.Sqlmock) {
		expectTraffic(mock)
		expectRefusals(mock, contactBinding().ID)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT floor(extract(epoch FROM timestamp)")).
			WithArgs(sqlmock.AnyArg(), "endpoint:contact", int64(3600)).
			WillReturnRows(sqlmock.NewRows([]string{"bucket", "class", "n"}).AddRow(hour, "2", 7).AddRow(hour, "5", 1).AddRow(hour, nil, 3))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT parameters->>'caller'")).
			WillReturnRows(sqlmock.NewRows([]string{"caller", "n"}).AddRow("ann@example.com", 5))
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var d Detail
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &d))
	assert.Equal(t, int64(3600), d.BucketSeconds)
	require.Len(t, d.Series, 25, "a bucket for every hour of the day, the current one included")
	last := d.Series[len(d.Series)-1]
	assert.Equal(t, now.Truncate(time.Hour), last.Start)
	assert.Equal(t, int64(7), last.S2xx)
	assert.Equal(t, int64(1), last.S5xx)
	assert.Equal(t, int64(40), last.Refused)
	assert.Equal(t, int64(2), d.Series[len(d.Series)-2].Refused)
	assert.Equal(t, []Caller{{Caller: "ann@example.com", Requests: 5}}, d.TopCallers)
	assert.Equal(t, []epbinding.Failure{failure}, d.Failures)
}

func TestDetail_RefusalsAndFailures(t *testing.T) {
	b := &bindings{list: []epbinding.Binding{contactBinding()}}
	rec := serve(t, b, "/api/v1/admin/endpoints/missing", func(sqlmock.Sqlmock) {})
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = serve(t, b, "/api/v1/admin/endpoints?range=month", func(sqlmock.Sqlmock) {})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = serve(t, &bindings{listErr: errors.New("db down")}, "/api/v1/admin/endpoints", func(sqlmock.Sqlmock) {})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	rec = serve(t, b, "/api/v1/admin/endpoints", func(mock sqlmock.Sqlmock) {
		mock.ExpectQuery("SELECT substr").WillReturnError(errors.New("db down"))
	})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	rec = serve(t, b, "/api/v1/admin/endpoints/contact?range=week", func(mock sqlmock.Sqlmock) {
		expectTraffic(mock)
		expectRefusals(mock, contactBinding().ID)
		mock.ExpectQuery("SELECT floor").WithArgs(sqlmock.AnyArg(), "endpoint:contact", int64(6*3600)).
			WillReturnError(errors.New("db down"))
	})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestRegister_NeedsItsReads(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/endpoints", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code, "nothing is mounted without a database")
}
