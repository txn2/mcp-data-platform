package catalog

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestAspectsFor(t *testing.T) {
	if got := len(AspectsFor(KindDataset)); got != 11 {
		t.Errorf("dataset carries %d aspects, want 11", got)
	}
	if Allows(KindColumn, AspectCuratedQueries) || Allows(KindColumn, AspectIncidents) ||
		Allows(KindColumn, AspectContextDocuments) {
		t.Error("a column carries a dataset-only aspect")
	}
	if !Allows(KindColumn, AspectGlossaryTerms) {
		t.Error("a column should carry glossary terms")
	}
	if Allows(KindGlossaryTerm, AspectTags) || !Allows(KindGlossaryTerm, AspectDescription) {
		t.Error("a glossary term carries a definition's aspects only")
	}
	if AspectsFor("bogus") != nil {
		t.Error("an unknown kind carries no aspects")
	}
}

func TestNormalizeAspectRoundTripsEveryAspect(t *testing.T) {
	values := map[AspectName]string{
		AspectDescription:          `"Orders placed on the storefront."`,
		AspectTags:                 `["pii","finance"]`,
		AspectGlossaryTerms:        `["mcp:glossary_term:revenue"]`,
		AspectOwners:               `[{"owner":"ana@example.com","type":"technical_owner"}]`,
		AspectDomain:               `"mcp:domain:sales"`,
		AspectCustomProperties:     `{"refresh":"hourly"}`,
		AspectStructuredProperties: `{"retention_days":[90]}`,
		AspectDocumentationLinks:   `[{"url":"https://example.com/orders","description":"Runbook"}]`,
		AspectCuratedQueries:       `[{"name":"daily","sql":"SELECT 1","description":"per day"}]`,
		AspectIncidents:            `[{"id":"i1","type":"operational","title":"late","state":"active"}]`,
		AspectContextDocuments:     `[{"id":"d1","title":"Loading","content":"Loaded at 02:00","content_type":"text/markdown"}]`,
	}
	for _, name := range AspectsFor(KindDataset) {
		t.Run(string(name), func(t *testing.T) {
			in, ok := values[name]
			if !ok {
				t.Fatalf("no sample for %s", name)
			}
			out, err := NormalizeAspect(KindDataset, name, json.RawMessage(in))
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			_ = json.Unmarshal([]byte(in), &want)
			_ = json.Unmarshal(out, &got)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("normalized %s = %s, want %s", name, out, in)
			}
			var a Aspects
			if err := a.Set(name, out); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNormalizeAspectNullClears(t *testing.T) {
	for _, v := range []string{"", "null", "  null "} {
		out, err := NormalizeAspect(KindDataset, AspectDescription, json.RawMessage(v))
		if err != nil || out != nil {
			t.Errorf("%q: got %s, %v; want nil, nil", v, out, err)
		}
	}
}

func TestNormalizeAspectRefuses(t *testing.T) {
	tests := []struct {
		name  string
		kind  Kind
		asp   AspectName
		value string
		want  error
	}{
		{"curated queries on a column", KindColumn, AspectCuratedQueries, `[]`, ErrAspectNotAllowed},
		{"unknown aspect", KindDataset, "lineage", `[]`, ErrAspectNotAllowed},
		{"wrong shape", KindDataset, AspectTags, `"pii"`, ErrInvalidAspect},
		{"unknown field", KindDataset, AspectOwners, `[{"owner":"a","role":"x"}]`, ErrInvalidAspect},
		{"term that is a tag", KindDataset, AspectGlossaryTerms, `["mcp:tag:pii"]`, ErrInvalidAspect},
		{"term that is no reference", KindDataset, AspectGlossaryTerms, `["revenue"]`, ErrInvalidRef},
		{"domain that is a term", KindDataset, AspectDomain, `"mcp:glossary_term:x"`, ErrInvalidAspect},
		{"blank tag", KindDataset, AspectTags, `[" "]`, ErrInvalidAspect},
		{"owner with no owner", KindDataset, AspectOwners, `[{"type":"x"}]`, ErrInvalidAspect},
		{"link with no url", KindDataset, AspectDocumentationLinks, `[{"description":"x"}]`, ErrInvalidAspect},
		{"query with no sql", KindDataset, AspectCuratedQueries, `[{"name":"x"}]`, ErrInvalidAspect},
		{"incident with no id", KindDataset, AspectIncidents, `[{"title":"x","state":"active"}]`, ErrInvalidAspect},
		{"incident in an unknown state", KindDataset, AspectIncidents, `[{"id":"i","title":"x","state":"open"}]`, ErrInvalidAspect},
		{"document with no title", KindDataset, AspectContextDocuments, `[{"id":"d"}]`, ErrInvalidAspect},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeAspect(tt.kind, tt.asp, json.RawMessage(tt.value))
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestSetUnknownAspect(t *testing.T) {
	var a Aspects
	if err := a.Set("bogus", json.RawMessage(`1`)); !errors.Is(err, ErrInvalidAspect) {
		t.Fatalf("got %v", err)
	}
}

func TestWithTextPassesEveryFreeTextValueThroughFn(t *testing.T) {
	a := Aspects{
		Description:          "d",
		Tags:                 []string{"t"},
		GlossaryTerms:        []string{"mcp:glossary_term:g"},
		Owners:               []Owner{{Owner: "o", Type: "technical_owner"}},
		Domain:               "mcp:domain:x",
		CustomProperties:     map[string]string{"k": "v"},
		StructuredProperties: map[string][]any{"s": {"v", 1.0}},
		DocumentationLinks:   []Link{{URL: "https://example.com", Description: "l"}},
		CuratedQueries:       []CuratedQuery{{Name: "n", SQL: "SELECT 1", Description: "q"}},
		Incidents:            []Incident{{ID: "i", Title: "t", Description: "d", State: IncidentActive, Resolution: "r"}},
		ContextDocuments:     []ContextDocument{{ID: "c", Title: "t", Content: "c", Category: "k"}},
	}
	got := a.WithText(func(s string) string { return "<" + s + ">" })
	want := Aspects{
		Description:          "<d>",
		Tags:                 []string{"t"},
		GlossaryTerms:        []string{"mcp:glossary_term:g"},
		Owners:               []Owner{{Owner: "<o>", Type: "technical_owner"}},
		Domain:               "mcp:domain:x",
		CustomProperties:     map[string]string{"k": "<v>"},
		StructuredProperties: map[string][]any{"s": {"<v>", 1.0}},
		DocumentationLinks:   []Link{{URL: "https://example.com", Description: "<l>"}},
		CuratedQueries:       []CuratedQuery{{Name: "<n>", SQL: "SELECT 1", Description: "<q>"}},
		Incidents:            []Incident{{ID: "i", Title: "<t>", Description: "<d>", State: IncidentActive, Resolution: "<r>"}},
		ContextDocuments:     []ContextDocument{{ID: "c", Title: "<t>", Content: "<c>", Category: "<k>"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if a.Description != "d" || a.Owners[0].Owner != "o" {
		t.Fatal("the original curation was changed")
	}
	if empty := (Aspects{}).WithText(strings.ToUpper); empty.Owners != nil || empty.CustomProperties != nil {
		t.Fatalf("absent aspects stayed absent: %+v", empty)
	}
}
