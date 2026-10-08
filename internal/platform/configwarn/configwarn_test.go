package configwarn

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/opsobs"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// fakeToolkit registers tools and, optionally, connections.
type fakeToolkit struct {
	kind, name string
	tools      []string
	conns      []string
}

func (f *fakeToolkit) Kind() string                        { return f.kind }
func (f *fakeToolkit) Name() string                        { return f.name }
func (*fakeToolkit) Connection() string                    { return "" }
func (f *fakeToolkit) Tools() []string                     { return f.tools }
func (*fakeToolkit) RegisterTools(*mcp.Server)             {}
func (*fakeToolkit) SetSemanticProvider(semantic.Provider) {}
func (*fakeToolkit) SetQueryProvider(query.Provider)       {}
func (*fakeToolkit) Close() error                          { return nil }
func (f *fakeToolkit) ListConnections() []toolkit.ConnectionDetail {
	out := make([]toolkit.ConnectionDetail, 0, len(f.conns))
	for _, c := range f.conns {
		out = append(out, toolkit.ConnectionDetail{Name: c})
	}
	return out
}

// captureLog routes slog at WARN into a buffer for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// withMetrics installs an enabled recorder for the test and returns a scrape.
func withMetrics(t *testing.T) func() string {
	t.Helper()
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	prev := opsobs.Metrics()
	opsobs.SetMetrics(m)
	t.Cleanup(func() { opsobs.SetMetrics(prev) })
	return func() string {
		rec := httptest.NewRecorder()
		m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
		b, _ := io.ReadAll(rec.Body)
		return string(b)
	}
}

