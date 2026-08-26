package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// A persona's API route rules survive a portal edit (#1479). Before they were
// carried here, saving a persona through this API wrote it back without the
// rules its file config gave it, and the endpoints those rules denied became
// callable with no warning anywhere.

const routeRulePath = "/v1/orders/{id}"

// mutableDeps builds the deps a write route needs, with a store to observe.
func mutableDeps(store *mockPersonaStore, personas ...*persona.Persona) Deps {
	return Deps{
		PersonaRegistry: &mockPersonaRegistry{allResult: personas},
		PersonaStore:    store,
		Config:          testConfig(),
		ConfigStore:     &mockConfigStore{mode: "database"},
	}
}

// sendJSON issues a request with a JSON body and returns the recorder.
func sendJSON(t *testing.T, h *Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(string(buf)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestCreatePersona_PersistsAPIRoutes(t *testing.T) {
	store := &mockPersonaStore{}
	h := NewHandler(mutableDeps(store), nil)

	w := sendJSON(t, h, http.MethodPost, "/api/v1/admin/personas", personaCreateRequest{
		Name: "analyst", DisplayName: "Analyst",
		AllowTools: []string{"*"},
		APIRoutes: []persona.APIRouteRule{
			{Connection: "crm-*", Methods: []string{"delete"}, Paths: []string{routeRulePath}, Action: persona.ActionDeny},
		},
	})
	require.Equal(t, http.StatusCreated, w.Code)

	require.Len(t, store.setCalls, 1)
	got := store.setCalls[0].APIRoutes
	require.Len(t, got, 1)
	assert.Equal(t, "crm-*", got[0].Connection)
	// Methods are stored uppercase: the toolkit uppercases an inbound method
	// before matching, and the patterns are compared case-sensitively, so a
	// rule typed in lower case would never match anything.
	assert.Equal(t, []string{http.MethodDelete}, got[0].Methods)
	// The path is stored exactly as written, placeholders included.
	assert.Equal(t, []string{routeRulePath}, got[0].Paths)
	assert.Equal(t, persona.ActionDeny, got[0].Action)

	var detail personaDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	assert.Equal(t, store.setCalls[0].APIRoutes, detail.APIRoutes)
}

func TestUpdatePersona_KeepsAPIRoutesTheEditorSentBack(t *testing.T) {
	store := &mockPersonaStore{}
	existing := &persona.Persona{Name: "analyst", DisplayName: "Analyst"}
	h := NewHandler(mutableDeps(store, existing), nil)

	rules := []persona.APIRouteRule{
		{Connection: "crm-prod", Paths: []string{"/v1/admin/*"}, Action: persona.ActionDeny},
	}
	w := sendJSON(t, h, http.MethodPut, "/api/v1/admin/personas/analyst", personaCreateRequest{
		DisplayName: "Analyst", AllowTools: []string{"*"}, APIRoutes: rules,
	})
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, store.setCalls, 1)
	require.Len(t, store.setCalls[0].APIRoutes, 1)
	// A hand-written glob round-trips as the glob it was typed as.
	assert.Equal(t, []string{"/v1/admin/*"}, store.setCalls[0].APIRoutes[0].Paths)
}

func TestUpdatePersona_WritesNoRuleWhenTheEditorSentNone(t *testing.T) {
	store := &mockPersonaStore{}
	h := NewHandler(mutableDeps(store, &persona.Persona{Name: "analyst", DisplayName: "Analyst"}), nil)

	w := sendJSON(t, h, http.MethodPut, "/api/v1/admin/personas/analyst", personaCreateRequest{
		DisplayName: "Analyst", AllowTools: []string{"*"},
	})
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, store.setCalls, 1)
	assert.Empty(t, store.setCalls[0].APIRoutes)
}

