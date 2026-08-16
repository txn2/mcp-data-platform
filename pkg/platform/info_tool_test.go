package platform

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/knowledgelayer"
	"github.com/txn2/mcp-data-platform/internal/platform/resourcelayer"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform/instructions"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/storage"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// stubInsightStore overrides Stats on the noop insight store so reviewQueueInfo
// can be tested without a database. Other InsightStore methods are inherited
// from the noop and are unused here.
type stubInsightStore struct {
	knowledgekit.InsightStore
	stats *knowledgekit.InsightStats
	err   error
}

func (s stubInsightStore) Stats(context.Context, knowledgekit.InsightFilter) (*knowledgekit.InsightStats, error) {
	return s.stats, s.err
}

func TestReviewQueueInfo(t *testing.T) {
	oldest := time.Now().Add(-94 * 24 * time.Hour)
	tests := []struct {
		name  string
		store knowledgekit.InsightStore
		want  *ReviewQueueInfo
	}{
		{name: "nil store returns nil", store: nil, want: nil},
		{
			name:  "stats error returns nil (orientation must not fail)",
			store: stubInsightStore{InsightStore: knowledgekit.NewNoopStore(), err: errors.New("db down")},
			want:  nil,
		},
		{
			name:  "empty queue returns nil",
			store: stubInsightStore{InsightStore: knowledgekit.NewNoopStore(), stats: &knowledgekit.InsightStats{TotalPending: 0}},
			want:  nil,
		},
		{
			name: "pending queue with staleness is summarized",
			store: stubInsightStore{InsightStore: knowledgekit.NewNoopStore(), stats: &knowledgekit.InsightStats{
				TotalPending:    6,
				OldestPendingAt: &oldest,
				PendingOver30d:  2,
			}},
			want: &ReviewQueueInfo{Pending: 6, OldestPendingAgeDays: 94, PendingOver30d: 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build the layer handle over the injected insight store; the
			// db/embedding inputs are unused with apply disabled.
			handle, err := knowledgelayer.NewFromInsightStore(nil, tt.store, nil, knowledgelayer.Config{ToolkitName: instanceDefault})
			require.NoError(t, err)
			p := &Platform{knowledge: handle}
			got := p.reviewQueueInfo(context.Background())
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.want.Pending, got.Pending)
			assert.Equal(t, tt.want.PendingOver30d, got.PendingOver30d)
			assert.InDelta(t, tt.want.OldestPendingAgeDays, got.OldestPendingAgeDays, 1)
		})
	}
}

const (
	testInfoVersion      = "1.0.0"
	testInfoToolkitCount = 4 // 3 configured + 1 prepended "platform"
	testInfoVersionV1    = "v1"
)

// requireInfoFromResult extracts an Info struct from a tool call result.
func requireInfoFromResult(t *testing.T, result *mcp.CallToolResult) Info {
	t.Helper()
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected TextContent")
	var info Info
	err := json.Unmarshal([]byte(textContent.Text), &info)
	require.NoError(t, err)
	return info
}

// TestBuildFeatures_GatesKnowledgeByPersonaTools is the #686 fix: platform_info
// must not advertise the knowledge lifecycle to a persona that cannot drive it.
func TestBuildFeatures_GatesKnowledgeByPersonaTools(t *testing.T) {
	p := &Platform{config: &Config{
		Knowledge: KnowledgeConfig{Apply: KnowledgeApplyConfig{Enabled: new(true), DataHubConnection: "primary"}},
	}}

	t.Run("persona with the tools sees the knowledge features", func(t *testing.T) {
		f := p.buildFeatures(context.Background(), []string{"memory_capture", "apply_knowledge"})
		assert.True(t, f.KnowledgeCapture)
		require.NotNil(t, f.KnowledgeApply)
		assert.True(t, f.KnowledgeApply.Enabled)
	})

	t.Run("persona without the tools sees neither", func(t *testing.T) {
		f := p.buildFeatures(context.Background(), []string{"trino_query"})
		assert.False(t, f.KnowledgeCapture, "capture hidden from a persona without memory_capture")
		assert.Nil(t, f.KnowledgeApply, "apply hidden from a persona without apply_knowledge")
	})
}

