package instructions

import (
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

func TestComposeForCaller_LayersBaselineAdminAndNotes(t *testing.T) {
	tools := []string{"search", "memory_capture"}
	out := ComposeForCaller("ADMIN CONTEXT", tools, nil, nil, "RUNTIME NOTE")

	// Baseline leads, admin context sits beneath it, runtime note is last.
	if !strings.HasPrefix(out, "How to operate this platform:") {
		t.Errorf("baseline should lead, got: %q", out)
	}
	baseIdx := strings.Index(out, "How to operate this platform:")
	adminIdx := strings.Index(out, "ADMIN CONTEXT")
	noteIdx := strings.Index(out, "RUNTIME NOTE")
	if baseIdx >= adminIdx || adminIdx >= noteIdx {
		t.Errorf("expected order baseline < admin < note, got %d %d %d (%q)", baseIdx, adminIdx, noteIdx, out)
	}
}

func TestComposeForCaller_PersonaTunesAdminButNotBaseline(t *testing.T) {
	reg := persona.NewRegistry()
	// Override the admin layer entirely; the baseline must still be present.
	p := &persona.Persona{
		Name:  "p",
		Tools: persona.ToolRules{Allow: []string{"search", "memory_capture"}},
		Context: persona.ContextOverrides{
			AgentInstructionsOverride: "PERSONA OVERRIDE",
		},
	}
	out := ComposeForCaller("ADMIN CONTEXT", []string{"search"}, p, reg)
	if !strings.Contains(out, "How to operate this platform:") {
		t.Errorf("baseline must survive a persona override, got: %q", out)
	}
	if strings.Contains(out, "ADMIN CONTEXT") {
		t.Errorf("persona override should replace the admin layer, got: %q", out)
	}
	if !strings.Contains(out, "PERSONA OVERRIDE") {
		t.Errorf("persona override text should be present, got: %q", out)
	}
}

func TestComposeForCaller_SkipsBlankNotes(t *testing.T) {
	out := ComposeForCaller("ADMIN", []string{"search"}, nil, nil, "", "   ")
	if strings.HasSuffix(out, "\n\n") {
		t.Errorf("blank notes should not add trailing separators, got: %q", out)
	}
}

const defaultServerName = "mcp-data-platform"

func TestInfoToolTitle(t *testing.T) {
	tests := []struct {
		name       string
		serverName string
		wantTitle  string
	}{
		{"custom name is used as title", "ACME Data Platform", "ACME Data Platform"},
		{"default name returns Platform Info", defaultServerName, "Platform Info"},
		{"empty name returns Platform Info", "", "Platform Info"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InfoToolTitle(tt.serverName, defaultServerName, "Platform Info"); got != tt.wantTitle {
				t.Errorf("InfoToolTitle(%q) = %q, want %q", tt.serverName, got, tt.wantTitle)
			}
		})
	}
}

func TestInfoToolDescription(t *testing.T) {
	tests := []struct {
		name         string
		serverName   string
		tags         []string
		wantContains []string
	}{
		{
			name:       "default name uses generic description",
			serverName: defaultServerName,
			wantContains: []string{
				"MANDATORY first call",
				"Get information about this MCP data platform",
				"including its purpose",
			},
		},
		{
			name:       "custom name appears in description",
			serverName: "ACME Data Platform",
			wantContains: []string{
				"MANDATORY first call",
				"Get information about ACME Data Platform",
				"MUST be called before any other tool",
			},
		},
		{
			name:         "tags appear in parentheses",
			serverName:   "ACME Data Platform",
			tags:         []string{"analytics", "sales"},
			wantContains: []string{"Get information about ACME Data Platform", "(analytics, sales)"},
		},
		{
			name:         "empty tags omits parentheses",
			serverName:   "ACME Data Platform",
			tags:         []string{},
			wantContains: []string{"Get information about ACME Data Platform"},
		},
		{
			name:         "mentions consequences of skipping",
			serverName:   defaultServerName,
			wantContains: []string{"incorrect query routing", "operational rule violations", "degraded output quality"},
		},
		{
			name:         "mentions specific tools that must not precede it",
			serverName:   defaultServerName,
			wantContains: []string{"search", "trino_query", "trino_describe_table", "s3_list_objects"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desc := InfoToolDescription(tt.serverName, defaultServerName, tt.tags)
			for _, want := range tt.wantContains {
				if !strings.Contains(desc, want) {
					t.Errorf("description missing %q: %q", want, desc)
				}
			}
		})
	}
}

