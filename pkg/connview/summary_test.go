package connview

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSummarize_SixtyConnectionsFitUnder16KB(t *testing.T) {
	long := "Orders and their line items for the retail business. " + strings.Repeat("Usage rule: always filter by region before joining to shipments. ", 40)
	var out Output
	for i := range 60 {
		kind := []string{"trino", "api", "s3", "appdb"}[i%4]
		out.Connections = append(out.Connections, Entry{
			Kind: kind, Name: fmt.Sprintf("connection-%02d", i), Connection: fmt.Sprintf("connection-%02d", i),
			Reference: fmt.Sprintf("mcp:connection:(%s,connection-%02d)", kind, i), Description: long,
			KnowledgePageCount: 3, KnowledgePages: []KnowledgePage{{ID: "p", Slug: "s", Title: "t"}},
		})
	}
	out.Count = 60
	full, _ := json.Marshal(out)
	sum := Summarize(out, func(kind string) []string { return []string{kind + "_query", kind + "_describe"} })
	raw, _ := json.Marshal(sum)
	if len(raw) >= 16*1024 {
		t.Fatalf("summary of 60 connections is %d bytes (full %d), want under 16 KB", len(raw), len(full))
	}
	if sum.Count != 60 || len(sum.Connections) != 60 || sum.Form != FormSummary || sum.Note == "" {
		t.Fatalf("summary dropped connections or its form: %d %s", len(sum.Connections), sum.Form)
	}
	if got := sum.Connections[0].Description; got != "Orders and their line items for the retail business." {
		t.Errorf("description = %q", got)
	}
	if sum.Connections[0].KnowledgePages != nil || sum.Connections[0].KnowledgePageCount != 3 {
		t.Error("summary kept the page sample or dropped the count")
	}
	if len(sum.KindTools) != 4 || sum.KindTools["appdb"][0] != "appdb_query" {
		t.Errorf("kind_tools = %v", sum.KindTools)
	}
	if out.Connections[0].KnowledgePages == nil {
		t.Error("Summarize changed the view it was given")
	}
}

func TestDetail(t *testing.T) {
	out := Output{Connections: []Entry{
		{Kind: "trino", Name: "warehouse", Connection: "wh", Description: "Long. Text.", maxConcurrentCalls: 2},
		{Kind: "api", Name: "crm"},
	}}
	d, ok := Detail(out, "wh", func(string) []string { return []string{"trino_query"} })
	if !ok || d.Count != 1 || d.Connections[0].Description != "Long. Text." || d.Form != FormDetail {
		t.Fatalf("detail = %+v %v", d, ok)
	}
	if len(d.CallLimits) != 1 || d.KindTools["trino"][0] != "trino_query" {
		t.Errorf("detail limits/tools = %+v %v", d.CallLimits, d.KindTools)
	}
	if _, ok := Detail(out, "nope", nil); ok {
		t.Error("an unknown name was found")
	}
	if got := Names(out); strings.Join(got, ",") != "warehouse,crm" {
		t.Errorf("names = %v", got)
	}
	if kindTools(out.Connections, func(string) []string { return nil }) != nil {
		t.Error("no tools gave a map")
	}
}

func TestFirstSentence(t *testing.T) {
	if got := firstSentence("One line\nsecond line"); got != "One line" {
		t.Errorf("firstSentence = %q", got)
	}
	if got := firstSentence(strings.Repeat("a", 300)); !strings.HasSuffix(got, "...") {
		t.Errorf("not bounded: %d", len(got))
	}
}