// The second acceptance criterion of #1479, through the API the editor uses: a
// persona whose rules came from the config file shows them on the read, and the
// save that follows stores the rules it was shown rather than dropping them.
func TestFilePersona_APIRoutesSurviveAReaderAndASave(t *testing.T) {
	cfg := testConfig()
	rules := []persona.APIRouteRule{
		{Connection: "crm-*", Methods: []string{http.MethodGet}, Action: persona.ActionAllow},
		{
			Connection: "crm-*", Methods: []string{http.MethodDelete},
			Paths: []string{routeRulePath}, Action: persona.ActionDeny,
		},
	}
	filePersona := &persona.Persona{
		Name: "analyst", DisplayName: "Analyst", Roles: []string{"analyst"},
		APIRoutes: rules, Source: platform.SourceFile,
	}
	store := &mockPersonaStore{}
	h := NewHandler(Deps{
		PersonaRegistry:  &mockPersonaRegistry{allResult: []*persona.Persona{filePersona}},
		PersonaStore:     store,
		Config:           cfg,
		ConfigStore:      &mockConfigStore{mode: "database"},
		FilePersonaNames: map[string]bool{"analyst": true},
	}, nil)

	// The editor opens the persona: the rules the file gave it are on the wire.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/personas/analyst", http.NoBody)
	read := httptest.NewRecorder()
	h.ServeHTTP(read, req)
	require.Equal(t, http.StatusOK, read.Code)
	var detail personaDetail
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &detail))
	require.Equal(t, rules, detail.APIRoutes, "the file's rules are absent from the editor's read")

	// The editor saves what it was given, changing something else entirely.
	saved := sendJSON(t, h, http.MethodPut, "/api/v1/admin/personas/analyst", personaCreateRequest{
		DisplayName: "Analyst (revised)",
		Roles:       detail.Roles,
		AllowTools:  detail.AllowTools,
		APIRoutes:   detail.APIRoutes,
	})
	require.Equal(t, http.StatusOK, saved.Code)
	require.Len(t, store.setCalls, 1)
	assert.Equal(t, rules, store.setCalls[0].APIRoutes,
		"editing the persona in the portal dropped the rules its file config gave it")
}

func TestCreatePersona_RejectsUnusableAPIRoutes(t *testing.T) {
	cases := []struct {
		name string
		rule persona.APIRouteRule
		want string
	}{
		{
			// A rule with no connection matches no connection, so storing it
			// would record a rule that can never take effect.
			name: "no connection",
			rule: persona.APIRouteRule{Methods: []string{http.MethodGet}},
			want: "Connection is required",
		},
		{
			name: "unknown action",
			rule: persona.APIRouteRule{Connection: "crm", Action: "audit"},
			want: "invalid action",
		},
		{
			// filepath.Match reports ErrBadPattern, which the matcher swallows
			// as "no match" — an unparseable deny rule reads as in force while
			// denying nothing. persona.Registry.Register refuses it too, but it
			// runs after the store write, so the check has to happen here.
			name: "unparseable path glob",
			rule: persona.APIRouteRule{Connection: "crm", Paths: []string{"/v1/[unclosed"}},
			want: "invalid glob",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &mockPersonaStore{}
			h := NewHandler(mutableDeps(store), nil)
			w := sendJSON(t, h, http.MethodPost, "/api/v1/admin/personas", personaCreateRequest{
				Name: "analyst", DisplayName: "Analyst", APIRoutes: []persona.APIRouteRule{c.rule},
			})
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, decodeProblem(w.Body.Bytes()).Detail, c.want)
			assert.Empty(t, store.setCalls, "an invalid rule must not reach the store")
		})
	}
}

