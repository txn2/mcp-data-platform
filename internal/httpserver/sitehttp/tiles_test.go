package sitehttp

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitetile"
)

// TestTile: an environment's tile of the release it serves is answered to
// someone who may open the environment; a tile of another release, an
// environment with no release or offline, one the caller may not open and a
// deployment with no tile store answer 404 (#2658).
func TestTile(t *testing.T) {
	f := newFixture(t)
	st := f.store.sites[siteID]
	st.Environments[0].Release = 4
	st.Environments[2].Release = 2
	st.Environments[2].Status = site.StatusOffline
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	f.rt.Tiles = sitetile.NewStore(db)
	key := sitetile.Key("p", siteID, site.EnvDev, 4)
	require.NoError(t, f.objects.PutObject(context.Background(), "portal", key, []byte("PNG"), "image/png"))
	tileOf := func(release int) {
		mock.ExpectQuery(regexp.QuoteMeta("SELECT drawn_release, tile_key, failure FROM site_tiles")).
			WillReturnRows(sqlmock.NewRows([]string{"r", "k", "f"}).AddRow(release, key, ""))
	}
	path := base + "/" + siteName + "/environments/"

	tileOf(4)
	rec := f.statusIs(t, http.StatusOK, route{http.MethodGet, path + site.EnvDev + "/tile", nil}, userOwner)
	assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))
	assert.Equal(t, "PNG", rec.Body.String())

	tileOf(3)
	f.statusIs(t, http.StatusNotFound, route{http.MethodGet, path + site.EnvDev + "/tile", nil}, userOwner)
	f.statusIs(t, http.StatusNotFound, route{http.MethodGet, path + "stage/tile", nil}, userOwner)
	f.statusIs(t, http.StatusNotFound, route{http.MethodGet, path + envProd + "/tile", nil}, userOwner)
	f.statusIs(t, http.StatusNotFound, route{http.MethodGet, path + site.EnvDev + "/tile", nil}, userStranger)
	f.rt.Tiles = nil
	f.statusIs(t, http.StatusNotFound, route{http.MethodGet, path + site.EnvDev + "/tile", nil}, userOwner)
}

// TestTile_AMissingObjectIsDrawnAgain: a recorded tile whose object is gone
// (the capacity pass removed it, #2701) answers 404 and clears the row so the
// worker draws it again; a read that fails for another reason keeps the row.
func TestTile_AMissingObjectIsDrawnAgain(t *testing.T) {
	f := newFixture(t)
	f.store.sites[siteID].Environments[0].Release = 4
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	f.rt.Tiles = sitetile.NewStore(db)
	key := sitetile.Key("p", siteID, site.EnvDev, 4)
	path := base + "/" + siteName + "/environments/" + site.EnvDev + "/tile"

	mock.ExpectQuery(regexp.QuoteMeta("SELECT drawn_release, tile_key, failure FROM site_tiles")).
		WillReturnRows(sqlmock.NewRows([]string{"r", "k", "f"}).AddRow(4, key, ""))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE site_tiles SET drawn_release = NULL")).
		WithArgs(siteID, site.EnvDev, key).WillReturnResult(sqlmock.NewResult(0, 1))
	f.statusIs(t, http.StatusNotFound, route{http.MethodGet, path, nil}, userOwner)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT drawn_release, tile_key, failure FROM site_tiles")).
		WillReturnRows(sqlmock.NewRows([]string{"r", "k", "f"}).AddRow(4, key, ""))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE site_tiles SET drawn_release = NULL")).WillReturnError(errors.New("db down"))
	f.statusIs(t, http.StatusNotFound, route{http.MethodGet, path, nil}, userOwner)
	require.NoError(t, mock.ExpectationsWereMet())

	f.objects.getErr = errors.New("connection refused")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT drawn_release, tile_key, failure FROM site_tiles")).
		WillReturnRows(sqlmock.NewRows([]string{"r", "k", "f"}).AddRow(4, key, ""))
	f.statusIs(t, http.StatusNotFound, route{http.MethodGet, path, nil}, userOwner)
	require.NoError(t, mock.ExpectationsWereMet(), "a failure other than a missing object keeps the row")
}
