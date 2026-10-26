package botcheckapi

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

	"github.com/txn2/mcp-data-platform/internal/botcheck"
)

// memChecks is a Store over a map; used is which endpoints name each check,
// as the endpoints' foreign key would refuse a delete.
type memChecks struct {
	m    map[string]botcheck.Check
	used map[string][]string
	err  error
}

func newChecks(cs ...botcheck.Check) *memChecks {
	m := &memChecks{m: map[string]botcheck.Check{}, used: map[string][]string{}}
	for _, c := range cs {
		m.m[c.Name] = c
	}
	return m
}

func (m *memChecks) List(context.Context) ([]botcheck.Check, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := []botcheck.Check{}
	for _, name := range []string{"default", "forms", "signup"} {
		if c, ok := m.m[name]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *memChecks) Get(_ context.Context, name string) (botcheck.Check, error) {
	if m.err != nil {
		return botcheck.Check{}, m.err
	}
	c, ok := m.m[name]
	if !ok {
		return botcheck.Check{}, botcheck.ErrNotFound
	}
	return c, nil
}

func (m *memChecks) Put(_ context.Context, c botcheck.Check, author string) (botcheck.Check, error) {
	c.UpdatedBy = author
	m.m[c.Name] = c
	return c, nil
}

func (m *memChecks) Delete(_ context.Context, name string) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.m[name]; !ok {
		return botcheck.ErrNotFound
	}
	if len(m.used[name]) > 0 {
		return &botcheck.InUseError{Name: name, Endpoints: m.used[name]}
	}
	delete(m.m, name)
	return nil
}

func (m *memChecks) UsedBy(context.Context) (map[string][]string, error) { return m.used, nil }

func mux(store *memChecks, keyAvailable bool) *http.ServeMux {
	m := http.NewServeMux()
	Register(m, func(h http.Handler) http.Handler { return h }, Config{
		Checks: store, KeyAvailable: keyAvailable, Author: func(*http.Request) string { return "admin@example.com" },
		Secret: func(_ context.Context, name string) (string, error) {
			if name == "turnstile-key" {
				return "x", nil
			}
			return "", errors.New(`secret "` + name + `" does not exist`)
		},
	})
	return m
}

func do(m *http.ServeMux, method, path, body string) (rec *httptest.ResponseRecorder, out map[string]any) {
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body)))
	out = map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func TestList_EveryCheckWithTheEndpointsUsingIt(t *testing.T) {
	store := newChecks(botcheck.New("default"), botcheck.New("forms"))
	store.used["default"] = []string{"contact", "orders"}
	rec := httptest.NewRecorder()
	mux(store, false).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", listPath, http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
	var out List
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Checks, 2)
	assert.Equal(t, []string{"contact", "orders"}, out.Checks[0].UsedBy)
	assert.Equal(t, []string{}, out.Checks[1].UsedBy, "a check no endpoint names lists []")
	assert.False(t, out.Checks[0].Usable, "a proof of work with nothing to sign it")
	assert.False(t, out.KeyAvailable)

	store.err = errors.New("down")
	rec, _ = do(mux(store, true), "GET", listPath, "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// #2259: two proof-of-work checks are configured side by side, each with its
// own difficulty; a new one starts at the defaults.
func TestPut_CreatesAndUpdatesANamedCheck(t *testing.T) {
	store := newChecks(botcheck.New("default"))
	m := mux(store, true)
	rec, out := do(m, "PUT", listPath+"/forms", `{"difficulty":20,"max_difficulty":24}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "forms", out["name"])
	assert.InDelta(t, 20, out["difficulty"], 0)
	assert.InDelta(t, botcheck.DefaultRateThreshold, out["rate_threshold"], 0, "a new check takes the defaults it does not name")
	assert.Equal(t, "admin@example.com", out["updated_by"])
	assert.Equal(t, true, out["usable"])
	assert.Equal(t, botcheck.DefaultDifficulty, store.m["default"].Difficulty, "the other check is untouched")

	_, out = do(m, "PUT", listPath+"/forms", `{"rate_threshold":5}`)
	assert.InDelta(t, 20, out["difficulty"], 0, "an update keeps what it does not name")

	rec, out = do(m, "GET", listPath+"/forms", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.InDelta(t, 5, out["rate_threshold"], 0)
	rec, _ = do(m, "GET", listPath+"/gone", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPut_AProviderNeedsAUsableSecret(t *testing.T) {
	store := newChecks()
	m := mux(store, true)
	rec, _ := do(m, "PUT", listPath+"/signup", `{"provider":"turnstile","site_key":"1x00000000000000000000AA","secret_name":"missing"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `secret \"missing\" does not exist`)
	assert.Empty(t, store.m, "a refused write stores nothing")

	rec, out := do(m, "PUT", listPath+"/signup", `{"provider":"turnstile","site_key":"1x00000000000000000000AA","secret_name":"turnstile-key"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, botcheck.ProviderTurnstile, out["provider"])
	assert.Equal(t, true, out["usable"])
	assert.NotContains(t, rec.Body.String(), `"x"`, "the secret's value never appears")
}

func TestPut_Refusals(t *testing.T) {
	m := mux(newChecks(), true)
	for name, tc := range map[string]struct{ path, body string }{
		"not json":             {"/default", `{`},
		"unknown field":        {"/default", `{"providr":"pow"}`},
		"bad provider":         {"/default", `{"provider":"recaptcha"}`},
		"none is no provider":  {"/default", `{"provider":"none"}`},
		"the override is gone": {"/default", `{"allow_public_without_check":true}`},
		"bad name":             {"/Default", `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			rec, _ := do(m, "PUT", listPath+tc.path, tc.body)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
	rec, _ := do(mux(&memChecks{err: errors.New("down")}, true), "PUT", listPath+"/default", `{}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// #2259: a check an endpoint names is not deleted, and the refusal names the
// endpoint; one no endpoint names is.
func TestDelete_RefusesACheckInUseNamingTheEndpoints(t *testing.T) {
	store := newChecks(botcheck.New("default"), botcheck.New("forms"))
	store.used["default"] = []string{"contact"}
	m := mux(store, true)
	rec, _ := do(m, "DELETE", listPath+"/default", "")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "contact")
	assert.Contains(t, store.m, "default")

	rec, _ = do(m, "DELETE", listPath+"/forms", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.NotContains(t, store.m, "forms")
	rec, _ = do(m, "DELETE", listPath+"/forms", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = do(mux(&memChecks{err: errors.New("down")}, true), "DELETE", listPath+"/x", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestRegister_NothingWithoutAStore(t *testing.T) {
	m := http.NewServeMux()
	Register(m, func(h http.Handler) http.Handler { return h }, Config{})
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", listPath, http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
