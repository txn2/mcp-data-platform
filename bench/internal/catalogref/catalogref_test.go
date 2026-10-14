package catalogref

import (
	"errors"
	"strings"
	"testing"
)

func TestDataset(t *testing.T) {
	if got, want := Dataset("orders"), "mcp:dataset:(trino,e2e,memory.bench.orders)"; got != want {
		t.Fatalf("Dataset = %q, want %q", got, want)
	}
	if got := FetchArgs("x")["reference"]; got != "x" {
		t.Fatalf("FetchArgs reference = %v", got)
	}
}

func TestParse(t *testing.T) {
	text := `{"found":true,"reference":"r","document":{"content":{"name":"orders","aspects":` +
		`{"description":"cents","tags":["bench"],"custom_properties":{"unit":"cents"}}}}}` + "\n\ntrailing enrichment"
	got, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "cents" || len(got.Tags) != 1 || got.CustomProperties["unit"] != "cents" {
		t.Fatalf("Parse = %+v", got)
	}
}

func TestParseFailures(t *testing.T) {
	cases := map[string]string{
		"not json":   "nope",
		"not found":  `{"found":false,"message":"stale reference"}`,
		"no content": `{"found":true,"document":{}}`,
	}
	for name, text := range cases {
		if _, err := Parse(text); err == nil {
			t.Errorf("%s: Parse succeeded", name)
		} else if name == "not found" && (!errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "stale reference")) {
			t.Errorf("not found error %q is not ErrNotFound carrying the fetch message", err)
		}
	}
}

func TestRenderRoundTrips(t *testing.T) {
	want := Curation{Description: "d", Tags: []string{"bench"}}
	got, err := Parse(Render("r", want))
	if err != nil || got.Description != "d" || len(got.Tags) != 1 {
		t.Fatalf("Parse(Render) = %+v, %v", got, err)
	}
}