func TestBuild_GatesOnAccessibleTools(t *testing.T) {
	tests := []struct {
		name        string
		tools       []string
		wantSearch  bool
		wantCapture bool
		wantApply   bool
		wantEmpty   bool
	}{
		{
			name:      "no accessible tools yields empty baseline",
			tools:     []string{"trino_query", "datahub_get_entity"},
			wantEmpty: true,
		},
		{
			name:       "search only mentions search, not memory_capture",
			tools:      []string{"search", "trino_query"},
			wantSearch: true,
		},
		{
			name:        "memory_capture only mentions capture, not search",
			tools:       []string{"memory_capture"},
			wantCapture: true,
		},
		{
			name:        "both tools mention both",
			tools:       []string{"search", "memory_capture", "trino_query"},
			wantSearch:  true,
			wantCapture: true,
		},
		{
			name:      "apply_knowledge adds the synthesize bullet",
			tools:     []string{"apply_knowledge"},
			wantApply: true,
		},
		{
			name:      "nil tools yields empty baseline",
			tools:     nil,
			wantEmpty: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(tt.tools)
			if tt.wantEmpty {
				if got != "" {
					t.Fatalf("expected empty baseline, got %q", got)
				}
				return
			}
			if got == "" {
				t.Fatal("expected a non-empty baseline")
			}
			mentionsSearch := strings.Contains(got, "`search`")
			mentionsCapture := strings.Contains(got, "`memory_capture`")
			mentionsApply := strings.Contains(got, "`apply_knowledge`")
			if mentionsSearch != tt.wantSearch {
				t.Errorf("mentions search = %v, want %v (baseline: %q)", mentionsSearch, tt.wantSearch, got)
			}
			if mentionsCapture != tt.wantCapture {
				t.Errorf("mentions memory_capture = %v, want %v (baseline: %q)", mentionsCapture, tt.wantCapture, got)
			}
			if mentionsApply != tt.wantApply {
				t.Errorf("mentions apply_knowledge = %v, want %v (baseline: %q)", mentionsApply, tt.wantApply, got)
			}
		})
	}
}

func TestBuild_NeverNamesInaccessibleTool(t *testing.T) {
	// A caller with only memory_capture must never see the word "search" in the
	// baseline; that is the whole point of the per-tool gate.
	got := Build([]string{"memory_capture"})
	if strings.Contains(got, "`search`") {
		t.Errorf("baseline named search for a caller without it: %q", got)
	}
}

func TestBuild_NamesFetchOnlyWhenAccessible(t *testing.T) {
	// With fetch accessible, the reuse bullet teaches reading a result in full.
	withFetch := Build([]string{"search", "fetch"})
	if !strings.Contains(withFetch, "`fetch`") {
		t.Errorf("baseline should name fetch when accessible: %q", withFetch)
	}
	// Without fetch (a persona that denies it), the baseline must not name it.
	noFetch := Build([]string{"search"})
	if strings.Contains(noFetch, "`fetch`") {
		t.Errorf("baseline named fetch for a caller without it: %q", noFetch)
	}
	// Fetch alone (no search) says nothing: the reuse guidance hangs off search.
	if got := Build([]string{"fetch"}); got != "" {
		t.Errorf("fetch without search should yield an empty baseline, got %q", got)
	}
}

func TestBuild_HasHeaderWhenNonEmpty(t *testing.T) {
	got := Build([]string{"search"})
	if !strings.HasPrefix(got, "How to operate this platform:") {
		t.Errorf("expected header prefix, got %q", got)
	}
}

func TestBuild_NoEmDashes(t *testing.T) {
	// The project bans em dashes in all written artifacts.
	got := Build([]string{"search", "memory_capture"})
	if strings.Contains(got, "—") {
		t.Errorf("baseline contains an em dash: %q", got)
	}
}

func TestCompose(t *testing.T) {
	tests := []struct {
		name           string
		baseline, rest string
		want           string
	}{
		{"both present", "BASE", "ADMIN", "BASE\n\nADMIN"},
		{"empty baseline returns rest", "", "ADMIN", "ADMIN"},
		{"empty rest returns baseline", "BASE", "", "BASE"},
		{"both empty", "  ", "", ""},
		{"trims surrounding space", "  BASE  ", "  ADMIN  ", "BASE\n\nADMIN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Compose(tt.baseline, tt.rest); got != tt.want {
				t.Errorf("Compose(%q, %q) = %q, want %q", tt.baseline, tt.rest, got, tt.want)
			}
		})
	}
}

func TestAccessibleTools(t *testing.T) {
	all := []string{"search", "memory_capture", "trino_query"}

	// Nil persona: no filtering, all tools returned.
	if got := AccessibleTools(all, nil, nil); len(got) != 3 {
		t.Errorf("nil persona should return all tools, got %v", got)
	}

	// A persona allowing only search must drop memory_capture.
	reg := persona.NewRegistry()
	p := &persona.Persona{Name: "reader", Tools: persona.ToolRules{Allow: []string{"search"}}}
	got := AccessibleTools(all, p, reg)
	if len(got) != 1 || got[0] != "search" {
		t.Errorf("expected only search, got %v", got)
	}
}
