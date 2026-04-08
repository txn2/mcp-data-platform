package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
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
		ps := &mockPersonaStore{deleteErr: platform.ErrPersonaNotFound}
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
