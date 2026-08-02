package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/platform/personastore"
)

const testPriority = 10

func TestListPersonas(t *testing.T) {
	t.Run("returns sorted persona summaries", func(t *testing.T) {
		pReg := &mockPersonaRegistry{
			allResult: []*persona.Persona{
				{
					Name:        "admin",
					DisplayName: "Administrator",
					Roles:       []string{"admin"},
					Tools:       persona.ToolRules{Allow: []string{"*"}},
				},
				{
					Name:        "analyst",
					DisplayName: "Data Analyst",
					Roles:       []string{"analyst"},
					Tools:       persona.ToolRules{Allow: []string{"trino_*"}},
				},
			},
		}
		tkReg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", tools: []string{"trino_query", "trino_describe_table"}},
			},
		}
		h := NewHandler(Deps{PersonaRegistry: pReg, ToolkitRegistry: tkReg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/personas", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, float64(2), body["total"])

		personas, ok := body["personas"].([]any)
		require.True(t, ok, "personas should be a slice")
		// Sorted by name: admin < analyst
		first, ok := personas[0].(map[string]any)
		require.True(t, ok, "first persona should be a map")
		assert.Equal(t, "admin", first["name"])
		// admin has Allow: ["*"] which matches all tools → 2
		assert.Equal(t, float64(2), first["tool_count"])

		second, ok := personas[1].(map[string]any)
		require.True(t, ok, "second persona should be a map")
		assert.Equal(t, "analyst", second["name"])
		// analyst has Allow: ["trino_*"] which matches trino_query and trino_describe_table → 2
		assert.Equal(t, float64(2), second["tool_count"])
	})

	t.Run("returns zero tool_count without toolkit registry", func(t *testing.T) {
		pReg := &mockPersonaRegistry{
			allResult: testPersonas("admin"),
		}
		h := NewHandler(Deps{PersonaRegistry: pReg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/personas", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		personas, ok := body["personas"].([]any)
		require.True(t, ok, "personas should be a slice")
		first, ok := personas[0].(map[string]any)
		require.True(t, ok, "first persona should be a map")
		assert.Equal(t, float64(0), first["tool_count"])
	})

	// A persona registered with nil Roles (e.g. a YAML config without the
	// roles: key) must serialize as "roles":[], not "roles":null, or the
	// RolesPage / PersonasPanel UI crashes when iterating p.roles.
	t.Run("nil Roles serializes as empty array, not null", func(t *testing.T) {
		pReg := &mockPersonaRegistry{
			allResult: []*persona.Persona{
				{Name: "minimal", DisplayName: "Minimal", Roles: nil},
			},
		}
		h := NewHandler(Deps{PersonaRegistry: pReg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/personas", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		raw := w.Body.String()
		assert.Contains(t, raw, `"roles":[]`, "roles must be []; got: %s", raw)
		assert.NotContains(t, raw, `"roles":null`, "roles must never be null on the wire")
	})
}

func TestGetPersona(t *testing.T) {
	t.Run("returns persona with resolved tools", func(t *testing.T) {
		p := &persona.Persona{
			Name:        "analyst",
			DisplayName: "Data Analyst",
			Description: "Analyze data",
			Roles:       []string{"analyst"},
			Priority:    testPriority,
			Tools: persona.ToolRules{
				Allow: []string{"trino_*", "datahub_search"},
				Deny:  []string{"trino_explain"},
			},
		}
		pReg := &mockPersonaRegistry{
			allResult: []*persona.Persona{p},
		}
		tkReg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", tools: []string{"trino_query", "trino_explain"}},
				{kind: "datahub", name: "primary", tools: []string{"datahub_search", "datahub_get_entity"}},
			},
		}
		h := NewHandler(Deps{PersonaRegistry: pReg, ToolkitRegistry: tkReg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/personas/analyst", http.NoBody)
		req.SetPathValue("name", "analyst")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body personaDetail
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "analyst", body.Name)
		assert.Equal(t, "Data Analyst", body.DisplayName)
		assert.Equal(t, 10, body.Priority)
		// trino_query allowed (trino_*), trino_explain denied, datahub_search allowed explicitly
		assert.Contains(t, body.Tools, "trino_query")
		assert.Contains(t, body.Tools, "datahub_search")
		assert.NotContains(t, body.Tools, "trino_explain")
		assert.NotContains(t, body.Tools, "datahub_get_entity")
	})

	t.Run("returns 404 for unknown persona", func(t *testing.T) {
		pReg := &mockPersonaRegistry{allResult: testPersonas("admin")}
		h := NewHandler(Deps{PersonaRegistry: pReg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/personas/unknown", http.NoBody)
		req.SetPathValue("name", "unknown")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "persona not found", pd.Detail)
	})

	t.Run("returns empty tools list without toolkit registry", func(t *testing.T) {
		p := &persona.Persona{
			Name:  "admin",
			Roles: []string{"admin"},
			Tools: persona.ToolRules{Allow: []string{"*"}},
		}
		pReg := &mockPersonaRegistry{allResult: []*persona.Persona{p}}
		h := NewHandler(Deps{PersonaRegistry: pReg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/personas/admin", http.NoBody)
		req.SetPathValue("name", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body personaDetail
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.NotNil(t, body.Tools)
		assert.Len(t, body.Tools, 0)
	})

	t.Run("returns context overrides nested under context key", func(t *testing.T) {
		p := &persona.Persona{
			Name:        "analyst",
			DisplayName: "Data Analyst",
			Roles:       []string{"analyst"},
			Tools:       persona.ToolRules{Allow: []string{"*"}},
			Context: persona.ContextOverrides{
				DescriptionPrefix:       "You are helping a data analyst.",
				AgentInstructionsSuffix: "Always explain SQL queries.",
			},
		}
		pReg := &mockPersonaRegistry{allResult: []*persona.Persona{p}}
		h := NewHandler(Deps{PersonaRegistry: pReg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/personas/analyst", http.NoBody)
		req.SetPathValue("name", "analyst")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify the JSON structure has context as a nested object
		var raw map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
		ctxObj, ok := raw["context"].(map[string]any)
		require.True(t, ok, "context must be a nested JSON object")
		assert.Equal(t, "You are helping a data analyst.", ctxObj["description_prefix"])
		assert.Equal(t, "Always explain SQL queries.", ctxObj["agent_instructions_suffix"])

		// Also verify typed decode
		var body personaDetail
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.NotNil(t, body.Context)
		assert.Equal(t, "You are helping a data analyst.", body.Context.DescriptionPrefix)
		assert.Equal(t, "Always explain SQL queries.", body.Context.AgentInstructionsSuffix)
		assert.Empty(t, body.Context.DescriptionOverride)
		assert.Empty(t, body.Context.AgentInstructionsOverride)
	})

	t.Run("omits context key when no overrides set", func(t *testing.T) {
		p := &persona.Persona{
			Name:  "admin",
			Roles: []string{"admin"},
			Tools: persona.ToolRules{Allow: []string{"*"}},
		}
		pReg := &mockPersonaRegistry{allResult: []*persona.Persona{p}}
		h := NewHandler(Deps{PersonaRegistry: pReg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/personas/admin", http.NoBody)
		req.SetPathValue("name", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var raw map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
		_, hasContext := raw["context"]
		assert.False(t, hasContext, "context key should be omitted when empty")
	})

	// A persona built from a YAML config that omits roles/allow_tools/
	// deny_tools must still serialize the required array fields as [].
	// The PersonaEditor UI does draft.allowTools.filter(...) and the
	// like; a null on the wire crashes the editor on open.
	t.Run("nil slice fields serialize as empty arrays, not null", func(t *testing.T) {
		p := &persona.Persona{
			Name:        "minimal",
			DisplayName: "Minimal",
			Roles:       nil,
			Tools:       persona.ToolRules{Allow: nil, Deny: nil},
		}
		pReg := &mockPersonaRegistry{allResult: []*persona.Persona{p}}
		h := NewHandler(Deps{PersonaRegistry: pReg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/personas/minimal", http.NoBody)
		req.SetPathValue("name", "minimal")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		raw := w.Body.String()
		for _, field := range []string{"roles", "allow_tools", "deny_tools", "tools"} {
			assert.Contains(t, raw, fmt.Sprintf("%q:[]", field), "%s must be []; got: %s", field, raw)
			assert.NotContains(t, raw, fmt.Sprintf("%q:null", field), "%s must never be null on the wire", field)
		}
	})
}

func TestCreatePersona(t *testing.T) {
	t.Run("creates persona successfully", func(t *testing.T) {
		pReg := &mockPersonaRegistry{allResult: testPersonas("admin")}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{PersonaRegistry: pReg, Config: testConfig(), ConfigStore: cs}, nil)

		body := `{"name":"analyst","display_name":"Data Analyst","roles":["analyst"],"allow_tools":["trino_*"]}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/personas", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		var resp personaDetail
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "analyst", resp.Name)
		assert.Equal(t, "Data Analyst", resp.DisplayName)
	})

	t.Run("rejects duplicate name", func(t *testing.T) {
		pReg := &mockPersonaRegistry{allResult: testPersonas("admin")}
		h := NewHandler(Deps{PersonaRegistry: pReg, Config: testConfig(), ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		body := `{"name":"admin","display_name":"New Admin","roles":["admin"]}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/personas", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "persona already exists", pd.Detail)
	})

	t.Run("rejects missing name", func(t *testing.T) {
		pReg := &mockPersonaRegistry{}
		h := NewHandler(Deps{PersonaRegistry: pReg, Config: testConfig(), ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		body := `{"display_name":"No Name"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/personas", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "name is required", pd.Detail)
	})

	t.Run("rejects missing display_name", func(t *testing.T) {
		pReg := &mockPersonaRegistry{}
		h := NewHandler(Deps{PersonaRegistry: pReg, Config: testConfig(), ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		body := `{"name":"test"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/personas", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "display_name is required", pd.Detail)
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		pReg := &mockPersonaRegistry{}
		h := NewHandler(Deps{PersonaRegistry: pReg, Config: testConfig(), ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/personas", strings.NewReader("{bad"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	// Issue #923: an authorization-defining resource must never silently drop a
	// mis-named field. The nested YAML-config shape {"tools":{"allow":[...]}}
	// would otherwise create a deny-all persona (grants dropped) with a 201, and
	// a typo'd deny key ("deny_tols") would create a MORE permissive persona
	// than intended. Strict decoding turns both into a named 400.
	t.Run("rejects nested tools field from YAML-config shape", func(t *testing.T) {
		pReg := &mockPersonaRegistry{}
		h := NewHandler(Deps{PersonaRegistry: pReg, Config: testConfig(), ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		body := `{"name":"analyst","display_name":"Data Analyst","roles":["analyst"],"tools":{"allow":["trino_*"],"deny":["*_delete_*"]}}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/personas", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "unknown field")
		assert.Contains(t, pd.Detail, `"tools"`)
	})

	t.Run("rejects typo'd deny_tols key", func(t *testing.T) {
		pReg := &mockPersonaRegistry{}
		h := NewHandler(Deps{PersonaRegistry: pReg, Config: testConfig(), ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		body := `{"name":"analyst","display_name":"Data Analyst","roles":["analyst"],"allow_tools":["*"],"deny_tols":["trino_execute"]}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/personas", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "unknown field")
		assert.Contains(t, pd.Detail, `"deny_tols"`)
	})
}

func TestUpdatePersona(t *testing.T) {
	t.Run("updates persona successfully", func(t *testing.T) {
		pReg := &mockPersonaRegistry{allResult: testPersonas("analyst")}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{PersonaRegistry: pReg, Config: testConfig(), ConfigStore: cs}, nil)

		body := `{"display_name":"Updated Analyst","roles":["analyst","viewer"]}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/personas/analyst", strings.NewReader(body))
		req.SetPathValue("name", "analyst")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp personaDetail
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "analyst", resp.Name)
		assert.Equal(t, "Updated Analyst", resp.DisplayName)
	})

	t.Run("rejects missing display_name", func(t *testing.T) {
		pReg := &mockPersonaRegistry{allResult: testPersonas("analyst")}
		h := NewHandler(Deps{PersonaRegistry: pReg, Config: testConfig(), ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		body := `{"roles":["analyst"]}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/personas/analyst", strings.NewReader(body))
		req.SetPathValue("name", "analyst")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		pReg := &mockPersonaRegistry{}
		h := NewHandler(Deps{PersonaRegistry: pReg, Config: testConfig(), ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/personas/test", strings.NewReader("{bad"))
		req.SetPathValue("name", "test")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestDeletePersona(t *testing.T) {
	t.Run("deletes persona successfully", func(t *testing.T) {
		pReg := &mockPersonaRegistry{allResult: testPersonas("admin", "analyst")}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{PersonaRegistry: pReg, Config: testConfig(), ConfigStore: cs}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/personas/analyst", http.NoBody)
		req.SetPathValue("name", "analyst")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "deleted", resp["status"])
	})

	t.Run("returns 404 for non-existent persona", func(t *testing.T) {
		pReg := &mockPersonaRegistry{allResult: testPersonas("admin")}
		h := NewHandler(Deps{PersonaRegistry: pReg, Config: testConfig(), ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/personas/nonexistent", http.NoBody)
		req.SetPathValue("name", "nonexistent")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("blocks deletion of admin persona", func(t *testing.T) {
		pReg := &mockPersonaRegistry{allResult: testPersonas("admin")}
		h := NewHandler(Deps{PersonaRegistry: pReg, Config: testConfig(), ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/personas/admin", http.NoBody)
		req.SetPathValue("name", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "cannot delete the admin persona", pd.Detail)
	})
}

func TestCreatePersonaWithStore(t *testing.T) {
	pReg := &mockPersonaRegistry{allResult: testPersonas("admin")}
	cs := &mockConfigStore{mode: "database"}
	ps := &mockPersonaStore{}
	h := NewHandler(Deps{
		PersonaRegistry: pReg,
		Config:          testConfig(),
		ConfigStore:     cs,
		PersonaStore:    ps,
	}, nil)

	body := `{"name":"analyst","display_name":"Data Analyst","roles":["analyst"],"allow_tools":["trino_*"]}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/personas", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	require.Len(t, ps.setCalls, 1, "PersonaStore.Set should be called once")
	assert.Equal(t, "analyst", ps.setCalls[0].Name)
	assert.Equal(t, "Data Analyst", ps.setCalls[0].DisplayName)
	assert.Equal(t, []string{"analyst"}, ps.setCalls[0].Roles)
}

func TestUpdatePersonaWithStore(t *testing.T) {
	pReg := &mockPersonaRegistry{allResult: testPersonas("analyst")}
	cs := &mockConfigStore{mode: "database"}
	ps := &mockPersonaStore{}
	h := NewHandler(Deps{
		PersonaRegistry: pReg,
		Config:          testConfig(),
		ConfigStore:     cs,
		PersonaStore:    ps,
	}, nil)

	body := `{"display_name":"Updated Analyst","roles":["analyst","viewer"]}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/personas/analyst", strings.NewReader(body))
	req.SetPathValue("name", "analyst")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.Len(t, ps.setCalls, 1, "PersonaStore.Set should be called once")
	assert.Equal(t, "analyst", ps.setCalls[0].Name)
	assert.Equal(t, "Updated Analyst", ps.setCalls[0].DisplayName)
}

func TestDeletePersonaWithStore(t *testing.T) {
	pReg := &mockPersonaRegistry{allResult: testPersonas("admin", "analyst")}
	cs := &mockConfigStore{mode: "database"}
	ps := &mockPersonaStore{}
	h := NewHandler(Deps{
		PersonaRegistry: pReg,
		Config:          testConfig(),
		ConfigStore:     cs,
		PersonaStore:    ps,
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/personas/analyst", http.NoBody)
	req.SetPathValue("name", "analyst")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.Len(t, ps.deleteCalls, 1, "PersonaStore.Delete should be called once")
	assert.Equal(t, "analyst", ps.deleteCalls[0])
}

func TestCreatePersonaWithStoreError(t *testing.T) {
	pReg := &mockPersonaRegistry{allResult: testPersonas("admin")}
	cs := &mockConfigStore{mode: "database"}
	ps := &mockPersonaStore{setErr: fmt.Errorf("database connection lost")}
	h := NewHandler(Deps{
		PersonaRegistry: pReg,
		Config:          testConfig(),
		ConfigStore:     cs,
		PersonaStore:    ps,
	}, nil)

	body := `{"name":"analyst","display_name":"Data Analyst","roles":["analyst"],"allow_tools":["trino_*"]}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/personas", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Store error should fail the request — DB-first two-phase commit
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	pd := decodeProblem(w.Body.Bytes())
	assert.Equal(t, "failed to persist persona", pd.Detail)
	// Store was called (and failed)
	require.Len(t, ps.setCalls, 1)
	// Registry should NOT have been updated
	assert.Equal(t, 0, pReg.registerCalled)
}

func TestUpdatePersonaWithStoreError(t *testing.T) {
	pReg := &mockPersonaRegistry{allResult: testPersonas("admin", "analyst")}
	cs := &mockConfigStore{mode: "database"}
	ps := &mockPersonaStore{setErr: fmt.Errorf("database connection lost")}
	h := NewHandler(Deps{
		PersonaRegistry: pReg,
		Config:          testConfig(),
		ConfigStore:     cs,
		PersonaStore:    ps,
	}, nil)

	body := `{"name":"analyst","display_name":"Data Analyst","roles":["analyst"],"allow_tools":["trino_*"]}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/personas/analyst", strings.NewReader(body))
	req.SetPathValue("name", "analyst")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Store error should fail the request — DB-first two-phase commit.
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	pd := decodeProblem(w.Body.Bytes())
	assert.Equal(t, "failed to persist persona", pd.Detail)
	require.Len(t, ps.setCalls, 1)
	// Registry should NOT have been updated on a persist failure.
	assert.Equal(t, 0, pReg.registerCalled)
}

func TestDeletePersonaWithStoreError(t *testing.T) {
	pReg := &mockPersonaRegistry{allResult: testPersonas("admin", "analyst")}
	cs := &mockConfigStore{mode: "database"}
	ps := &mockPersonaStore{deleteErr: fmt.Errorf("database connection lost")}
	h := NewHandler(Deps{
		PersonaRegistry: pReg,
		Config:          testConfig(),
		ConfigStore:     cs,
		PersonaStore:    ps,
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/personas/analyst", http.NoBody)
	req.SetPathValue("name", "analyst")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Store error should fail the request — DB-first two-phase commit
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	pd := decodeProblem(w.Body.Bytes())
	assert.Equal(t, "failed to delete persona from database", pd.Detail)
	// Store was called (and failed)
	require.Len(t, ps.deleteCalls, 1)
	// Registry should NOT have been updated — analyst should still exist
	_, exists := pReg.Get("analyst")
	assert.True(t, exists, "analyst persona should still exist in registry")
}

func TestPersonaSourceTracking(t *testing.T) {
	t.Run("create sets source to database", func(t *testing.T) {
		pReg := &mockPersonaRegistry{allResult: testPersonas("admin")}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{PersonaRegistry: pReg, Config: testConfig(), ConfigStore: cs}, nil)

		body := `{"name":"new-persona","display_name":"New","roles":["viewer"],"allow_tools":["*"]}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/personas", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		var detail personaDetail
		require.NoError(t, json.NewDecoder(w.Body).Decode(&detail))
		assert.Equal(t, "database", detail.Source)
	})

	t.Run("update file persona sets source to both", func(t *testing.T) {
		pReg := &mockPersonaRegistry{allResult: testPersonas("analyst")}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{
			PersonaRegistry:  pReg,
			Config:           testConfig(),
			ConfigStore:      cs,
			FilePersonaNames: map[string]bool{"analyst": true},
		}, nil)

		body := `{"display_name":"Updated Analyst","roles":["analyst"]}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/personas/analyst", strings.NewReader(body))
		req.SetPathValue("name", "analyst")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var detail personaDetail
		require.NoError(t, json.NewDecoder(w.Body).Decode(&detail))
		assert.Equal(t, "both", detail.Source)
	})

	t.Run("delete file-only persona blocked", func(t *testing.T) {
		p := testPersonas("analyst")[0]
		p.Source = "file"
		pReg := &mockPersonaRegistry{allResult: []*persona.Persona{p}}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{
			PersonaRegistry:  pReg,
			Config:           testConfig(),
			ConfigStore:      cs,
			FilePersonaNames: map[string]bool{"analyst": true},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/personas/analyst", http.NoBody)
		req.SetPathValue("name", "analyst")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "config file")
	})

	t.Run("delete both persona reverts to file", func(t *testing.T) {
		p := testPersonas("analyst")[0]
		p.Source = "both"
		pReg := &mockPersonaRegistry{allResult: []*persona.Persona{p}}
		cs := &mockConfigStore{mode: "database"}
		ps := &mockPersonaStore{}
		cfg := testConfig()
		cfg.Personas.Definitions = map[string]platform.PersonaDef{
			"analyst": {
				DisplayName: "File Analyst",
				Roles:       []string{"analyst"},
				Tools:       platform.ToolRulesDef{Allow: []string{"*"}},
			},
		}
		h := NewHandler(Deps{
			PersonaRegistry:  pReg,
			Config:           cfg,
			ConfigStore:      cs,
			PersonaStore:     ps,
			FilePersonaNames: map[string]bool{"analyst": true},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/personas/analyst", http.NoBody)
		req.SetPathValue("name", "analyst")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		// After delete, the file version should be re-registered
		reverted, ok := pReg.Get("analyst")
		assert.True(t, ok, "analyst should exist after revert")
		assert.Equal(t, "File Analyst", reverted.DisplayName)
		assert.Equal(t, "file", reverted.Source)
	})

	t.Run("delete both persona tolerates ErrPersonaNotFound from store", func(t *testing.T) {
		p := testPersonas("analyst")[0]
		p.Source = "both"
		pReg := &mockPersonaRegistry{allResult: []*persona.Persona{p}}
		cs := &mockConfigStore{mode: "database"}
		ps := &mockPersonaStore{deleteErr: personastore.ErrNotFound}
		cfg := testConfig()
		cfg.Personas.Definitions = map[string]platform.PersonaDef{
			"analyst": {
				DisplayName: "File Analyst",
				Roles:       []string{"analyst"},
				Tools:       platform.ToolRulesDef{Allow: []string{"*"}},
			},
		}
		h := NewHandler(Deps{
			PersonaRegistry:  pReg,
			Config:           cfg,
			ConfigStore:      cs,
			PersonaStore:     ps,
			FilePersonaNames: map[string]bool{"analyst": true},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/personas/analyst", http.NoBody)
		req.SetPathValue("name", "analyst")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		// Should succeed despite store returning ErrPersonaNotFound
		assert.Equal(t, http.StatusOK, w.Code)
		reverted, ok := pReg.Get("analyst")
		assert.True(t, ok)
		assert.Equal(t, "file", reverted.Source)
	})

	t.Run("delete both tolerates a failed revert re-register", func(t *testing.T) {
		p := testPersonas("analyst")[0]
		p.Source = "both"
		pReg := &mockPersonaRegistry{
			allResult:   []*persona.Persona{p},
			registerErr: fmt.Errorf("registry unavailable"),
		}
		cs := &mockConfigStore{mode: "database"}
		ps := &mockPersonaStore{}
		cfg := testConfig()
		cfg.Personas.Definitions = map[string]platform.PersonaDef{
			"analyst": {
				DisplayName: "File Analyst",
				Roles:       []string{"analyst"},
				Tools:       platform.ToolRulesDef{Allow: []string{"*"}},
			},
		}
		h := NewHandler(Deps{
			PersonaRegistry:  pReg,
			Config:           cfg,
			ConfigStore:      cs,
			PersonaStore:     ps,
			FilePersonaNames: map[string]bool{"analyst": true},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/personas/analyst", http.NoBody)
		req.SetPathValue("name", "analyst")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		// The DB delete succeeded; a failed file-version re-register is
		// logged (name sanitized via logsan) but does not fail the request.
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestExtractAuthor(t *testing.T) {
	t.Run("returns email when user has email", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), adminUserKey, &User{
			UserID: "user-123",
			Email:  "alice@example.com",
			Roles:  []string{"admin"},
		})
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", http.NoBody)
		assert.Equal(t, "alice@example.com", extractAuthor(req))
	})

	t.Run("returns user ID when email is empty", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), adminUserKey, &User{
			UserID: "user-456",
			Email:  "",
			Roles:  []string{"admin"},
		})
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", http.NoBody)
		assert.Equal(t, "user-456", extractAuthor(req))
	})

	t.Run("returns unknown when no user in context", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", http.NoBody)
		assert.Equal(t, "unknown", extractAuthor(req))
	})
}

func TestBuildPersonaFromRequest(t *testing.T) {
	req := personaCreateRequest{
		Name:        "test",
		DisplayName: "Test Persona",
		Description: "A test",
		Roles:       []string{"admin"},
		AllowTools:  []string{"trino_*"},
		DenyTools:   []string{"s3_*"},
		Priority:    testPriority,
	}

	p := buildPersonaFromRequest(req)
	assert.Equal(t, "test", p.Name)
	assert.Equal(t, "Test Persona", p.DisplayName)
	assert.Equal(t, []string{"trino_*"}, p.Tools.Allow)
	assert.Equal(t, []string{"s3_*"}, p.Tools.Deny)
	assert.Equal(t, testPriority, p.Priority)
}

func TestBuildPersonaFromRequest_NilTools(t *testing.T) {
	req := personaCreateRequest{
		Name:        "test",
		DisplayName: "Test",
	}

	p := buildPersonaFromRequest(req)
	assert.NotNil(t, p.Tools.Allow)
	assert.NotNil(t, p.Tools.Deny)
	assert.Len(t, p.Tools.Allow, 0)
	assert.Len(t, p.Tools.Deny, 0)
}

func TestTestPersonaAccess(t *testing.T) {
	makeHandler := func() *Handler {
		p := &persona.Persona{
			Name: "analyst",
			Tools: persona.ToolRules{
				Allow: []string{"trino_*"},
				Deny:  []string{"trino_admin_*"},
			},
		}
		return NewHandler(Deps{
			PersonaRegistry: &mockPersonaRegistry{allResult: []*persona.Persona{p}},
		}, nil)
	}

	postBody := func(t *testing.T, h *Handler, name string, body any) *httptest.ResponseRecorder {
		t.Helper()
		buf, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
			"/api/v1/admin/personas/"+name+"/test-access", strings.NewReader(string(buf)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	t.Run("returns allow decision with matched pattern", func(t *testing.T) {
		w := postBody(t, makeHandler(), "analyst", testPersonaAccessRequest{ToolName: "trino_query"})
		require.Equal(t, http.StatusOK, w.Code)
		var resp testPersonaAccessResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.True(t, resp.Allowed)
		assert.Equal(t, "trino_*", resp.MatchedPattern)
		assert.Equal(t, persona.AccessSourceAllow, resp.Source)
	})

	t.Run("returns deny decision with matched pattern", func(t *testing.T) {
		w := postBody(t, makeHandler(), "analyst", testPersonaAccessRequest{ToolName: "trino_admin_kill"})
		require.Equal(t, http.StatusOK, w.Code)
		var resp testPersonaAccessResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.False(t, resp.Allowed)
		assert.Equal(t, "trino_admin_*", resp.MatchedPattern)
		assert.Equal(t, persona.AccessSourceDeny, resp.Source)
	})

	t.Run("returns default-deny when no rule matches", func(t *testing.T) {
		w := postBody(t, makeHandler(), "analyst", testPersonaAccessRequest{ToolName: "datahub_search"})
		require.Equal(t, http.StatusOK, w.Code)
		var resp testPersonaAccessResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.False(t, resp.Allowed)
		assert.Empty(t, resp.MatchedPattern)
		assert.Equal(t, persona.AccessSourceDefault, resp.Source)
	})

	t.Run("returns 404 for unknown persona", func(t *testing.T) {
		w := postBody(t, makeHandler(), "ghost", testPersonaAccessRequest{ToolName: "trino_query"})
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("returns 400 for empty tool_name", func(t *testing.T) {
		w := postBody(t, makeHandler(), "analyst", testPersonaAccessRequest{ToolName: ""})
		assert.Equal(t, http.StatusBadRequest, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "tool_name is required")
	})

	t.Run("returns 400 for invalid JSON", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
			"/api/v1/admin/personas/analyst/test-access", strings.NewReader("not json"))
		w := httptest.NewRecorder()
		makeHandler().ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("returns 503 when persona registry is unavailable", func(t *testing.T) {
		// Without PersonaRegistry, registerPersonaRoutes() never registers
		// the route, so the mux returns 404. Hit the handler method
		// directly to exercise the 503 branch.
		h := NewHandler(Deps{}, nil)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
			"/api/v1/admin/personas/analyst/test-access", strings.NewReader(`{"tool_name":"x"}`))
		req.SetPathValue("name", "analyst")
		w := httptest.NewRecorder()
		h.testPersonaAccess(w, req)
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})
}

// TestPersonaWriteWarnsOnIncoherentToolSet drives the real HTTP handlers so it
// proves the warning fires on the actual write path, not merely that
// persona.CheckCoherence works when handed the right input.
func TestPersonaWriteWarnsOnIncoherentToolSet(t *testing.T) {
	searchToolkit := &mockToolkitRegistry{
		allResult: []mockToolkit{
			{kind: "search", name: "primary", tools: []string{"search", "fetch"}},
			{kind: "trino", name: "prod", tools: []string{"trino_query"}},
		},
	}

	tests := []struct {
		name        string
		method      string
		target      string
		body        string
		toolkits    ToolkitRegistry
		wantStatus  int
		wantWarning []string
		noWarning   bool
	}{
		{
			name:        "create with search but no fetch warns",
			method:      http.MethodPost,
			target:      "/api/v1/admin/personas",
			body:        `{"name":"analyst","display_name":"Data Analyst","roles":["analyst"],"allow_tools":["search","trino_*"]}`,
			toolkits:    searchToolkit,
			wantStatus:  http.StatusCreated,
			wantWarning: []string{"persona grants a capability it cannot complete", "persona=analyst", "granted=search", "missing=fetch"},
		},
		{
			name:       "create with search and fetch does not warn",
			method:     http.MethodPost,
			target:     "/api/v1/admin/personas",
			body:       `{"name":"analyst","display_name":"Data Analyst","roles":["analyst"],"allow_tools":["search","fetch"]}`,
			toolkits:   searchToolkit,
			wantStatus: http.StatusCreated,
			noWarning:  true,
		},
		{
			name:        "update that drops fetch warns",
			method:      http.MethodPut,
			target:      "/api/v1/admin/personas/admin",
			body:        `{"display_name":"Administrator","roles":["admin"],"allow_tools":["*"],"deny_tools":["fetch"]}`,
			toolkits:    searchToolkit,
			wantStatus:  http.StatusOK,
			wantWarning: []string{"granted=search", "missing=fetch"},
		},
		{
			// No toolkit registry means no registered tool set to judge
			// against; the write must still succeed.
			name:       "create with no toolkit registry does not warn",
			method:     http.MethodPost,
			target:     "/api/v1/admin/personas",
			body:       `{"name":"analyst","display_name":"Data Analyst","roles":["analyst"],"allow_tools":["search"]}`,
			toolkits:   nil,
			wantStatus: http.StatusCreated,
			noWarning:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			oldLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			defer slog.SetDefault(oldLogger)

			deps := Deps{
				PersonaRegistry: &mockPersonaRegistry{allResult: testPersonas("admin")},
				Config:          testConfig(),
				ConfigStore:     &mockConfigStore{mode: "database"},
			}
			if tt.toolkits != nil {
				deps.ToolkitRegistry = tt.toolkits
			}
			h := NewHandler(deps, nil)

			req := httptest.NewRequestWithContext(context.Background(), tt.method, tt.target, strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			require.Equal(t, tt.wantStatus, w.Code, "body: %s", w.Body.String())

			if tt.noWarning {
				assert.Empty(t, buf.String(), "expected no warning")
				return
			}
			for _, want := range tt.wantWarning {
				assert.Contains(t, buf.String(), want)
			}
		})
	}
}

// TestRevertToFilePersonaWarnsOnIncoherentToolSet covers the third write path:
// deleting a database override re-registers the file version, so the persona
// now in force is not the one any earlier write logged about.
func TestRevertToFilePersonaWarnsOnIncoherentToolSet(t *testing.T) {
	var buf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(oldLogger)

	p := testPersonas("analyst")[0]
	p.Source = "both"
	cfg := testConfig()
	cfg.Personas.Definitions = map[string]platform.PersonaDef{
		"analyst": {
			DisplayName: "File Analyst",
			Roles:       []string{"analyst"},
			// The file version discovers but cannot read what it discovers.
			Tools: platform.ToolRulesDef{Allow: []string{"search", "trino_*"}},
		},
	}
	h := NewHandler(Deps{
		PersonaRegistry:  &mockPersonaRegistry{allResult: []*persona.Persona{p}},
		Config:           cfg,
		ConfigStore:      &mockConfigStore{mode: "database"},
		PersonaStore:     &mockPersonaStore{},
		FilePersonaNames: map[string]bool{"analyst": true},
		ToolkitRegistry: &mockToolkitRegistry{
			allResult: []mockToolkit{{kind: "search", name: "primary", tools: []string{"search", "fetch"}}},
		},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/personas/analyst", http.NoBody)
	req.SetPathValue("name", "analyst")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, buf.String(), "persona grants a capability it cannot complete")
	assert.Contains(t, buf.String(), "persona=analyst")
	assert.Contains(t, buf.String(), "missing=fetch")
}
