package mapsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/maps"
)

type fakeService struct {
	err       error
	saved     maps.SettingsInput
	added     maps.RegionInput
	author    string
	refreshed string
	deleted   string
}

func (f *fakeService) View(_ context.Context, bucketOf maps.ResolvedBucket) (maps.View, error) {
	v := maps.View{Regions: []maps.Region{}, Presets: maps.Presets()}
	if bucketOf != nil {
		v.Settings.ResolvedBucket = bucketOf(maps.Settings{})
	}
	return v, f.err
}

func (f *fakeService) SaveSettings(_ context.Context, in maps.SettingsInput, author string) error {
	f.saved, f.author = in, author
	return f.err
}

func (f *fakeService) AddRegion(_ context.Context, in maps.RegionInput, author string) (*maps.Region, error) {
	f.added, f.author = in, author
	if f.err != nil {
		return nil, f.err
	}
	return &maps.Region{ID: "sf", State: maps.StateQueued}, nil
}

func (f *fakeService) RefreshRegion(_ context.Context, id string) (*maps.Region, error) {
	f.refreshed = id
	if f.err != nil {
		return nil, f.err
	}
	return &maps.Region{ID: id, State: maps.StateQueued}, nil
}

func (f *fakeService) DeleteRegion(_ context.Context, id string) error {
	f.deleted = id
	return f.err
}

func (f *fakeService) Estimate(_ context.Context, _ maps.EstimateInput) (maps.Estimate, error) {
	return maps.Estimate{SizeBytes: 42}, f.err
}

func setup(svc Service) http.Handler {
	mux := http.NewServeMux()
	passthrough := func(h http.Handler) http.Handler { return h }
	Register(mux, passthrough, Config{
		Service: svc, BucketOf: func(maps.Settings) string { return "managed-resources" },
		Author: func(*http.Request) string { return "admin@example.com" },
	})
	return mux
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body)))
	return rec
}

func TestRoutes(t *testing.T) {
	f := &fakeService{}
	h := setup(f)

	rec := do(t, h, http.MethodGet, mapsPath, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var v maps.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.Equal(t, "managed-resources", v.Settings.ResolvedBucket)

	rec = do(t, h, http.MethodPut, mapsPath, `{"enabled":true,"max_zoom":12}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, f.saved.Enabled)
	assert.Equal(t, "admin@example.com", f.author)

	rec = do(t, h, http.MethodPost, mapsPath+"/regions", `{"preset":"united-states"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "united-states", f.added.Preset)

	rec = do(t, h, http.MethodPost, mapsPath+"/regions/sf/refresh", "")
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, "sf", f.refreshed)

	rec = do(t, h, http.MethodDelete, mapsPath+"/regions/sf", "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "sf", f.deleted)

	rec = do(t, h, http.MethodPost, mapsPath+"/estimate", `{"preset":"united-states"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"size_bytes":42`)
}

func TestBodiesAreRefused(t *testing.T) {
	h := setup(&fakeService{})
	for _, body := range []string{`{"enabled":"yes"}`, `{"unknown":1}`, `{} {}`, `not json`} {
		rec := do(t, h, http.MethodPut, mapsPath, body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
	}
	rec := do(t, h, http.MethodPost, mapsPath+"/regions", `{"bounds":"x"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = do(t, h, http.MethodPost, mapsPath+"/estimate", `[]`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestErrorsMapToStatuses(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		body   string
	}{
		{maps.ErrRegionNotFound, http.StatusNotFound, "region not found"},
		{maps.ErrRegionBusy, http.StatusConflict, "being fetched"},
		{errors.New("database down"), http.StatusInternalServerError, "see the server log"},
	} {
		h := setup(&fakeService{err: tc.err})
		rec := do(t, h, http.MethodPost, mapsPath+"/regions/sf/refresh", "")
		assert.Equal(t, tc.status, rec.Code)
		assert.Contains(t, rec.Body.String(), tc.body)
		assert.NotContains(t, rec.Body.String(), "database down", "an internal failure is not returned")
	}

	h := setup(&fakeService{err: errors.New("down")})
	for _, req := range [][3]string{
		{http.MethodGet, mapsPath, ""},
		{http.MethodPut, mapsPath, `{}`},
		{http.MethodPost, mapsPath + "/regions", `{}`},
		{http.MethodDelete, mapsPath + "/regions/sf", ""},
	} {
		assert.Equal(t, http.StatusInternalServerError, do(t, h, req[0], req[1], req[2]).Code, req[1])
	}
	rec := do(t, h, http.MethodPost, mapsPath+"/estimate", `{}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "the source build could not be read: down")
}

func TestInputErrorsAreReturnedAsWritten(t *testing.T) {
	_, inErr := (&maps.Service{}).Estimate(context.Background(), maps.EstimateInput{Preset: "atlantis"})
	require.Error(t, inErr)
	h := setup(&fakeService{err: inErr})
	rec := do(t, h, http.MethodPost, mapsPath+"/regions", `{"preset":"atlantis"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `unknown preset`)
	rec = do(t, h, http.MethodPost, mapsPath+"/estimate", `{"preset":"atlantis"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRegisterWithoutAServiceMountsNothing(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, nil, Config{})
	assert.Equal(t, http.StatusNotFound, do(t, mux, http.MethodGet, mapsPath, "").Code)
}