// TestHandleInfo covers the configured values info_tool passes through. Its
// fixtures disable the purpose argument, which is on by default and would
// otherwise append its runtime note to every expected instruction string;
// TestHandleInfo_AppendsPurposeNote covers that note on its own.
func TestHandleInfo(t *testing.T) {
	tests := []struct {
		name                  string
		config                Config
		wantName              string
		wantVer               string
		wantDesc              string
		wantTags              []string
		wantAgentInstructions string
	}{
		{
			name: "returns configured values",
			config: Config{
				Server: ServerConfig{
					Name:        "test-platform",
					Version:     "2.0.0",
					Description: "Test platform description",
				},
				Toolkits: map[string]any{
					"trino":   map[string]any{},
					"datahub": map[string]any{},
				},
				Enrichment: EnrichmentConfig{
					TrinoSemanticEnrichment: new(true),
					DataHubQueryEnrichment:  new(true),
				},
				Audit: AuditConfig{
					Enabled: new(true),
				},
			},
			wantName: "test-platform",
			wantVer:  "2.0.0",
			wantDesc: "Test platform description",
		},
		{
			name: "handles empty description",
			config: Config{
				Server: ServerConfig{
					Name:    "minimal-platform",
					Version: testInfoVersion,
				},
			},
			wantName: "minimal-platform",
			wantVer:  testInfoVersion,
			wantDesc: "",
		},
		{
			name: "returns tags and agent instructions",
			config: Config{
				Server: ServerConfig{
					Name:              "tagged-platform",
					Version:           testInfoVersion,
					Tags:              []string{"ACME Corp", "XWidget", "analytics"},
					AgentInstructions: "Prices are in cents - divide by 100.",
				},
			},
			wantName:              "tagged-platform",
			wantVer:               testInfoVersion,
			wantTags:              []string{"ACME Corp", "XWidget", "analytics"},
			wantAgentInstructions: "Prices are in cents - divide by 100.",
		},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.config.Purpose.Enabled = new(false)
			p := &Platform{
				config:          &tt.config,
				personaRegistry: persona.NewRegistry(),
				toolkitRegistry: registry.NewRegistry(),
			}

			result, extra, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

			require.NoError(t, err)
			assert.Nil(t, extra)
			require.NotNil(t, result)
			require.Len(t, result.Content, 1)

			textContent, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok, "expected TextContent")

			var info Info
			err = json.Unmarshal([]byte(textContent.Text), &info)
			require.NoError(t, err)

			assert.Equal(t, tt.wantName, info.Name)
			assert.Equal(t, tt.wantVer, info.Version)
			assert.Equal(t, tt.wantDesc, info.Description)
			assert.Equal(t, tt.wantTags, info.Tags)
			assert.Equal(t, tt.wantAgentInstructions, info.AgentInstructions)
		})
	}
}

// regWithTools returns a toolkit registry exposing the given tool names.
func regWithTools(t *testing.T, tools ...string) *registry.Registry {
	t.Helper()
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(&mockToolkit{kind: "search", name: "default", tools: tools}))
	return reg
}

// TestHandleInfo_AppendsPurposeNote proves the purpose guidance (#1317) reaches
// the agent through platform_info: on by default, absent when purpose is
// disabled, and dropping the refusal sentence when purpose is record-only.
func TestHandleInfo_AppendsPurposeNote(t *testing.T) {
	instructionsFor := func(t *testing.T, purpose PurposeConfig) string {
		t.Helper()
		config := Config{
			Server:  ServerConfig{Name: "purpose-test", Version: testInfoVersion},
			Purpose: purpose,
		}
		p := &Platform{
			config:          &config,
			personaRegistry: persona.NewRegistry(),
			toolkitRegistry: regWithTools(t, "search"),
		}
		result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})
		require.NoError(t, err)
		return requireInfoFromResult(t, result).AgentInstructions
	}

	byDefault := instructionsFor(t, PurposeConfig{})
	assert.Contains(t, byDefault, "Stating why you are calling:",
		"purpose is on by default, so its guidance ships by default")
	assert.Contains(t, byDefault, "PURPOSE_REQUIRED",
		"required by default, so the agent is told what omitting it costs")

	recordOnly := instructionsFor(t, PurposeConfig{Require: new(false)})
	assert.Contains(t, recordOnly, "Stating why you are calling:")
	assert.NotContains(t, recordOnly, "PURPOSE_REQUIRED",
		"a record-only deployment must not threaten a refusal that never comes")

	off := instructionsFor(t, PurposeConfig{Enabled: new(false)})
	assert.NotContains(t, off, "Stating why you are calling:",
		"a disabled feature is never described to the agent")
}

