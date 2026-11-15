package sitehttp

import (
	"database/sql/driver"
	"net/http"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitetraffic"
)

func trafficCols(lead ...string) []string {
	cols := append([]string{}, lead...)
	for i := range sitetraffic.NumClasses + sitetraffic.NumBuckets {
		cols = append(cols, "n"+string(rune('a'+i)))
	}
	return cols
}

// trafficRow is 50 2xx and 10 4xx, every one under 5ms.
func trafficRow(lead ...driver.Value) []driver.Value {
	out := append([]driver.Value{}, lead...)
	out = append(out, int64(50), int64(0), int64(10), int64(0), int64(0), int64(60))
	for range sitetraffic.NumBuckets - 1 {
		out = append(out, int64(0))
	}
	return out
}

func withTraffic(t *testing.T, f *fixture) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	f.rt.TrafficStore = &sitetraffic.Store{DB: db}
	return mock
}

// TestTraffic pins #2647's criteria 1 and 5 at the route: the owner reads
// prod's 50 2xx and 10 4xx with percentiles, someone who cannot view the site
// gets the site's refusal, and a range that is not one is refused.
func TestTraffic(t *testing.T) {
	f := newFixture(t)
	mock := withTraffic(t, f)
	mock.ExpectQuery(regexp.QuoteMeta("GROUP BY environment")).WillReturnRows(sqlmock.NewRows(trafficCols("environment")).AddRow(trafficRow("prod")...))
	mock.ExpectQuery(regexp.QuoteMeta("GROUP BY at")).WillReturnRows(sqlmock.NewRows(trafficCols("at")).AddRow(trafficRow(time.Date(2026, 11, 14, 13, 0, 0, 0, time.UTC))...))

	got := decodeBody[TrafficView](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, siteBase + "/traffic?range=1h", nil}, userOwner))
	assert.Equal(t, "1h", got.Range)
	assert.Equal(t, int64(60), got.StepSeconds)
	assert.True(t, got.Recording)
	assert.Equal(t, int64(50), got.Total.Classes["2xx"])
	assert.Equal(t, int64(10), got.Total.Classes["4xx"])
	assert.Positive(t, got.Total.P50)
	assert.Positive(t, got.Total.P99)
	assert.Equal(t, int64(60), got.Environments["prod"].Requests)
	require.Len(t, got.Series, 1)

	f.statusIs(t, http.StatusForbidden, route{http.MethodGet, siteBase + "/traffic", nil}, userStranger)
	f.statusIs(t, http.StatusBadRequest, route{http.MethodGet, siteBase + "/traffic?range=2y", nil}, userOwner)
	f.statusIs(t, http.StatusUnauthorized, route{http.MethodGet, siteBase + "/traffic", nil}, nil)
}

// TestTraffic_NoStoreAndOff: with no store the view is empty, and with
// recording off it says so.
func TestTraffic_NoStoreAndOff(t *testing.T) {
	f := newFixture(t)
	off := false
	cfg := f.rt.Live.Get()
	cfg.Traffic.Enabled = &off
	f.rt.Live.Set(cfg)
	got := decodeBody[TrafficView](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, siteBase + "/traffic", nil}, userOwner))
	assert.False(t, got.Recording, "recording off says so")
	assert.Equal(t, []TrafficPoint{}, got.Series)
}

// TestList_CarriesEachSitesLast24Hours pins criterion 2.
func TestList_CarriesEachSitesLast24Hours(t *testing.T) {
	f := newFixture(t)
	mock := withTraffic(t, f)
	mock.ExpectQuery(regexp.QuoteMeta("GROUP BY subject, environment")).
		WillReturnRows(sqlmock.NewRows(trafficCols("subject", "environment")).AddRow(trafficRow(siteID, "dev")...))
	list := decodeBody[SiteList](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, base, nil}, userOwner))
	require.NotEmpty(t, list.Sites)
	require.NotNil(t, list.Sites[0].Traffic)
	assert.Equal(t, int64(60), list.Sites[0].Traffic.Requests)
	assert.Positive(t, list.Sites[0].Traffic.P95)
}

// TestReplicas pins criterion 4's per-replica view for an administrator.
func TestReplicas(t *testing.T) {
	f := newAdmin(t)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	f.rt.TrafficStore = &sitetraffic.Store{DB: db}
	mock.ExpectQuery(regexp.QuoteMeta("FROM site_traffic_replicas")).
		WillReturnRows(sqlmock.NewRows(trafficCols("instance")).AddRow(trafficRow("a:8080")...).AddRow(trafficRow("b:8080")...))
	rec := f.do(t, http.MethodGet, "/api/v1/admin/sites/traffic/replicas?range=24h", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeBody[ReplicasView](t, rec)
	require.Len(t, got.Replicas, 2)
	assert.Equal(t, "a:8080", got.Replicas[0].Instance)
	assert.Equal(t, int64(60), got.Replicas[1].Requests)
	assert.Equal(t, http.StatusBadRequest, f.do(t, http.MethodGet, "/api/v1/admin/sites/traffic/replicas?range=x", "").Code)
}

// A viewer granted one environment reads only that environment's traffic
// (#2647): another environment's figures are left out of the totals and the
// series, and asking for one they cannot open is answered as one that does
// not exist.
func TestTraffic_AViewerReadsOnlyTheEnvironmentsTheyOpen(t *testing.T) {
	f := newFixture(t)
	f.store.grants[siteID][userViewer.Email] = site.Grant{Email: userViewer.Email, Level: site.LevelView, Environments: []string{"stage"}}
	mock := withTraffic(t, f)
	mock.ExpectQuery(regexp.QuoteMeta("GROUP BY environment")).WillReturnRows(sqlmock.NewRows(trafficCols("environment")).
		AddRow(trafficRow("prod")...).AddRow(trafficRow("stage")...))
	mock.ExpectQuery(regexp.QuoteMeta("GROUP BY at")).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "stage", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows(trafficCols("at")).AddRow(trafficRow(time.Date(2026, 11, 14, 13, 0, 0, 0, time.UTC))...))

	got := decodeBody[TrafficView](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, siteBase + "/traffic?range=1h", nil}, userViewer))
	assert.NotContains(t, got.Environments, envProd)
	assert.Equal(t, int64(60), got.Environments["stage"].Requests)
	assert.Equal(t, int64(60), got.Total.Requests, "the totals are stage's alone")

	f.statusIs(t, http.StatusNotFound, route{http.MethodGet, siteBase + "/traffic?environment=" + envProd, nil}, userViewer)
}
