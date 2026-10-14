package catalogscanapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/catalogscan"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
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

type fakeDocuments struct {
	docs    []catalog.SchemaDocument
	listErr error
	getErr  error
	asked   catalog.Ref
}

func (f *fakeDocuments) ListDocuments(context.Context) ([]catalog.SchemaDocument, error) {
	return f.docs, f.listErr
}

func (f *fakeDocuments) GetDocument(_ context.Context, ref catalog.Ref) (*catalog.SchemaDocument, error) {
	f.asked = ref
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &catalog.SchemaDocument{
		Filename: ref.Key, MIMEType: "application/json", Content: []byte(`{"openapi":"3.0.0"}`),
		UpdatedAt: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC),
	}, nil
}

func TestDocumentRoutes(t *testing.T) {
	docs := &fakeDocuments{docs: []catalog.SchemaDocument{{
		Reference: `mcp:schema_document:(api,billing,"openapi.json")`, ConnectionKind: "api", Connection: "billing",
		Filename: "openapi.json", MIMEType: "application/json", SizeBytes: 19, Version: 2,
	}}}
	cfg := Config{Store: &fakeStore{}, Documents: docs}

	rec := serve(t, cfg, http.MethodGet, documentsPath, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var list DocumentList
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Documents, 1)
	assert.Equal(t, 2, list.Documents[0].Version)
	assert.NotContains(t, rec.Body.String(), `"content"`, "a listing carries no content")

	rec = serve(t, cfg, http.MethodGet, documentsPath+"/api/billing/openapi.json", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, `{"openapi":"3.0.0"}`, rec.Body.String())
	assert.Equal(t, catalog.SchemaDocumentRef("api", "billing", "openapi.json"), docs.asked)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))

	docs.getErr = catalog.ErrNotFound
	rec = serve(t, cfg, http.MethodGet, documentsPath+"/api/billing/missing.json", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	docs.getErr = errBoom
	rec = serve(t, cfg, http.MethodGet, documentsPath+"/api/billing/openapi.json", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	docs.listErr = errBoom
	rec = serve(t, cfg, http.MethodGet, documentsPath, "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	rec = serve(t, Config{Store: &fakeStore{}}, http.MethodGet, documentsPath, "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "no document reader mounts no document routes")
}