// TestHandleInfo_ComposesBaselineBeneathAdmin proves the platform baseline (#646)
// is composed above the admin's agent_instructions and surfaced separately.
func TestHandleInfo_ComposesBaselineBeneathAdmin(t *testing.T) {
	config := Config{Server: ServerConfig{
		Name:              "baseline-test",
		Version:           testInfoVersion,
		AgentInstructions: "ACME stores transactions in Cassandra.",
	}}
	p := &Platform{
		config:          &config,
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: regWithTools(t, "search", "memory_capture"),
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})
	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	// The composed instructions lead with the baseline (naming both tools) and
	// carry the admin text below it.
	assert.True(t, strings.HasPrefix(info.AgentInstructions, "How to operate this platform:"),
		"composed instructions should lead with the baseline, got: %q", info.AgentInstructions)
	assert.Contains(t, info.AgentInstructions, "`search`")
	assert.Contains(t, info.AgentInstructions, "`memory_capture`")
	assert.Contains(t, info.AgentInstructions, "ACME stores transactions in Cassandra.")
}

// TestHandleInfo_BaselineGatedByPersona proves the baseline names only tools the
// caller's persona can reach: a persona allowed search but not memory_capture
// gets a baseline that mentions search and never memory_capture.
func TestHandleInfo_BaselineGatedByPersona(t *testing.T) {
	pr := persona.NewRegistry()
	require.NoError(t, pr.Register(&persona.Persona{
		Name:  "reader",
		Tools: persona.ToolRules{Allow: []string{"search"}},
	}))

	p := &Platform{
		config:          &Config{Server: ServerConfig{Name: "g", Version: testInfoVersion}},
		personaRegistry: pr,
		toolkitRegistry: regWithTools(t, "search", "memory_capture"),
	}
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{PersonaName: "reader"})
	result, _, err := p.handleInfo(ctx, &mcp.CallToolRequest{})
	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	assert.Contains(t, info.AgentInstructions, "`search`")
	assert.NotContains(t, info.AgentInstructions, "`memory_capture`",
		"baseline must not name memory_capture for a persona that cannot call it")
}

// TestHandleInfo_NoBaselineWithoutBaselineTools proves a deployment exposing
// none of the baseline's tools gets no baseline (nothing to say without a tool).
func TestHandleInfo_NoBaselineWithoutBaselineTools(t *testing.T) {
	p := &Platform{
		config:          &Config{Server: ServerConfig{Name: "g", Version: testInfoVersion}},
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: regWithTools(t, "trino_query"),
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})
	require.NoError(t, err)
	info := requireInfoFromResult(t, result)
	assert.NotContains(t, info.AgentInstructions, "How to operate this platform:",
		"no baseline tools registered should yield no baseline")
}

// resourcesEnabledLayer builds a managed-resources handle backed by a mock
// database, which is all handleInfo reads: it asks only whether a store exists.
func resourcesEnabledLayer(t *testing.T) *resourcelayer.Handle {
	t.Helper()
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	h, err := resourcelayer.New(db, resourcelayer.Config{})
	require.NoError(t, err)
	require.NotNil(t, h.Store())
	return h
}

