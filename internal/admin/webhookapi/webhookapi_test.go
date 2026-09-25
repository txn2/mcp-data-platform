package webhookapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whadmin"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
)

type fakeService struct {
	sources map[string]whsource.Source
	err     error
	created whsource.Source
	updated whadmin.Update
}

func (f *fakeService) List(context.Context) ([]whsource.Source, error) {
	out := make([]whsource.Source, 0, len(f.sources))
	for _, s := range f.sources {
		out = append(out, s)
	}
	return out, f.err
}

func (f *fakeService) Get(_ context.Context, name string) (whsource.Source, whstore.Status, error) {
	if f.err != nil {
		return whsource.Source{}, whstore.Status{}, f.err
	}
	s, ok := f.sources[name]
	if !ok {
		return whsource.Source{}, whstore.Status{}, whadmin.ErrNotFound
	}
	return s, whstore.Status{Pending: 1, Rejections: []whstore.Rejection{}}, nil
}

func (f *fakeService) Create(_ context.Context, s whsource.Source) (whsource.Source, error) {
	f.created = s
	return s, f.err
}

func (f *fakeService) Update(_ context.Context, name string, u whadmin.Update) (whsource.Source, error) {
	f.updated = u
	return f.sources[name], f.err
}

func (f *fakeService) Delete(context.Context, string) error { return f.err }

const base = sourcesPath

// wrapped counts the requests the admin authentication saw.
var wrapped int

func newMux(svc Service) *http.ServeMux {
	mux := http.NewServeMux()
	wrap := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped++
			h.ServeHTTP(w, r)
		})
	}
	Register(mux, wrap, Config{Service: svc, Author: func(*http.Request) string { return "admin@example.com" }})
	return mux
}

func do(mux http.Handler, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body)))
	return rec
}

var stored = whsource.Source{
	Name: "esp", Enabled: true, Connection: "scratch",
	Auth: whsource.Auth{
		Mode: whsource.AuthHMAC, Secret: "s3cret", PreviousSecret: "old",
		PreviousUntil: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), SignatureHeader: "X-Sig",
	},
}

func TestListAndGetNeverCarrySecrets(t *testing.T) {
	svc := &fakeService{sources: map[string]whsource.Source{"esp": stored}}
	mux := newMux(svc)

	rec := do(mux, http.MethodGet, base, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "s3cret")
	assert.NotContains(t, rec.Body.String(), `"old"`)
	var list SourceList
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Sources, 1)
	v := list.Sources[0]
	assert.True(t, v.Auth.SecretSet)
	require.NotNil(t, v.Auth.PreviousUntil)
	assert.Equal(t, "/hooks/esp", v.Path)
	assert.Equal(t, "webhook_esp", v.Table)

	rec = do(mux, http.MethodGet, base+"/esp", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "s3cret")
	var detail SourceDetail
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))
	assert.Equal(t, 1, detail.Status.Pending)

	assert.Equal(t, http.StatusNotFound, do(mux, http.MethodGet, base+"/nope", "").Code)

	empty := newMux(&fakeService{sources: map[string]whsource.Source{}})
	rec = do(empty, http.MethodGet, base, "")
	assert.JSONEq(t, `{"sources":[]}`, rec.Body.String(), "no sources is an empty list, never null")

	failing := newMux(&fakeService{err: errors.New("db down")})
	assert.Equal(t, http.StatusInternalServerError, do(failing, http.MethodGet, base, "").Code)
}

func TestCreate(t *testing.T) {
	svc := &fakeService{}
	mux := newMux(svc)
	rec := do(mux, http.MethodPost, base,
		`{"name":"esp","connection":"scratch","auth":{"mode":"hmac","secret":"k","signature_header":"X-Sig"},"config":{"buffer_limit":100}}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.True(t, svc.created.Enabled, "a source is enabled unless the body says otherwise")
	assert.Equal(t, "k", svc.created.Auth.Secret)
	assert.Equal(t, 100, svc.created.Config.BufferLimit)
	assert.Equal(t, "admin@example.com", svc.created.CreatedBy)
	assert.NotContains(t, rec.Body.String(), `"k"`)

	rec = do(mux, http.MethodPost, base, `{"name":"x","enabled":false,"auth":{"mode":"hmac"}}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.False(t, svc.created.Enabled)

	assert.Equal(t, http.StatusBadRequest, do(mux, http.MethodPost, base, `{"unknown":1}`).Code)
	assert.Equal(t, http.StatusBadRequest, do(mux, http.MethodPost, base, `{"name":"a"} {"name":"b"}`).Code)
	assert.NotZero(t, wrapped, "every route is behind the admin authentication")
}

func TestUpdateAndDelete(t *testing.T) {
	svc := &fakeService{sources: map[string]whsource.Source{"esp": stored}}
	mux := newMux(svc)
	rec := do(mux, http.MethodPut, base+"/esp",
		`{"enabled":false,"auth":{"mode":"hmac","secret":"new","signature_header":"X-Sig"},"rotation_overlap_seconds":3600}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, time.Hour, svc.updated.RotationOverlap)
	require.NotNil(t, svc.updated.Enabled)
	assert.False(t, *svc.updated.Enabled)
	assert.Equal(t, "new", svc.updated.Auth.Secret)
	assert.Equal(t, http.StatusBadRequest, do(mux, http.MethodPut, base+"/esp", `nope`).Code)

	assert.Equal(t, http.StatusNoContent, do(mux, http.MethodDelete, base+"/esp", "").Code)
}

func TestErrorMapping(t *testing.T) {
	cases := map[error]int{
		whadmin.ErrNotFound:                         http.StatusNotFound,
		whadmin.ErrExists:                           http.StatusConflict,
		fmt.Errorf("x: %w", whadmin.ErrNameTaken):   http.StatusConflict,
		fmt.Errorf("x: %w", whsource.ErrInvalid):    http.StatusBadRequest,
		fmt.Errorf("x: %w", whadmin.ErrUnusable):    http.StatusBadRequest,
		whtable.ErrNoScratchTarget:                  http.StatusBadRequest,
		whtable.ErrReadOnly:                         http.StatusBadRequest,
		errors.New("pq: connection refused secret"): http.StatusInternalServerError,
	}
	for err, want := range cases {
		mux := newMux(&fakeService{err: err, sources: map[string]whsource.Source{"esp": stored}})
		rec := do(mux, http.MethodDelete, base+"/esp", "")
		assert.Equal(t, want, rec.Code, err.Error())
		if want == http.StatusInternalServerError {
			assert.NotContains(t, rec.Body.String(), "pq:", "an unexpected failure is not echoed")
		}
	}
	mux := newMux(&fakeService{err: whadmin.ErrExists})
	assert.Equal(t, http.StatusConflict, do(mux, http.MethodPost, base, `{"name":"esp"}`).Code)
	mux = newMux(&fakeService{err: whadmin.ErrNotFound})
	assert.Equal(t, http.StatusNotFound, do(mux, http.MethodPut, base+"/esp", `{}`).Code)
}

func TestRegisterWithoutService(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{})
	assert.Equal(t, http.StatusNotFound, do(mux, http.MethodGet, base, "").Code)
}
