package secretapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/secretstore"
	"github.com/txn2/mcp-data-platform/internal/totp"
)

// memStore models the real store's contract: Put validates, creates or
// keeps the value, and Get and List never carry one.
type memStore struct {
	values  map[string]string
	secrets map[string]secretstore.Secret
	fail    bool
}

func newMem() *memStore {
	return &memStore{values: map[string]string{}, secrets: map[string]secretstore.Secret{}}
}

func (m *memStore) List(context.Context) ([]secretstore.Secret, error) {
	if m.fail {
		return nil, errors.New("down")
	}
	out := make([]secretstore.Secret, 0, len(m.secrets))
	for _, s := range m.secrets {
		out = append(out, s)
	}
	return out, nil
}

func (m *memStore) Get(_ context.Context, name string) (secretstore.Secret, error) {
	s, ok := m.secrets[name]
	if !ok {
		return secretstore.Secret{}, secretstore.ErrNotFound
	}
	return s, nil
}

func (m *memStore) Put(_ context.Context, w secretstore.Write) (secretstore.Secret, bool, error) {
	if m.fail {
		return secretstore.Secret{}, false, errors.New("down")
	}
	_, exists := m.secrets[w.Name]
	if err := secretstore.Validate(w, !exists); err != nil {
		return secretstore.Secret{}, false, fmt.Errorf("put: %w", err)
	}
	if w.Value != nil {
		m.values[w.Name] = *w.Value
	}
	kind := w.Kind
	if kind == "" {
		kind = secretstore.KindValue
	}
	s := secretstore.Secret{Name: w.Name, Description: w.Description, Kind: kind, AllowConnections: w.AllowConnections, AllowPersonas: w.AllowPersonas, UpdatedBy: w.Actor}
	if kind == secretstore.KindTOTP {
		p := totp.Defaults()
		s.TOTP = &p
	}
	m.secrets[w.Name] = s
	return s, !exists, nil
}

func (m *memStore) Delete(_ context.Context, name string) error {
	if _, ok := m.secrets[name]; !ok {
		return secretstore.ErrNotFound
	}
	delete(m.secrets, name)
	return nil
}

func (m *memStore) Code(_ context.Context, name string) (secretstore.CurrentCode, error) {
	s, ok := m.secrets[name]
	switch {
	case !ok:
		return secretstore.CurrentCode{}, secretstore.ErrNotFound
	case s.Kind != secretstore.KindTOTP:
		return secretstore.CurrentCode{}, secretstore.ErrNotTOTP
	}
	return secretstore.CurrentCode{Code: "287082", SecondsLeft: 12, Params: *s.TOTP}, nil
}

func serve(t *testing.T, store Store) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{Store: store, Author: func(*http.Request) string { return "admin@example.com" }})
	return mux
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSecretRoutes(t *testing.T) {
	store := newMem()
	h := serve(t, store)

	rec := do(t, h, http.MethodPut, "/api/v1/admin/secrets/portal_password", `{"description":"vendor","value":"hunter22","allow_connections":["grid"],"allow_personas":[]}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "hunter22", "a write never echoes the value")
	assert.Equal(t, "admin@example.com", store.secrets["portal_password"].UpdatedBy)

	rec = do(t, h, http.MethodPut, "/api/v1/admin/secrets/portal_password", `{"description":"rescoped","allow_connections":["grid","vendor"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "hunter22", store.values["portal_password"], "a change without a value keeps it")

	rec = do(t, h, http.MethodGet, "/api/v1/admin/secrets/portal_password", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "hunter22")
	assert.NotContains(t, rec.Body.String(), `"value":`, "no value key")

	rec = do(t, h, http.MethodGet, "/api/v1/admin/secrets", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var list SecretList
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Secrets, 1)
	assert.NotContains(t, rec.Body.String(), "hunter22")

	assert.Equal(t, http.StatusNoContent, do(t, h, http.MethodDelete, "/api/v1/admin/secrets/portal_password", "").Code)
	assert.Equal(t, http.StatusNotFound, do(t, h, http.MethodDelete, "/api/v1/admin/secrets/portal_password", "").Code)
	assert.Equal(t, http.StatusNotFound, do(t, h, http.MethodGet, "/api/v1/admin/secrets/portal_password", "").Code)
}

func TestSecretRouteRefusals(t *testing.T) {
	h := serve(t, newMem())
	for body, want := range map[string]string{
		`{"value":"hunter22"}`:                                   "allow_connections",
		`{"value":"abc","allow_connections":["g"]}`:              "at least 6",
		`{"allow_connections":["g"]}`:                            "value is required",
		`{"bogus":1}`:                                            "not a valid secret",
		`{"value":"hunter22","allow_connections":["g"]} {"x":1}`: "more than one JSON value",
	} {
		rec := do(t, h, http.MethodPut, "/api/v1/admin/secrets/pw", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
		assert.Contains(t, rec.Body.String(), want, body)
	}
	broken := serve(t, &memStore{fail: true})
	assert.Equal(t, http.StatusInternalServerError, do(t, broken, http.MethodGet, "/api/v1/admin/secrets", "").Code)
	rec := do(t, broken, http.MethodPut, "/api/v1/admin/secrets/pw", `{"value":"hunter22","allow_connections":["g"]}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "down")

	mux := http.NewServeMux()
	Register(mux, nil, Config{})
	assert.Equal(t, http.StatusNotFound, do(t, mux, http.MethodGet, "/api/v1/admin/secrets", "").Code, "no store mounts nothing")
}

// An authenticator seed is saved with its kind, shown with its parameters and
// never its seed, and its current code is read without issuing it (#2065).
func TestAuthenticatorSeedRoutes(t *testing.T) {
	store := newMem()
	h := serve(t, store)
	const seed = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

	rec := do(t, h, http.MethodPut, "/api/v1/admin/secrets/vendor_mfa", `{"kind":"totp","value":"otpauth://totp/V:ops?secret=`+seed+`","allow_connections":["grid"]}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), seed)
	var sec secretstore.Secret
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sec))
	assert.Equal(t, secretstore.KindTOTP, sec.Kind)
	require.NotNil(t, sec.TOTP)

	rec = do(t, h, http.MethodGet, "/api/v1/admin/secrets/vendor_mfa/code", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var current secretstore.CurrentCode
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &current))
	assert.Equal(t, "287082", current.Code)
	assert.Equal(t, 12, current.SecondsLeft)
	assert.NotContains(t, rec.Body.String(), seed)

	for body, want := range map[string]string{
		`{"kind":"totp","value":"otpauth://hotp/V:x?secret=` + seed + `","allow_connections":["g"]}`: "hotp",
		`{"kind":"totp","value":"not!base32","allow_connections":["g"]}`:                             "not base32",
	} {
		rec = do(t, h, http.MethodPut, "/api/v1/admin/secrets/bad", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
		assert.Contains(t, rec.Body.String(), want, body)
	}

	require.Equal(t, http.StatusCreated, do(t, h, http.MethodPut, "/api/v1/admin/secrets/pw", `{"value":"hunter22","allow_connections":["g"]}`).Code)
	assert.Equal(t, http.StatusConflict, do(t, h, http.MethodGet, "/api/v1/admin/secrets/pw/code", "").Code)
	assert.Equal(t, http.StatusNotFound, do(t, h, http.MethodGet, "/api/v1/admin/secrets/none/code", "").Code)
}