// TestHandleInfo_ResourcesNoteStatesThePositioningAndTheRule proves a
// deployment with managed resources tells the agent both what a resource is and
// when to reach for one, in the wording every other surface uses (#1015).
func TestHandleInfo_ResourcesNoteStatesThePositioningAndTheRule(t *testing.T) {
	p := &Platform{
		config:          &Config{Server: ServerConfig{Name: "res", Version: testInfoVersion}},
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: regWithTools(t, "search", "fetch"),
		resources:       resourcesEnabledLayer(t),
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})
	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	assert.Contains(t, info.AgentInstructions, instructions.ResourcePositioning,
		"the canonical positioning statement must reach the agent verbatim")
	assert.Contains(t, info.AgentInstructions, "Before you format a deliverable, `search`")
	assert.Contains(t, info.AgentInstructions, "`fetch` (pass the result's `reference`)")
	assert.True(t, info.Features.ManagedResources)
}

// TestHandleInfo_ResourcesNoteGatedByPersona proves the note obeys the same
// tool gate as the baseline: a persona denied search is pointed at the
// resources/list protocol method rather than at a tool it would be refused.
func TestHandleInfo_ResourcesNoteGatedByPersona(t *testing.T) {
	pr := persona.NewRegistry()
	require.NoError(t, pr.Register(&persona.Persona{
		Name:  "reader",
		Tools: persona.ToolRules{Allow: []string{"trino_query"}},
	}))

	p := &Platform{
		config:          &Config{Server: ServerConfig{Name: "res", Version: testInfoVersion}},
		personaRegistry: pr,
		toolkitRegistry: regWithTools(t, "search", "fetch", "trino_query"),
		resources:       resourcesEnabledLayer(t),
	}
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{PersonaName: "reader"})
	result, _, err := p.handleInfo(ctx, &mcp.CallToolRequest{})
	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	assert.Contains(t, info.AgentInstructions, instructions.ResourcePositioning)
	assert.Contains(t, info.AgentInstructions, "`resources/list`")
	assert.NotContains(t, info.AgentInstructions, "`search`",
		"a persona denied search must not be told to call it")
}

// TestHandleInfo_NoResourcesNoteWithoutManagedResources proves the section is
// absent (not merely empty) on a deployment with no resource store, so an agent
// is never steered at a library that does not exist.
func TestHandleInfo_NoResourcesNoteWithoutManagedResources(t *testing.T) {
	p := &Platform{
		config:          &Config{Server: ServerConfig{Name: "res", Version: testInfoVersion}},
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: regWithTools(t, "search", "fetch"),
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})
	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	assert.NotContains(t, info.AgentInstructions, instructions.ResourcePositioning)
	assert.NotContains(t, info.AgentInstructions, "Uploaded reference material:")
	assert.False(t, info.Features.ManagedResources)
}

