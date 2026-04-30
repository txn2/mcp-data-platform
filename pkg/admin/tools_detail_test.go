package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/configstore"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/enrichment"
)

// addStubTool is a tiny helper that registers a no-op tool on an MCP server.
// Used by tests that need a tool of a specific name without caring about
// the call result.
func addStubTool(s *mcp.Server, name, description string) {
	s.AddTool(&mcp.Tool{
		Name:        name,
		Description: description,
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})
}

func TestGetToolDetail_FullJoin(t *testing.T) {
	reg := &mockToolkitRegistry{
		allResult: []mockToolkit{
			{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query"}},
		},
	}

	personaStore := &mockPersonaStore{
		listResult: []platform.PersonaDefinition{
			{Name: "analyst", ToolsAllow: []string{"trino_*"}, ToolsDeny: []string{}},
			{Name: "viewer", ToolsAllow: []string{"datahub_*"}, ToolsDeny: []string{}},
			{Name: "admin", ToolsAllow: []string{"*"}, ToolsDeny: []string{"*_delete_*"}},
		},
	}

	cfgStore := &mockConfigStore{
		entries: map[string]*configstore.Entry{
			"tool.trino_query.description": {
				Key:       "tool.trino_query.description",
				Value:     "overridden description",
				UpdatedBy: "alice@example.com",
			},
		},
	}

	metricsQuerier := &mockAuditMetricsQuerier{
		breakdownResult: []audit.BreakdownEntry{
			{Dimension: "trino_query", Count: 42, SuccessRate: 0.95, AvgDurationMS: 123.4},
			{Dimension: "datahub_search", Count: 8, SuccessRate: 1.0, AvgDurationMS: 50},
		},
	}

	estore := newStubStore()
	_, err := estore.Create(context.Background(), enrichment.Rule{
		ID:             "r1",
		ConnectionName: "prod-trino",
		ToolName:       "trino_query",
		Enabled:        true,
	})
	require.NoError(t, err)

	cfg := testConfig()
	cfg.Tools.Deny = []string{"*_admin_*"}

	h := NewHandler(Deps{
		Config:              cfg,
		ToolkitRegistry:     reg,
		MCPServer:           newTestMCPServer(),
		PersonaStore:        personaStore,
		ConfigStore:         cfgStore,
		AuditMetricsQuerier: metricsQuerier,
		EnrichmentStore:     estore,
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/tools/trino_query", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var d ToolDetail
	require.NoError(t, json.NewDecoder(w.Body).Decode(&d))

	assert.Equal(t, "trino_query", d.Name)
	assert.Equal(t, "trino", d.ToolkitKind)
	assert.Equal(t, "prod", d.ToolkitName)
	assert.Equal(t, "prod-trino", d.Connection)
	assert.Equal(t, "Execute a SQL query", d.Description)
	assert.NotNil(t, d.InputSchema)

	// Persona matrix sorted alphabetically.
	require.Len(t, d.Personas, 3)
	assert.Equal(t, "admin", d.Personas[0].Persona)
	assert.True(t, d.Personas[0].Allowed)
	assert.Equal(t, "*", d.Personas[0].MatchedPattern)
	assert.Equal(t, persona.AccessSourceAllow, d.Personas[0].Source)

	assert.Equal(t, "analyst", d.Personas[1].Persona)
	assert.True(t, d.Personas[1].Allowed)
	assert.Equal(t, "trino_*", d.Personas[1].MatchedPattern)

	assert.Equal(t, "viewer", d.Personas[2].Persona)
	assert.False(t, d.Personas[2].Allowed)
	assert.Equal(t, persona.AccessSourceDefault, d.Personas[2].Source)

	// HiddenByPersona records denied personas.
	assert.True(t, d.HiddenByPersona["viewer"])
	assert.NotContains(t, d.HiddenByPersona, "analyst")

	// Global deny does not match this tool.
	assert.False(t, d.HiddenByGlobalDeny)
	assert.Empty(t, d.GlobalDenyPattern)

	// Description override surfaced.
	assert.True(t, d.DescriptionOverridden)
	assert.Equal(t, "alice@example.com", d.OverrideAuthor)

	// Activity from the breakdown row that matches the tool name.
	require.NotNil(t, d.Activity)
	assert.Equal(t, 42, d.Activity.CallCount)
	assert.InDelta(t, 0.95, d.Activity.SuccessRate, 0.0001)
	assert.InDelta(t, 123.4, d.Activity.AvgDurationMs, 0.0001)
	assert.Equal(t, int64(24*60*60), d.Activity.WindowSeconds)

	// Enrichment rules — kind="trino" so this tool is NOT a gateway-proxied
	// tool, so the count must be zero regardless of the stub's contents.
	assert.Equal(t, 0, d.EnrichmentRuleCount)
}

func TestGetToolDetail_GatewayProxied_CountsEnrichmentRules(t *testing.T) {
	reg := &mockToolkitRegistry{
		allResult: []mockToolkit{
			{kind: "mcp", name: "gateway", connection: "dev-mock", tools: []string{"dev-mock__echo"}},
		},
	}

	estore := newStubStore()
	for i := range 3 {
		_, err := estore.Create(context.Background(), enrichment.Rule{
			ID:             "r" + string(rune('a'+i)),
			ConnectionName: "dev-mock",
			ToolName:       "dev-mock__echo",
			Enabled:        true,
		})
		require.NoError(t, err)
	}

	server := newTestMCPServer()
	addStubTool(server, "dev-mock__echo", "echo a string")

	h := NewHandler(Deps{
		Config:          testConfig(),
		ToolkitRegistry: reg,
		MCPServer:       server,
		EnrichmentStore: estore,
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/tools/dev-mock__echo", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var d ToolDetail
	require.NoError(t, json.NewDecoder(w.Body).Decode(&d))
	assert.Equal(t, "mcp", d.ToolkitKind)
	assert.Equal(t, 3, d.EnrichmentRuleCount)
}

func TestGetToolDetail_HiddenByGlobalDeny(t *testing.T) {
	reg := &mockToolkitRegistry{
		allResult: []mockToolkit{
			{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_admin_kill"}},
		},
	}

	server := newTestMCPServer()
	addStubTool(server, "trino_admin_kill", "kill a query")

	cfg := testConfig()
	cfg.Tools.Deny = []string{"*_admin_*"}

	h := NewHandler(Deps{
		Config:          cfg,
		ToolkitRegistry: reg,
		MCPServer:       server,
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/tools/trino_admin_kill", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var d ToolDetail
	require.NoError(t, json.NewDecoder(w.Body).Decode(&d))
	assert.True(t, d.HiddenByGlobalDeny)
	assert.Equal(t, "*_admin_*", d.GlobalDenyPattern)
}

func TestGetToolDetail_PlatformTool(t *testing.T) {
	// platform_info is registered directly on the MCP server, not via any
	// toolkit. The detail handler must fall through to PlatformTools instead
	// of 404'ing.
	server := newTestMCPServer()
	addStubTool(server, "platform_info", "platform info")

	h := NewHandler(Deps{
		Config:          testConfig(),
		ToolkitRegistry: &mockToolkitRegistry{allResult: []mockToolkit{}},
		MCPServer:       server,
		PlatformTools: []platform.ToolInfo{
			{Name: "platform_info", Kind: "platform"},
		},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/tools/platform_info", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var d ToolDetail
	require.NoError(t, json.NewDecoder(w.Body).Decode(&d))
	assert.Equal(t, "platform_info", d.Name)
	assert.Equal(t, "platform", d.ToolkitKind)
	assert.Empty(t, d.Connection)
}

func TestGetToolDetail_NotFound(t *testing.T) {
	h := NewHandler(Deps{
		Config:          testConfig(),
		ToolkitRegistry: &mockToolkitRegistry{allResult: []mockToolkit{}},
		MCPServer:       newTestMCPServer(),
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/tools/nonexistent", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetToolDetail_RegistryUnavailable(t *testing.T) {
	h := NewHandler(Deps{Config: testConfig()}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/tools/anything", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestGetToolDetail_DegradesOnDependencyFailures(t *testing.T) {
	// The handler must not 500 when any of the optional fillers fail —
	// it should return whatever fields it could populate.
	reg := &mockToolkitRegistry{
		allResult: []mockToolkit{
			{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query"}},
		},
	}

	personaStore := &mockPersonaStore{listErr: errors.New("db unreachable")}
	cfgStore := &mockConfigStore{getErr: errors.New("db unreachable")}
	metrics := &mockAuditMetricsQuerier{breakdownErr: errors.New("db unreachable")}

	h := NewHandler(Deps{
		Config:              testConfig(),
		ToolkitRegistry:     reg,
		MCPServer:           newTestMCPServer(),
		PersonaStore:        personaStore,
		ConfigStore:         cfgStore,
		AuditMetricsQuerier: metrics,
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/tools/trino_query", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var d ToolDetail
	require.NoError(t, json.NewDecoder(w.Body).Decode(&d))
	assert.Equal(t, "trino_query", d.Name)
	assert.Empty(t, d.Personas)
	assert.False(t, d.DescriptionOverridden)
	assert.Nil(t, d.Activity)
}

// TestGetToolDetail_ActivityZeroRows covers fillToolActivity's
// "querier returns zero rows" branch — the explicit guard added when
// the breakdown was switched from top-N scan to ToolName-scoped query
// (#343 bug 2). The handler must not panic and Activity must remain
// nil so the UI renders "no data" rather than a zero-filled card.
func TestGetToolDetail_ActivityZeroRows(t *testing.T) {
	reg := &mockToolkitRegistry{
		allResult: []mockToolkit{
			{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query"}},
		},
	}
	// Querier responds successfully but with an empty result set —
	// this is the shape we'll get for any tool that has zero calls
	// in the recent window (ToolName filter narrows to one row at most;
	// no calls = no row).
	metrics := &mockAuditMetricsQuerier{breakdownResult: nil}

	h := NewHandler(Deps{
		Config:              testConfig(),
		ToolkitRegistry:     reg,
		MCPServer:           newTestMCPServer(),
		AuditMetricsQuerier: metrics,
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/tools/trino_query", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var d ToolDetail
	require.NoError(t, json.NewDecoder(w.Body).Decode(&d))
	assert.Nil(t, d.Activity,
		"Activity must remain nil when the querier returns zero rows "+
			"so the UI shows 'no data' rather than a zero-filled card")
}

func TestGetToolDetail_EmptyName(t *testing.T) {
	// Hitting /tools/ (no path value) is a 404 from the mux, not the handler.
	// The empty-name guard is exercised by routing through the handler with
	// a manually constructed request that includes an empty path value.
	h := NewHandler(Deps{
		Config:          testConfig(),
		ToolkitRegistry: &mockToolkitRegistry{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/tools/", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Route /tools/{name} doesn't match an empty name; mux returns 404.
	assert.NotEqual(t, http.StatusOK, w.Code)
}

func TestMatchGlobalDeny(t *testing.T) {
	tests := []struct {
		name      string
		patterns  []string
		toolName  string
		wantMatch bool
		wantPat   string
	}{
		{"exact", []string{"trino_query"}, "trino_query", true, "trino_query"},
		{"glob", []string{"*_admin_*"}, "trino_admin_kill", true, "*_admin_*"},
		{"first match wins", []string{"a*", "trino_*"}, "trino_query", true, "trino_*"},
		{"no match", []string{"datahub_*"}, "trino_query", false, ""},
		{"empty list", nil, "trino_query", false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pat, ok := matchGlobalDeny(tc.patterns, tc.toolName)
			assert.Equal(t, tc.wantMatch, ok)
			assert.Equal(t, tc.wantPat, pat)
		})
	}
}

func TestToolDescriptionConfigKey(t *testing.T) {
	assert.Equal(t, "tool.trino_query.description", toolDescriptionConfigKey("trino_query"))
	assert.Equal(t, "tool.dev-mock__echo.description", toolDescriptionConfigKey("dev-mock__echo"))
}

func TestUpdateDenyList(t *testing.T) {
	tests := []struct {
		name     string
		current  []string
		tool     string
		hidden   bool
		expected []string
	}{
		{"add to empty", nil, "trino_query", true, []string{"trino_query"}},
		{"add to non-empty", []string{"a", "b"}, "c", true, []string{"a", "b", "c"}},
		{"add idempotent", []string{"a", "b"}, "a", true, []string{"a", "b"}},
		{"remove", []string{"a", "b", "c"}, "b", false, []string{"a", "c"}},
		{"remove missing is no-op", []string{"a", "b"}, "x", false, []string{"a", "b"}},
		{"sorts result", []string{"z", "a"}, "m", true, []string{"a", "m", "z"}},
		{"strips empties", []string{"", "a"}, "b", true, []string{"a", "b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := updateDenyList(tc.current, tc.tool, tc.hidden)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestSetToolVisibility(t *testing.T) {
	makeHandler := func(initial []string) (*Handler, *mockConfigStore, *platform.Config) {
		cfg := testConfig()
		cfg.Tools.Deny = append([]string(nil), initial...)
		cs := &mockConfigStore{mode: "database"}
		if len(initial) > 0 {
			buf, _ := json.Marshal(initial)
			cs.entries = map[string]*configstore.Entry{
				"tools.deny": {Key: "tools.deny", Value: string(buf)},
			}
		}
		reg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query", "trino_execute"}},
			},
		}
		h := NewHandler(Deps{
			Config:          cfg,
			ConfigStore:     cs,
			ToolkitRegistry: reg,
			MCPServer:       newTestMCPServer(),
		}, nil)
		return h, cs, cfg
	}

	t.Run("hidden=true adds to deny list and applies live config", func(t *testing.T) {
		h, cs, cfg := makeHandler(nil)
		body := `{"hidden":true}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			"/api/v1/admin/tools/trino_query/visibility", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		var resp toolVisibilityResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.True(t, resp.Hidden)
		assert.Equal(t, []string{"trino_query"}, resp.Deny)
		assert.Equal(t, []string{"trino_query"}, cfg.Tools.Deny)

		stored := cs.entries["tools.deny"]
		require.NotNil(t, stored)
		var decoded []string
		require.NoError(t, json.Unmarshal([]byte(stored.Value), &decoded))
		assert.Equal(t, []string{"trino_query"}, decoded)
	})

	t.Run("hidden=false removes from deny list", func(t *testing.T) {
		h, _, cfg := makeHandler([]string{"trino_query", "trino_execute"})
		body := `{"hidden":false}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			"/api/v1/admin/tools/trino_query/visibility", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		var resp toolVisibilityResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.False(t, resp.Hidden)
		assert.Equal(t, []string{"trino_execute"}, resp.Deny)
		assert.Equal(t, []string{"trino_execute"}, cfg.Tools.Deny)
	})

	t.Run("404 for unknown tool", func(t *testing.T) {
		h, _, _ := makeHandler(nil)
		body := `{"hidden":true}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			"/api/v1/admin/tools/no_such_tool/visibility", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("400 for invalid body", func(t *testing.T) {
		h, _, _ := makeHandler(nil)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			"/api/v1/admin/tools/trino_query/visibility", strings.NewReader("not json"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("503 without config store", func(t *testing.T) {
		// Route only registered when h.isMutable(). Hit handler directly to
		// exercise the early-return branch when deps.ConfigStore is nil.
		h := NewHandler(Deps{}, nil)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			"/api/v1/admin/tools/trino_query/visibility", strings.NewReader(`{"hidden":true}`))
		req.SetPathValue("name", "trino_query")
		w := httptest.NewRecorder()
		h.setToolVisibility(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})

	t.Run("400 for empty tool name", func(t *testing.T) {
		h, _, _ := makeHandler(nil)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			"/api/v1/admin/tools//visibility", strings.NewReader(`{"hidden":true}`))
		req.SetPathValue("name", "")
		w := httptest.NewRecorder()
		h.setToolVisibility(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("500 when load returns non-NotFound error", func(t *testing.T) {
		cfg := testConfig()
		cs := &mockConfigStore{mode: "database", getErr: errors.New("db down")}
		reg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query"}},
			},
		}
		h := NewHandler(Deps{Config: cfg, ConfigStore: cs, ToolkitRegistry: reg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			"/api/v1/admin/tools/trino_query/visibility", strings.NewReader(`{"hidden":true}`))
		req.SetPathValue("name", "trino_query")
		w := httptest.NewRecorder()
		h.setToolVisibility(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("500 when ConfigStore.Set fails", func(t *testing.T) {
		cfg := testConfig()
		cs := &mockConfigStore{mode: "database", setErr: errors.New("disk full")}
		reg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query"}},
			},
		}
		h := NewHandler(Deps{Config: cfg, ConfigStore: cs, ToolkitRegistry: reg}, nil)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			"/api/v1/admin/tools/trino_query/visibility", strings.NewReader(`{"hidden":true}`))
		req.SetPathValue("name", "trino_query")
		w := httptest.NewRecorder()
		h.setToolVisibility(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// TestSetToolVisibility_ConcurrentAddsAreSerialized proves the fix for
// #343 bug 1: parallel toggles on different tool names must not lose
// each other's writes.
//
// Pre-fix, two admins toggling visibility on different tools at the
// same time could each load the same starting list (e.g. []), append
// their own tool, and the second writer would overwrite the first —
// silently dropping one of the changes. The fix adds an in-process
// mutex around the load → modify → save critical section.
//
// The test fires N concurrent PUT calls each toggling a different
// tool to hidden=true and asserts the final deny list contains every
// one of them. Without the lock, the test fails non-deterministically;
// with the lock it passes every run. -race must be on (see Makefile).
func TestSetToolVisibility_ConcurrentAddsAreSerialized(t *testing.T) {
	cfg := testConfig()
	cs := &mockConfigStore{mode: "database"}

	// Register a synthetic toolkit exposing 8 tool names so concurrent
	// PUTs against different paths are all valid against toolExists.
	toolNames := []string{
		"trino_query", "trino_execute", "datahub_search", "datahub_get_entity",
		"s3_list", "s3_get", "memory_recall", "memory_save",
	}
	reg := &mockToolkitRegistry{
		allResult: []mockToolkit{
			{kind: "trino", name: "prod", connection: "prod-trino", tools: toolNames},
		},
	}
	h := NewHandler(Deps{
		Config:          cfg,
		ConfigStore:     cs,
		ToolkitRegistry: reg,
		MCPServer:       newTestMCPServer(),
	}, nil)

	var wg sync.WaitGroup
	for _, name := range toolNames {
		wg.Add(1)
		go func(toolName string) {
			defer wg.Done()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
				"/api/v1/admin/tools/"+toolName+"/visibility",
				strings.NewReader(`{"hidden":true}`))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code,
				"concurrent visibility toggle for %s must succeed", toolName)
		}(name)
	}
	wg.Wait()

	// All N tools must end up in the deny list. Pre-fix, this assertion
	// would fail because of the lost-update race.
	stored := cs.entries["tools.deny"]
	require.NotNil(t, stored, "tools.deny entry must exist after concurrent writes")
	var decoded []string
	require.NoError(t, json.Unmarshal([]byte(stored.Value), &decoded))
	assert.ElementsMatch(t, toolNames, decoded,
		"every concurrent toggle must be reflected in the final deny list "+
			"(lost-update race? saw %v, want %v)", decoded, toolNames)
}

// TestSetToolVisibility_SameToolFlapStaysConsistent stresses the lock
// against the "same tool flapped between hidden=true and hidden=false
// many times concurrently" pathology. The end state must be one of the
// two valid outcomes (in deny list or not), the JSON value must parse
// to a clean []string, and the deny list must NOT contain duplicate
// entries or partial-write garbage. Different from
// _ConcurrentAddsAreSerialized which only catches dropped writes
// across distinct keys; this catches corruption from interleaved
// reads of the same key.
func TestSetToolVisibility_SameToolFlapStaysConsistent(t *testing.T) {
	cfg := testConfig()
	cs := &mockConfigStore{mode: "database"}

	reg := &mockToolkitRegistry{
		allResult: []mockToolkit{
			{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query"}},
		},
	}
	h := NewHandler(Deps{
		Config:          cfg,
		ConfigStore:     cs,
		ToolkitRegistry: reg,
		MCPServer:       newTestMCPServer(),
	}, nil)

	const flaps = 32
	var wg sync.WaitGroup
	for i := range flaps {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			hidden := idx%2 == 0
			body := `{"hidden":false}`
			if hidden {
				body = `{"hidden":true}`
			}
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
				"/api/v1/admin/tools/trino_query/visibility",
				strings.NewReader(body))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)
		}(i)
	}
	wg.Wait()

	// Final state must parse cleanly and either contain the tool
	// exactly once or not at all — never duplicated, never garbage.
	stored := cs.entries["tools.deny"]
	require.NotNil(t, stored)
	var decoded []string
	require.NoError(t, json.Unmarshal([]byte(stored.Value), &decoded),
		"final tools.deny value must parse as []string after concurrent flap")

	count := 0
	for _, name := range decoded {
		if name == "trino_query" {
			count++
		}
	}
	assert.LessOrEqual(t, count, 1,
		"concurrent flap must not duplicate the tool in the deny list (saw %d copies in %v)",
		count, decoded)
}
