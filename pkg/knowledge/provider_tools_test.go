package knowledge

import (
	"context"
	"strings"
	"testing"
)

type fakeToolRanker struct {
	gotRoles []string
	gotLimit int
	matches  []ToolMatch
}

func (f *fakeToolRanker) RankTools(_ context.Context, _ string, roles []string, limit int) []ToolMatch {
	f.gotRoles, f.gotLimit = roles, limit
	return f.matches
}

func TestToolsProvider_Search(t *testing.T) {
	r := &fakeToolRanker{matches: []ToolMatch{{
		Name: "manage_site", Description: "Publish a static site. More detail follows.", Score: 3,
		Example: map[string]any{"command": "publish"},
	}}}
	p := NewToolsProvider(r)
	if p.Name() != SourceTools || p.Scope() != ScopeShared {
		t.Fatalf("name/scope = %s/%v", p.Name(), p.Scope())
	}
	hits, err := p.Search(context.Background(), Query{Intent: "publish a site", Caller: Caller{Roles: []string{"analyst"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Ref != "manage_site" || hits[0].Source != SourceTools {
		t.Fatalf("hits = %+v", hits)
	}
	if want := `Tool manage_site: Publish a static site. Example call: {"command":"publish"}`; hits[0].Text != want {
		t.Errorf("text = %q, want %q", hits[0].Text, want)
	}
	if len(r.gotRoles) != 1 || r.gotRoles[0] != "analyst" || r.gotLimit != 5 {
		t.Errorf("ranker got roles %v limit %d", r.gotRoles, r.gotLimit)
	}
	if hits, _ := p.Search(context.Background(), Query{Intent: " "}); hits != nil {
		t.Error("an empty intent searched")
	}
	var nilP *ToolsProvider
	if hits, _ := nilP.Search(context.Background(), Query{Intent: "x"}); hits != nil {
		t.Error("a nil provider searched")
	}
	long := toolHitText(ToolMatch{Name: "t", Description: strings.Repeat("a", 400)})
	if !strings.HasSuffix(long, "...") {
		t.Errorf("a long description was not bounded: %d", len(long))
	}
}