func newRegistry(t *testing.T, tks ...*fakeToolkit) *registry.Registry {
	t.Helper()
	reg := registry.NewRegistry()
	for _, tk := range tks {
		if err := reg.Register(tk); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

func newPersonas(t *testing.T, ps ...*persona.Persona) *persona.Registry {
	t.Helper()
	reg := persona.NewRegistry()
	for _, p := range ps {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

// TestCheckStartup_PersonaToolUnregistered holds the acceptance sentence: a
// config naming a persona tool that is not registered starts with
// config_validation_warnings_total{code="persona_tool_unregistered"} at 1.
func TestCheckStartup_PersonaToolUnregistered(t *testing.T) {
	scrape := withMetrics(t)
	logs := captureLog(t)
	tools := newRegistry(t, &fakeToolkit{kind: "trino", name: "primary", tools: []string{"trino_query"}, conns: []string{"warehouse"}})
	personas := newPersonas(t, &persona.Persona{
		Name:        "analyst",
		Tools:       persona.ToolRules{Allow: []string{"trino_query", "trino_*", "trino_retired_tool", "platform_info"}},
		Connections: persona.ConnectionRules{Allow: []string{"*"}},
	})

	CheckStartup(context.Background(), Startup{
		Toolkits: tools, Personas: personas, Registered: []string{"trino_query", "platform_info"},
	})

	if !strings.Contains(scrape(), `config_validation_warnings_total{code="persona_tool_unregistered"} 1`) {
		t.Errorf("warning not counted:\n%s", scrape())
	}
	out := logs.String()
	for _, want := range []string{"persona allows a tool this deployment does not register", "persona=analyst", "tool=trino_retired_tool", "code=persona_tool_unregistered"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "unused_connection") {
		t.Errorf("a reachable connection was reported unused:\n%s", out)
	}
}

func TestCheckStartup_UnusedConnection(t *testing.T) {
	scrape := withMetrics(t)
	logs := captureLog(t)
	tools := newRegistry(t, &fakeToolkit{kind: "trino", name: "primary", tools: []string{"trino_query"}, conns: []string{"warehouse", "orphan"}})
	personas := newPersonas(t, &persona.Persona{
		Name: "analyst", Tools: persona.ToolRules{Allow: []string{"*"}},
		Connections: persona.ConnectionRules{Allow: []string{"warehouse"}},
	})
	CheckStartup(context.Background(), Startup{Toolkits: tools, Personas: personas, Registered: []string{"trino_query"}})
	if !strings.Contains(scrape(), `config_validation_warnings_total{code="unused_connection"} 1`) {
		t.Errorf("unused connection not counted:\n%s", scrape())
	}
	if !strings.Contains(logs.String(), "connection=orphan") {
		t.Errorf("log does not name the connection:\n%s", logs.String())
	}
}

func TestCheckAgentInstructions(t *testing.T) {
	tests := []struct {
		name         string
		instructions string
		registered   []string
		want         []string
	}{
		{"empty", "", []string{"trino_query"}, nil},
		{"valid references", "Use trino_query for SQL and datahub_search for discovery.", []string{"trino_query", "datahub_search"}, nil},
		{"stale reference", "Use trino_old_tool to run queries.", []string{"trino_query"}, []string{"trino_old_tool"}},
		{"two stale", "Use datahub_old and s3_removed.", []string{"trino_query"}, []string{"datahub_old", "s3_removed"}},
		{"non-tool tokens", "The table has first_name and last_name columns.", []string{"trino_query"}, nil},
		{"platform tool registered", "Use platform_info to discover capabilities.", []string{"platform_info"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLog(t)
			CheckStartup(context.Background(), Startup{AgentInstructions: tt.instructions, Registered: tt.registered})
			out := logs.String()
			if len(tt.want) == 0 && out != "" {
				t.Errorf("expected no warnings, got: %s", out)
			}
			for _, w := range tt.want {
				if !strings.Contains(out, w) || !strings.Contains(out, "code=agent_instructions_unknown_tool") {
					t.Errorf("expected a warning naming %q, got: %s", w, out)
				}
			}
		})
	}
}

func TestPersonaCoherence(t *testing.T) {
	tests := []struct {
		name  string
		tools []string
		allow []string
		want  []string
	}{
		{"wildcard", []string{"search", "fetch", "memory_capture"}, []string{"*"}, nil},
		{
			"search without fetch",
			[]string{"search", "fetch"},
			[]string{"search"},
			[]string{"persona grants a capability it cannot complete", "granted=search", "missing=fetch", "code=persona_incoherent"},
		},
		{
			"memory_capture without search",
			[]string{"search", "memory_capture"},
			[]string{"memory_capture"},
			[]string{"granted=memory_capture", "missing=search"},
		},
		{"fetch not registered", []string{"search"}, []string{"search"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLog(t)
			p := &persona.Persona{Name: "subject", Tools: persona.ToolRules{Allow: tt.allow}}
			CheckStartup(context.Background(), Startup{
				Toolkits: newRegistry(t, &fakeToolkit{kind: "test", name: "primary", tools: tt.tools}),
				Personas: newPersonas(t, p), Registered: tt.tools,
			})
			out := logs.String()
			if len(tt.want) == 0 && out != "" {
				t.Errorf("expected no warnings, got: %s", out)
			}
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("expected %q in: %s", w, out)
				}
			}
		})
	}
}

// TestCheckStartup_NoRegistries tolerates a platform with no personas or
// toolkits, which the stdio no-auth shape produces.
func TestCheckStartup_NoRegistries(t *testing.T) {
	logs := captureLog(t)
	CheckStartup(context.Background(), Startup{})
	if logs.Len() != 0 {
		t.Errorf("warnings with nothing configured: %s", logs.String())
	}
	if Connections(nil) != nil {
		t.Error("connections of a nil registry")
	}
}

// TestCheckPersona is the admin-API path: the same checks for one persona.
func TestCheckPersona(t *testing.T) {
	scrape := withMetrics(t)
	captureLog(t)
	p := &persona.Persona{Name: "writer", Tools: persona.ToolRules{Allow: []string{"search", "gone_tool"}}}
	CheckPersona(context.Background(), p, []string{"search", "fetch"}, []string{"search", "fetch"})
	CheckPersona(context.Background(), nil, nil, nil)
	body := scrape()
	for _, want := range []string{
		`config_validation_warnings_total{code="persona_tool_unregistered"} 1`,
		`config_validation_warnings_total{code="persona_incoherent"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
}

// TestWarn_BufferedUntilFlush shows a warning raised before the recorder
// exists is counted when Flush runs, once.
func TestWarn_BufferedUntilFlush(t *testing.T) {
	prev := opsobs.Metrics()
	opsobs.SetMetrics(nil)
	t.Cleanup(func() { opsobs.SetMetrics(prev) })
	captureLog(t)

	Warn(context.Background(), CodeDeprecatedKey, "early warning")
	Flush(context.Background()) // no recorder yet: nothing to hand over
	scrape := withMetrics(t)
	if strings.Contains(scrape(), `code="deprecated_key"`) {
		t.Fatal("counted before Flush")
	}
	Flush(context.Background())
	Flush(context.Background())
	if !strings.Contains(scrape(), `config_validation_warnings_total{code="deprecated_key"} 1`) {
		t.Errorf("buffered warning not counted once:\n%s", scrape())
	}
}

func TestConnections_SortedAndDeduplicated(t *testing.T) {
	reg := newRegistry(t,
		&fakeToolkit{kind: "trino", name: "b", conns: []string{"z", "a"}},
		&fakeToolkit{kind: "s3", name: "c", conns: []string{"m"}},
	)
	got := Connections(reg)
	want := []Connection{{"s3", "m"}, {"trino", "a"}, {"trino", "z"}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
}
