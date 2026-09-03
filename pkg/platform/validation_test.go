package platform

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// mockToolkitForValidation implements registry.Toolkit for validation tests.
type mockToolkitForValidation struct {
	kind  string
	name  string
	tools []string
}

func (m *mockToolkitForValidation) Kind() string                          { return m.kind }
func (m *mockToolkitForValidation) Name() string                          { return m.name }
func (*mockToolkitForValidation) Connection() string                      { return "" }
func (m *mockToolkitForValidation) Tools() []string                       { return m.tools }
func (*mockToolkitForValidation) RegisterTools(_ *mcp.Server)             {}
func (*mockToolkitForValidation) SetSemanticProvider(_ semantic.Provider) {}
func (*mockToolkitForValidation) SetQueryProvider(_ query.Provider)       {}
func (*mockToolkitForValidation) Close() error                            { return nil }

func TestValidateAgentInstructions(t *testing.T) {
	tests := []struct {
		name         string
		instructions string
		tools        []string
		wantWarnings []string // substrings expected in log output
		noWarnings   bool     // expect zero warnings
	}{
		{
			name:         "empty instructions produce no warnings",
			instructions: "",
			tools:        []string{"trino_query"},
			noWarnings:   true,
		},
		{
			name:         "valid tool references produce no warnings",
			instructions: "Use trino_query for SQL and datahub_search for discovery.",
			tools:        []string{"trino_query", "datahub_search"},
			noWarnings:   true,
		},
		{
			name:         "stale tool reference produces warning",
			instructions: "Use trino_old_tool to run queries.",
			tools:        []string{"trino_query"},
			wantWarnings: []string{"trino_old_tool"},
		},
		{
			name:         "multiple stale references produce multiple warnings",
			instructions: "Use datahub_old and s3_removed for data access.",
			tools:        []string{"trino_query"},
			wantWarnings: []string{"datahub_old", "s3_removed"},
		},
		{
			name:         "non-tool tokens are ignored",
			instructions: "The table has first_name and last_name columns. Use snake_case naming.",
			tools:        []string{"trino_query"},
			noWarnings:   true,
		},
		{
			name:         "platform_info is always recognized",
			instructions: "Use platform_info to discover capabilities.",
			tools:        []string{}, // no toolkit tools
			noWarnings:   true,
		},
		{
			name:         "mixed valid and invalid references",
			instructions: "Use trino_query for SQL. Also try trino_nonexistent for fun.",
			tools:        []string{"trino_query"},
			wantWarnings: []string{"trino_nonexistent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture log output
			var buf bytes.Buffer
			handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
			oldLogger := slog.Default()
			slog.SetDefault(slog.New(handler))
			defer slog.SetDefault(oldLogger)

			reg := registry.NewRegistry()
			if len(tt.tools) > 0 {
				_ = reg.Register(&mockToolkitForValidation{
					kind:  "test",
					name:  "primary",
					tools: tt.tools,
				})
			}

			p := &Platform{
				config:          &Config{Server: ServerConfig{AgentInstructions: tt.instructions}},
				toolkitRegistry: reg,
			}

			p.validateAgentInstructions()

			logOutput := buf.String()
			if tt.noWarnings {
				if logOutput != "" {
					t.Errorf("expected no warnings, got: %s", logOutput)
				}
				return
			}

			for _, want := range tt.wantWarnings {
				if !bytes.Contains(buf.Bytes(), []byte(want)) {
					t.Errorf("expected warning containing %q, got: %s", want, logOutput)
				}
			}
		})
	}
}

func TestValidatePersonaCoherence(t *testing.T) {
	tests := []struct {
		name         string
		tools        []string
		allow        []string
		deny         []string
		wantWarnings []string // substrings expected in log output
		noWarnings   bool
	}{
		{
			name:       "wildcard persona warns about nothing",
			tools:      []string{"search", "fetch", "memory_capture"},
			allow:      []string{"*"},
			noWarnings: true,
		},
		{
			name:         "search without fetch warns naming persona, tool and remedy",
			tools:        []string{"search", "fetch"},
			allow:        []string{"search"},
			wantWarnings: []string{"persona grants a capability it cannot complete", "subject", "granted=search", "missing=fetch", "tools.allow"},
		},
		{
			name:         "memory_capture without search warns",
			tools:        []string{"search", "memory_capture"},
			allow:        []string{"memory_capture"},
			wantWarnings: []string{"granted=memory_capture", "missing=search"},
		},
		{
			// The check is scoped to registered tools, so a deployment that
			// never registered fetch is not nagged about withholding it.
			name:       "unregistered fetch produces no warning",
			tools:      []string{"search"},
			allow:      []string{"search"},
			noWarnings: true,
		},
		{
			name:       "no registered tools produces no warning",
			tools:      nil,
			allow:      []string{"*"},
			noWarnings: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			oldLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			defer slog.SetDefault(oldLogger)

			reg := registry.NewRegistry()
			if len(tt.tools) > 0 {
				_ = reg.Register(&mockToolkitForValidation{kind: "test", name: "primary", tools: tt.tools})
			}

			personas := persona.NewRegistry()
			if err := personas.Register(&persona.Persona{
				Name:  "subject",
				Tools: persona.ToolRules{Allow: tt.allow, Deny: tt.deny},
			}); err != nil {
				t.Fatalf("registering persona: %v", err)
			}

			p := &Platform{toolkitRegistry: reg, personaRegistry: personas}

			p.validatePersonaCoherence()

			logOutput := buf.String()
			if tt.noWarnings {
				if logOutput != "" {
					t.Errorf("expected no warnings, got: %s", logOutput)
				}
				return
			}
			for _, want := range tt.wantWarnings {
				if !bytes.Contains(buf.Bytes(), []byte(want)) {
					t.Errorf("expected warning containing %q, got: %s", want, logOutput)
				}
			}
		})
	}
}

// TestValidatePersonaCoherenceNilRegistry proves the startup path tolerates a
// platform assembled without personas, which the stdio no-auth shape produces.
func TestValidatePersonaCoherenceNilRegistry(t *testing.T) {
	var buf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(oldLogger)

	reg := registry.NewRegistry()
	_ = reg.Register(&mockToolkitForValidation{kind: "test", name: "primary", tools: []string{"search", "fetch"}})

	p := &Platform{toolkitRegistry: reg}
	p.validatePersonaCoherence()

	if buf.Len() != 0 {
		t.Errorf("expected no warnings with no persona registry, got: %s", buf.String())
	}
}
