package botcheckapi

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

	"github.com/txn2/mcp-data-platform/internal/botcheck"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
)

type memSettings struct {
	v      *botcheck.Settings
	author string
	err    error
}

func (m *memSettings) Get(context.Context) (settingsjson.Stored[botcheck.Settings], error) {
	if m.err != nil {
		return settingsjson.Stored[botcheck.Settings]{}, m.err
	}
	if m.v == nil {
		return settingsjson.Stored[botcheck.Settings]{}, settingsjson.ErrNotFound
	}
	return settingsjson.Stored[botcheck.Settings]{Value: *m.v, UpdatedBy: m.author, UpdatedAt: time.Unix(1700000000, 0)}, nil
}

func (m *memSettings) Set(_ context.Context, v botcheck.Settings, author string) error {
	m.v, m.author = &v, author
	return nil
}

func mux(store *memSettings, keyAvailable bool) *http.ServeMux {
	m := http.NewServeMux()
	Register(m, func(h http.Handler) http.Handler { return h }, Config{
		Settings: store, KeyAvailable: keyAvailable, Author: func(*http.Request) string { return "admin@example.com" },
		Secret: func(_ context.Context, name string) (string, error) {
			if name == "turnstile-key" {
				return "x", nil
			}
			return "", errors.New(`secret "` + name + `" does not exist`)
		},
	})
	return m
}

func do(m *http.ServeMux, method, body string) (rec *httptest.ResponseRecorder, out map[string]any) {
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body)))
	out = map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func TestGet_TheDefaultIsProofOfWork(t *testing.T) {
	rec, out := do(mux(&memSettings{}, true), "GET", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, botcheck.ProviderPoW, out["provider"])
	assert.Equal(t, true, out["key_available"])
	assert.Equal(t, true, out["public_allowed"])

	_, out = do(mux(&memSettings{}, false), "GET", "")
	assert.Equal(t, false, out["public_allowed"], "proof of work with nothing to sign it is no check")
}

// #2173: the override lets a public endpoint run with no bot check.
func TestPut_NoneRefusesPublicUntilTheOverride(t *testing.T) {
	store := &memSettings{}
	m := mux(store, true)
	rec, out := do(m, "PUT", `{"provider":"none"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, false, out["public_allowed"])
	_, out = do(m, "PUT", `{"allow_public_without_check":true}`)
	assert.Equal(t, true, out["public_allowed"])
	assert.Equal(t, "admin@example.com", out["updated_by"])
}

func TestPut_AProviderNeedsAUsableSecret(t *testing.T) {
	store := &memSettings{}
	m := mux(store, true)
	rec, _ := do(m, "PUT", `{"provider":"turnstile","site_key":"1x00000000000000000000AA","secret_name":"missing"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `secret \"missing\" does not exist`)
	assert.Nil(t, store.v, "a refused write stores nothing")

	rec, out := do(m, "PUT", `{"provider":"turnstile","site_key":"1x00000000000000000000AA","secret_name":"turnstile-key"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, botcheck.ProviderTurnstile, out["provider"])
	assert.NotContains(t, rec.Body.String(), `"x"`, "the secret's value never appears")
}

func TestPut_Refusals(t *testing.T) {
	m := mux(&memSettings{}, true)
	for name, body := range map[string]string{
		"not json":      `{`,
		"unknown field": `{"providr":"pow"}`,
		"bad provider":  `{"provider":"recaptcha"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec, _ := do(m, "PUT", body)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
	broken := mux(&memSettings{err: errors.New("down")}, true)
	rec, _ := do(broken, "GET", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	rec, _ = do(broken, "PUT", `{}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestRegister_NothingWithoutAStore(t *testing.T) {
	m := http.NewServeMux()
	Register(m, func(h http.Handler) http.Handler { return h }, Config{})
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", path, http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
