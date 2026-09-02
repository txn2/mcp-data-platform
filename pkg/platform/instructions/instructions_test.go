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
		wantPrompt  bool
		wantScript  bool
		wantEmpty   bool
	}{
		{
			// Tools the baseline has nothing to say about. trino_query is
			// deliberately not among them any more: it carries the
			// inline-VALUES bullet (#1326).
			name:      "no accessible tools yields empty baseline",
			tools:     []string{"datahub_get_entity", "s3_list_objects"},
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
			name:       "manage_prompt adds the resolve-named-procedures bullet",
			tools:      []string{"manage_prompt"},
			wantPrompt: true,
		},
		{
			name:       "manage_script adds the settled-work bullet",
			tools:      []string{"manage_script"},
			wantScript: true,
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
			mentionsPrompt := strings.Contains(got, "`manage_prompt`")
			mentionsScript := strings.Contains(got, "`manage_script`")
			if mentionsSearch != tt.wantSearch {
				t.Errorf("mentions search = %v, want %v (baseline: %q)", mentionsSearch, tt.wantSearch, got)
			}
			if mentionsCapture != tt.wantCapture {
				t.Errorf("mentions memory_capture = %v, want %v (baseline: %q)", mentionsCapture, tt.wantCapture, got)
			}
			if mentionsApply != tt.wantApply {
				t.Errorf("mentions apply_knowledge = %v, want %v (baseline: %q)", mentionsApply, tt.wantApply, got)
			}
			if mentionsPrompt != tt.wantPrompt {
				t.Errorf("mentions manage_prompt = %v, want %v (baseline: %q)", mentionsPrompt, tt.wantPrompt, got)
			}
			if mentionsScript != tt.wantScript {
				t.Errorf("mentions manage_script = %v, want %v (baseline: %q)", mentionsScript, tt.wantScript, got)
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

// The capture route is the only way a query answered in conversation becomes
// something the next person can find (#1321). If the baseline stops naming it,
// the platform stops asking for it.
func TestBuild_NamesTheCaptureRoute(t *testing.T) {
	baseline := Build([]string{toolSearch, toolFetch, toolMemoryCapture})

	for _, want := range []string{"call_id", "`memory_capture` `sources`", "worth"} {
		if !strings.Contains(baseline, want) {
			t.Errorf("baseline is missing %q:\n%s", want, baseline)
		}
	}

	// It is named only where the tool that performs it is available.
	if strings.Contains(Build([]string{toolSearch, toolFetch}), "call_id") {
		t.Error("a caller without memory_capture must not be told to use it")
	}
}

// A short list of outside keys joins inline and needs no table (#1326).
// Without the hint the agent either asks for a table it cannot create or
// refuses the request outright, so what is asserted is that the baseline says
// so, and says where the bound is.
func TestBuild_NamesTheInlineJoin(t *testing.T) {
	baseline := Build([]string{toolTrinoQuery})

	for _, want := range []string{"VALUES", "IN (...)", "register it as a table"} {
		if !strings.Contains(baseline, want) {
			t.Errorf("baseline is missing %q:\n%s", want, baseline)
		}
	}

	// It is named only where the tool that performs it is available: a caller
	// with no query tool has nothing to join anything with.
	if strings.Contains(Build([]string{toolSearch, toolFetch}), "VALUES") {
		t.Error("a caller without trino_query must not be told to join inline")
	}
}

// The page index is what makes the platform's shipped guidance reachable on the
// first attempt: a search surfaces a page only once an agent already suspects it
// needs one, and the decision the page informs is made before any tool call.
func TestBuild_PageIndexNamesTheShippedGuidance(t *testing.T) {
	got := Build([]string{"manage_script", toolSaveAsset, toolMemoryCapture, toolFetch})
	for _, want := range []string{
		PageWritingManagedScripts,
		PageScriptOutputs,
		PageSemiDynamicDashboards,
		PageAssetReferences,
		PageProvenanceCapture,
		PageContentTypes,
	} {
		if !strings.Contains(got, "mcp:knowledge_page:"+want) {
			t.Errorf("page index does not name %q: %q", want, got)
		}
	}
}

// Each page is gated on the capability it documents, so a persona that cannot
// write scripts is not handed three pages about writing them.
func TestBuild_PageIndexGatesOnTheCapability(t *testing.T) {
	scriptsOnly := Build([]string{"manage_script", toolFetch})
	if strings.Contains(scriptsOnly, PageAssetReferences) {
		t.Errorf("a caller without save_asset was given the asset-references page: %q", scriptsOnly)
	}
	if !strings.Contains(scriptsOnly, PageWritingManagedScripts) {
		t.Errorf("a script author was not given the authoring page: %q", scriptsOnly)
	}

	assetsOnly := Build([]string{toolSaveAsset, toolFetch})
	if strings.Contains(assetsOnly, PageWritingManagedScripts) {
		t.Errorf("a caller without manage_script was given a scripts page: %q", assetsOnly)
	}
	for _, want := range []string{PageAssetReferences, PageContentTypes} {
		if !strings.Contains(assetsOnly, want) {
			t.Errorf("an asset author was not given %q: %q", want, assetsOnly)
		}
	}
}

// A knowledge page is returned by `fetch` and by `search` and by nothing else,
// so the index names the one the caller has -- and a caller with neither gets no
// index at all rather than references it cannot follow. This is the same rule
// the baseline applies to tools, extended to the pages it points at.
func TestBuild_PageIndexNamesOnlyAReachableRoute(t *testing.T) {
	withFetch := Build([]string{"manage_script", toolFetch})
	if !strings.Contains(withFetch, "`fetch` one by the reference") {
		t.Errorf("index did not name fetch for a caller that has it: %q", withFetch)
	}

	withSearch := Build([]string{"manage_script", toolSearch})
	if strings.Contains(withSearch, "`fetch`") {
		t.Errorf("index named fetch for a caller without it: %q", withSearch)
	}
	if !strings.Contains(withSearch, "`search` finds one by the slug") {
		t.Errorf("index did not fall back to search: %q", withSearch)
	}

	neither := Build([]string{"manage_script"})
	if strings.Contains(neither, "mcp:knowledge_page:") {
		t.Errorf("index named a page no tool the caller has can return: %q", neither)
	}
	if !strings.Contains(neither, "manage_script") {
		t.Errorf("the capability line itself must still appear: %q", neither)
	}
}

// The capability lines are an index, not a manual: each names its entry-point
// tool and the judgment made before reaching for it, and the depth is left to
// the pages. A line that grows into a manual is what put the baseline at 5.5 KB
// in the mandatory first call of every session (#1586).
func TestBuild_LinesStayAnIndex(t *testing.T) {
	all := []string{
		toolSearch, toolFetch, toolMemoryCapture, toolApplyKnowledge,
		toolManagePrompt, toolTrinoQuery, "manage_script", toolSaveAsset,
	}
	got := Build(all)
	if len(got) > 4000 {
		t.Errorf("the baseline is %d characters; it is an index and belongs well under 4000", len(got))
	}
	for line := range strings.SplitSeq(got, "\n") {
		if !strings.HasPrefix(line, "- ") || strings.Contains(line, "mcp:knowledge_page:") {
			continue
		}
		if len(line) > 560 {
			t.Errorf("capability line is %d characters, which is a manual rather than an index: %q", len(line), line)
		}
	}
}

// The asset bullet is the only thing that reaches an agent composing a document:
// the decision to name a file rather than embed it is made while the markup is
// being written, before any tool call the schema could inform.
func TestBuild_NamesTheReferenceRule(t *testing.T) {
	got := Build([]string{toolSaveAsset, toolFetch})
	for _, want := range []string{"`references`", "`save_asset`", "do not carry it"} {
		if !strings.Contains(got, want) {
			t.Errorf("baseline does not name %q: %q", want, got)
		}
	}
	if other := Build([]string{toolSearch, toolFetch}); strings.Contains(other, "do not carry it") {
		t.Errorf("the reference rule reached a caller without save_asset: %q", other)
	}
}
