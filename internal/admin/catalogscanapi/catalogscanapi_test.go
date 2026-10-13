package catalogscanapi

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

	"github.com/txn2/mcp-data-platform/internal/platform/catalogscan"
)

var errBoom = errors.New("boom")

type fakeStore struct {
	set                  catalogscan.Settings
	saved                *catalogscan.Settings
	author               string
	getErr, setErr, oErr error
	outcomes             []catalogscan.Outcome
}

func (f *fakeStore) Get(context.Context) (catalogscan.Settings, error) { return f.set, f.getErr }
func (f *fakeStore) Set(_ context.Context, s catalogscan.Settings, author string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.saved, f.author, f.set = &s, author, s
	return nil
}

func (f *fakeStore) Outcomes(context.Context) ([]catalogscan.Outcome, error) {
	return f.outcomes, f.oErr
}

type fakeScanner struct {
	asked int
	by    string
	err   error
}

func (f *fakeScanner) ScanNow(_ context.Context, by string) error {
	f.asked++
	f.by = by
	return f.err
}

func passthrough(h http.Handler) http.Handler { return h }

func serve(t *testing.T, cfg Config, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, passthrough, cfg)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body)))
	return rec
}

func TestSettingsRoutes(t *testing.T) {
	st := &fakeStore{set: catalogscan.Defaults()}
	cfg := Config{Store: st, Author: func(*http.Request) string { return "admin@example.com" }}

	rec := serve(t, cfg, http.MethodGet, settingsPath, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"scan_interval":"6h0m0s","exclusions":[]}`, rec.Body.String())

	rec = serve(t, cfg, http.MethodPut, settingsPath,
		`{"scan_interval":"2h","exclusions":[{"connection_kind":"trino","connection":"w","entries":["hive.tmp"]}]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, st.saved)
	assert.Equal(t, "admin@example.com", st.author)
	assert.Equal(t, "2h", st.saved.ScanInterval)

	rec = serve(t, cfg, http.MethodPut, settingsPath, `{"scan_interval":"2h"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotNil(t, st.saved.Exclusions, "an omitted list is saved as empty, not null")

	for _, body := range []string{`{`, `{"bogus":1}`, `{"scan_interval":"1m"}`} {
		rec = serve(t, cfg, http.MethodPut, settingsPath, body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
	}
}

func TestSettingsRouteFailures(t *testing.T) {
	rec := serve(t, Config{Store: &fakeStore{getErr: errBoom}}, http.MethodGet, settingsPath, "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	rec = serve(t, Config{Store: &fakeStore{setErr: errBoom}}, http.MethodPut, settingsPath, `{"scan_interval":"6h"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	rec = serve(t, Config{Store: &fakeStore{getErr: errBoom}}, http.MethodPut, settingsPath, `{"scan_interval":"6h"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code, "the saved settings could not be read back")
}

func TestScanRoutes(t *testing.T) {
	st := &fakeStore{outcomes: []catalogscan.Outcome{{ConnectionKind: "trino", Connection: "w", Datasets: 4}}}
	sc := &fakeScanner{}
	rec := serve(t, Config{Store: st, Scanner: sc}, http.MethodGet, scansPath, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var list ScanList
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	assert.Equal(t, 4, list.Scans[0].Datasets)

	rec = serve(t, Config{Store: st, Scanner: sc}, http.MethodPost, scansPath, "")
	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, 1, sc.asked)

	rec = serve(t, Config{Store: st}, http.MethodPost, scansPath, "")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "no scanner, no request route")

	rec = serve(t, Config{Store: st, Scanner: &fakeScanner{err: errBoom}}, http.MethodPost, scansPath, "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	rec = serve(t, Config{Store: &fakeStore{oErr: errBoom}}, http.MethodGet, scansPath, "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	rec = serve(t, Config{}, http.MethodGet, scansPath, "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "no store mounts nothing")
}