func TestInfoFeatures(t *testing.T) {
	config := Config{
		Server: ServerConfig{
			Name:    "feature-test",
			Version: testInfoVersion,
		},
		Enrichment: EnrichmentConfig{
			TrinoSemanticEnrichment:  new(true),
			DataHubQueryEnrichment:   new(true),
			S3SemanticEnrichment:     new(false),
			DataHubStorageEnrichment: new(true),
		},
		Audit: AuditConfig{
			Enabled: new(true),
		},
	}

	p := &Platform{
		config:           &config,
		personaRegistry:  persona.NewRegistry(),
		toolkitRegistry:  registry.NewRegistry(),
		semanticProvider: semantic.NewNoopProvider(),
		queryProvider:    query.NewNoopProvider(),
		storageProvider:  storage.NewNoopProvider(),
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	assert.True(t, info.Features.SemanticEnrichment, "semantic enrichment should be enabled")
	assert.True(t, info.Features.QueryEnrichment, "query enrichment should be enabled")
	assert.True(t, info.Features.StorageEnrichment, "storage enrichment should be enabled")
	assert.True(t, info.Features.AuditLogging, "audit logging should be enabled")
}

// TestInfoFeatures_EnrichmentReportedOffWithoutProvider verifies the honesty
// gate: with enrichment flags default-on (nil) but no providers configured,
// platform_info must NOT report enrichment as enabled, since nothing would be
// produced. The agent should not be told it has context it cannot receive.
func TestInfoFeatures_EnrichmentReportedOffWithoutProvider(t *testing.T) {
	config := Config{
		Server: ServerConfig{Name: "feature-test", Version: testInfoVersion},
		// Injection left zero-value: every enrichment flag is nil => default-on.
	}

	p := &Platform{
		config:          &config,
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: registry.NewRegistry(),
		// No providers configured.
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	assert.False(t, info.Features.SemanticEnrichment, "no semantic provider => not reported")
	assert.False(t, info.Features.QueryEnrichment, "no query provider => not reported")
	assert.False(t, info.Features.StorageEnrichment, "no storage provider => not reported")
}

func TestInfoConfigVersion(t *testing.T) {
	config := Config{
		APIVersion: testInfoVersionV1,
		Server: ServerConfig{
			Name:    "version-test",
			Version: testInfoVersion,
		},
	}

	p := &Platform{
		config:          &config,
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: registry.NewRegistry(),
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	assert.Equal(t, testInfoVersionV1, info.ConfigVersion.APIVersion)
	assert.Equal(t, testInfoVersionV1, info.ConfigVersion.LatestVersion)
	assert.Contains(t, info.ConfigVersion.SupportedVersions, testInfoVersionV1)
}

func TestInfoToolkitDescriptions(t *testing.T) {
	config := Config{
		Server: ServerConfig{Name: "desc-test", Version: testInfoVersion},
		Toolkits: map[string]any{
			"trino":   map[string]any{"description": "Run SQL queries against rdbms and opensearch catalogs"},
			"datahub": map[string]any{"description": "Browse the ACME data catalog"},
			"s3":      map[string]any{}, // no description — should be omitted
		},
	}

	p := &Platform{config: &config, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	require.NotNil(t, info.ToolkitDescriptions)
	assert.Equal(t, "Run SQL queries against rdbms and opensearch catalogs", info.ToolkitDescriptions["trino"])
	assert.Equal(t, "Browse the ACME data catalog", info.ToolkitDescriptions["datahub"])
	assert.NotContains(t, info.ToolkitDescriptions, "s3", "empty description should be omitted")
}

func TestInfoToolkitDescriptionsNilWhenNone(t *testing.T) {
	config := Config{
		Server: ServerConfig{Name: "no-desc-test", Version: testInfoVersion},
		Toolkits: map[string]any{
			"trino": map[string]any{},
		},
	}

	p := &Platform{config: &config, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	// Only the auto-injected platform description should be present
	require.NotNil(t, info.ToolkitDescriptions)
	assert.Len(t, info.ToolkitDescriptions, 1, "only platform description should be present")
	assert.NotEmpty(t, info.ToolkitDescriptions["platform"])
}

func TestInfoToolkits(t *testing.T) {
	config := Config{
		Server: ServerConfig{
			Name:    "toolkit-test",
			Version: testInfoVersion,
		},
		Toolkits: map[string]any{
			"trino":   map[string]any{"host": "localhost"},
			"datahub": map[string]any{"url": "http://localhost"},
			"s3":      map[string]any{"region": "us-east-1"},
		},
	}

	p := &Platform{
		config:          &config,
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: registry.NewRegistry(),
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	assert.Len(t, info.Toolkits, testInfoToolkitCount)
	assert.Equal(t, "platform", info.Toolkits[0], "platform should be prepended first")
	assert.Contains(t, info.Toolkits, "trino")
	assert.Contains(t, info.Toolkits, "datahub")
	assert.Contains(t, info.Toolkits, "s3")
	assert.NotEmpty(t, info.ToolkitDescriptions["platform"], "platform toolkit should have a description")
}

func newPersonaRegistry(t *testing.T) *persona.Registry {
	t.Helper()
	reg := persona.NewRegistry()
	_ = reg.Register(&persona.Persona{
		Name:        "analyst",
		DisplayName: "Data Analyst",
		Description: "Analyze data and run queries",
	})
	_ = reg.Register(&persona.Persona{
		Name:        "admin",
		DisplayName: "Administrator",
		Description: "Full access to all features",
	})
	return reg
}

func TestInfoPersona(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Name: "persona-test", Version: testInfoVersion},
	}

	t.Run("shows caller's persona from context", func(t *testing.T) {
		reg := newPersonaRegistry(t)
		p := &Platform{config: &cfg, personaRegistry: reg, toolkitRegistry: registry.NewRegistry()}

		ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
			PersonaName: "analyst",
		})
		result, _, err := p.handleInfo(ctx, &mcp.CallToolRequest{})

		require.NoError(t, err)
		info := requireInfoFromResult(t, result)

		require.NotNil(t, info.Persona)
		assert.Equal(t, "analyst", info.Persona.Name)
		assert.Equal(t, "Data Analyst", info.Persona.DisplayName)
		assert.Equal(t, "Analyze data and run queries", info.Persona.Description)
	})

	// With no persona on the context the caller mapped to none, and
	// platform_info reports that rather than naming a persona they were never
	// granted.
	t.Run("reports no persona when the caller mapped to none", func(t *testing.T) {
		reg := newPersonaRegistry(t)
		p := &Platform{config: &cfg, personaRegistry: reg, toolkitRegistry: registry.NewRegistry()}

		result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

		require.NoError(t, err)
		info := requireInfoFromResult(t, result)

		assert.Nil(t, info.Persona)
	})

	t.Run("no persona when no context and no default", func(t *testing.T) {
		reg := newPersonaRegistry(t)
		p := &Platform{config: &cfg, personaRegistry: reg, toolkitRegistry: registry.NewRegistry()}

		result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

		require.NoError(t, err)
		info := requireInfoFromResult(t, result)

		assert.Nil(t, info.Persona)
	})
}

func TestInfoPortalURL(t *testing.T) {
	t.Run("includes portal_url when configured", func(t *testing.T) {
		cfg := Config{
			Server: ServerConfig{Name: "portal-test", Version: testInfoVersion},
			Portal: PortalConfig{PublicBaseURL: "https://portal.example.com"},
		}
		p := &Platform{config: &cfg, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
		result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

		require.NoError(t, err)
		info := requireInfoFromResult(t, result)
		assert.Equal(t, "https://portal.example.com", info.PortalURL)
	})

	t.Run("omits portal_url when not configured", func(t *testing.T) {
		cfg := Config{
			Server: ServerConfig{Name: "no-portal-test", Version: testInfoVersion},
		}
		p := &Platform{config: &cfg, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
		result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

		require.NoError(t, err)
		info := requireInfoFromResult(t, result)
		assert.Empty(t, info.PortalURL)
	})
}

func TestInfoPlatformToolkitPrepended(t *testing.T) {
	t.Run("platform is always first even with no configured toolkits", func(t *testing.T) {
		cfg := Config{
			Server: ServerConfig{Name: "empty-tk-test", Version: testInfoVersion},
		}
		p := &Platform{config: &cfg, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
		result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

		require.NoError(t, err)
		info := requireInfoFromResult(t, result)
		require.NotEmpty(t, info.Toolkits)
		assert.Equal(t, "platform", info.Toolkits[0])
		assert.NotEmpty(t, info.ToolkitDescriptions["platform"])
	})

	t.Run("does not override operator-provided platform description", func(t *testing.T) {
		cfg := Config{
			Server: ServerConfig{Name: "custom-desc-test", Version: testInfoVersion},
			Toolkits: map[string]any{
				"platform": map[string]any{
					"description": "Our custom platform description",
				},
				"trino": map[string]any{},
			},
		}
		p := &Platform{config: &cfg, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
		result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

		require.NoError(t, err)
		info := requireInfoFromResult(t, result)
		assert.Equal(t, "platform", info.Toolkits[0])
		assert.Equal(t, "Our custom platform description", info.ToolkitDescriptions["platform"])
		assert.Contains(t, info.Toolkits, "trino")
	})
}

func TestResolveCallerPersona(t *testing.T) {
	cfg := Config{Server: ServerConfig{Name: "test"}}

	t.Run("returns nil when registry is empty and no context", func(t *testing.T) {
		p := &Platform{config: &cfg, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
		result := p.resolveCallerPersona(context.Background())
		assert.Nil(t, result)
	})

	t.Run("returns nil when persona name not found in registry", func(t *testing.T) {
		p := &Platform{config: &cfg, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
		ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
			PersonaName: "nonexistent",
		})
		result := p.resolveCallerPersona(ctx)
		assert.Nil(t, result)
	})
}