func TestDeletePersona_RevertsToTheFileVersionWithItsAPIRoutes(t *testing.T) {
	cfg := testConfig()
	cfg.Personas.Definitions = map[string]platform.PersonaDef{
		"analyst": {
			DisplayName: "Analyst",
			Roles:       []string{"analyst"},
			APIRoutes: []platform.APIRouteDef{
				{Connection: "crm-*", Methods: []string{http.MethodDelete}, Action: persona.ActionDeny},
			},
		},
	}
	reg := &mockPersonaRegistry{allResult: []*persona.Persona{
		{Name: "analyst", DisplayName: "Analyst", Source: platform.SourceBoth},
	}}
	h := NewHandler(Deps{
		PersonaRegistry:  reg,
		PersonaStore:     &mockPersonaStore{},
		Config:           cfg,
		ConfigStore:      &mockConfigStore{mode: "database"},
		FilePersonaNames: map[string]bool{"analyst": true},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/admin/personas/analyst", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	reverted := reg.lastRegistered
	require.NotNil(t, reverted, "the file version was not re-registered")
	require.Len(t, reverted.APIRoutes, 1)
	assert.Equal(t, "crm-*", reverted.APIRoutes[0].Connection)
	assert.Equal(t, persona.ActionDeny, reverted.APIRoutes[0].Action)
}

func TestTestPersonaAccess_APIRouteCase(t *testing.T) {
	p := &persona.Persona{
		Name: "analyst",
		APIRoutes: []persona.APIRouteRule{
			{Connection: "crm-*", Methods: []string{http.MethodGet}},
			{Connection: "crm-*", Methods: []string{http.MethodDelete}, Paths: []string{routeRulePath}, Action: persona.ActionDeny},
		},
	}
	h := NewHandler(Deps{PersonaRegistry: &mockPersonaRegistry{allResult: []*persona.Persona{p}}}, nil)
	post := func(t *testing.T, body testPersonaAccessRequest) *httptest.ResponseRecorder {
		t.Helper()
		return sendJSON(t, h, http.MethodPost, "/api/v1/admin/personas/analyst/test-access", body)
	}

	t.Run("a deny rule answers with the rule that refused", func(t *testing.T) {
		w := post(t, testPersonaAccessRequest{
			Connection: "crm-prod", Method: "delete", Path: routeRulePath,
		})
		require.Equal(t, http.StatusOK, w.Code)
		var resp testPersonaAccessResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.False(t, resp.Allowed)
		assert.Equal(t, persona.AccessSourceDeny, resp.Source)
		require.NotNil(t, resp.MatchedRule)
		assert.Equal(t, []string{routeRulePath}, resp.MatchedRule.Paths)
	})

	t.Run("an allow rule answers with the rule that admitted", func(t *testing.T) {
		w := post(t, testPersonaAccessRequest{
			Connection: "crm-prod", Method: http.MethodGet, Path: routeRulePath,
		})
		require.Equal(t, http.StatusOK, w.Code)
		var resp testPersonaAccessResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Allowed)
		assert.Equal(t, persona.AccessSourceAllow, resp.Source)
		require.NotNil(t, resp.MatchedRule)
		assert.Equal(t, []string{http.MethodGet}, resp.MatchedRule.Methods)
	})

	t.Run("a connection no rule touches keeps full access", func(t *testing.T) {
		w := post(t, testPersonaAccessRequest{
			Connection: "billing", Method: http.MethodDelete, Path: routeRulePath,
		})
		require.Equal(t, http.StatusOK, w.Code)
		var resp testPersonaAccessResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Allowed)
		assert.Equal(t, persona.AccessSourceDefault, resp.Source)
		assert.Nil(t, resp.MatchedRule)
	})

	t.Run("rejects a route question missing its method or path", func(t *testing.T) {
		w := post(t, testPersonaAccessRequest{Connection: "crm-prod", Method: http.MethodGet})
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, decodeProblem(w.Body.Bytes()).Detail, "method and path are required")
	})

	t.Run("rejects a relative path", func(t *testing.T) {
		w := post(t, testPersonaAccessRequest{Connection: "crm-prod", Method: http.MethodGet, Path: "v1/orders"})
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, decodeProblem(w.Body.Bytes()).Detail, "path must start with /")
	})

	t.Run("rejects both questions at once", func(t *testing.T) {
		w := post(t, testPersonaAccessRequest{
			ToolName: "trino_query", Connection: "crm-prod", Method: http.MethodGet, Path: "/v1/orders",
		})
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, decodeProblem(w.Body.Bytes()).Detail, "not both")
	})
}

func TestNormalizeAPIRoutes(t *testing.T) {
	t.Run("a recognized action is stored in its canonical case", func(t *testing.T) {
		out := normalizeAPIRoutes([]persona.APIRouteRule{
			{Connection: "crm", Action: "ALLOW"},
			{Connection: "crm", Action: "Deny"},
		})
		require.Len(t, out, 2)
		assert.Equal(t, persona.ActionAllow, out[0].Action)
		assert.Equal(t, persona.ActionDeny, out[1].Action)
	})

	t.Run("an unrecognized action is passed through so validation refuses it", func(t *testing.T) {
		// Folding it into an allow here would store as a grant the request
		// that asked for something else.
		out := normalizeAPIRoutes([]persona.APIRouteRule{{Connection: "crm", Action: "audit"}})
		require.Len(t, out, 1)
		assert.Equal(t, "audit", out[0].Action)
		assert.Error(t, persona.ValidateAPIRoutes(out))
	})

	got := normalizeAPIRoutes([]persona.APIRouteRule{{
		Connection: "  crm-*  ",
		Methods:    []string{" get ", "", "post"},
		Paths:      []string{" /v1/orders/* ", ""},
	}})
	require.Len(t, got, 1)
	assert.Equal(t, "crm-*", got[0].Connection)
	assert.Equal(t, []string{http.MethodGet, http.MethodPost}, got[0].Methods)
	assert.Equal(t, []string{"/v1/orders/*"}, got[0].Paths)
	// An empty action is stored as the allow it has always meant.
	assert.Equal(t, persona.ActionAllow, got[0].Action)

	t.Run("an all-empty list stays nil so it reads as any", func(t *testing.T) {
		out := normalizeAPIRoutes([]persona.APIRouteRule{{Connection: "crm", Methods: []string{" ", ""}}})
		require.Len(t, out, 1)
		assert.Nil(t, out[0].Methods)
	})

	t.Run("no rules is no rules", func(t *testing.T) {
		assert.Nil(t, normalizeAPIRoutes(nil))
	})
}
