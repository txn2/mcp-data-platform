package lifecycle

import (
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
)

func TestNormalizeText(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"  Net Revenue!  ", "net revenue"},
		{"Net\tRevenue\nexcludes  returns.", "net revenue excludes returns"},
		{"URN:li:dataset", "urn li dataset"},
		{"---", ""},
		{"a1b2", "a1b2"},
	}
	for _, c := range cases {
		if got := normalizeText(c.in); got != c.want {
			t.Errorf("normalizeText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFactSurfaced(t *testing.T) {
	fact := "Net revenue excludes returns and discounts."
	surfaced := []llm.Message{{Role: "user", ToolResults: []llm.ToolResult{
		{Text: "search results\nDescription: NET REVENUE excludes returns and discounts. (owner: fin)"},
	}}}
	notSurfaced := []llm.Message{{Role: "user", ToolResults: []llm.ToolResult{
		{Text: "search results: 3 datasets matched, none documented"},
	}}}
	errorResult := []llm.Message{{Role: "user", ToolResults: []llm.ToolResult{
		{Text: "Net revenue excludes returns and discounts.", IsError: true}, // error results do not count as surfaced
	}}}

	if !factSurfaced(fact, surfaced) {
		t.Error("expected fact surfaced when present in a result (case/punctuation-insensitive)")
	}
	if factSurfaced(fact, notSurfaced) {
		t.Error("expected fact not surfaced when absent")
	}
	if factSurfaced(fact, errorResult) {
		t.Error("an error tool result must not count as surfacing")
	}
	if factSurfaced("", surfaced) {
		t.Error("an empty fact must never count as surfaced")
	}
}

func TestSurfacedTarget(t *testing.T) {
	datahub := protocol.Protocol{Sink: protocol.SinkDataHub, Fact: "the fact"}
	if got := surfacedTarget(datahub); got != "the fact" {
		t.Errorf("datahub sink target = %q, want the fact", got)
	}
	// The page-sink needle is the SUMMARY, not the body: search renders a page
	// hit as title plus summary and the a3 surface has no page-body fetch, so a
	// body needle could never appear in a tool result there.
	page := protocol.Protocol{Sink: protocol.SinkKnowledgePage, Fact: "the fact",
		Page: &protocol.PagePayload{Summary: "the page summary", Body: "the page body"}}
	if got := surfacedTarget(page); got != "the page summary" {
		t.Errorf("page sink target = %q, want the page summary", got)
	}
	// A page sink with no page payload falls back to the fact rather than panicking.
	if got := surfacedTarget(protocol.Protocol{Sink: protocol.SinkKnowledgePage, Fact: "the fact"}); got != "the fact" {
		t.Errorf("page sink without payload target = %q, want the fact", got)
	}
}
