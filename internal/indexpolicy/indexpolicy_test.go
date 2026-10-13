package indexpolicy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchPath(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"pipelines/**", "pipelines", true},
		{"pipelines/**", "pipelines/nltv", true},
		{"pipelines/**", "pipelines/nltv/2019", true},
		{"pipelines/**", "brand/posters", false},
		{"brand/*", "brand/posters", true},
		{"brand/*", "brand/posters/2024", false},
		{"**/raw", "webhooks/blackbaud/raw", true},
		{"**", "", true},
		{"reports", "reports", true},
		{"reports", "reports/weekly", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, MatchPath(c.pattern, c.path), "%s ~ %s", c.pattern, c.path)
	}
}

func TestResolveTheTicketsExampleRules(t *testing.T) {
	r, err := NewResolver(Config{
		Default: "full",
		Rules: []Rule{
			{Path: "pipelines/**", Index: "none", Max: "metadata"},
			{Path: "brand/**", MIME: []string{"image/*"}, Index: "metadata"},
			{MIME: []string{"text/csv", "application/vnd.apache.parquet"}, SizeOver: "1MiB", Index: "none"},
			{Origin: []string{"webhook"}, Index: "none"},
			{Origin: []string{"catalog"}, Index: "metadata"},
		},
	})
	require.NoError(t, err)

	cases := []struct {
		name      string
		file      File
		requested Policy
		want      Decision
	}{
		{"a backfill nobody classified", File{Path: "pipelines/nltv", MIMEType: "text/csv", SizeBytes: 10}, "", Decision{None, false}},
		{"a backfill that asked for full is capped", File{Path: "pipelines/nltv", MIMEType: "text/csv"}, Full, Decision{Metadata, true}},
		{"a writer may ask for less than the ceiling", File{Path: "pipelines/nltv"}, None, Decision{None, true}},
		{"poster art a pipeline wrote", File{Path: "brand/posters", MIMEType: "image/png", Origin: OriginScript}, "", Decision{Metadata, false}},
		{"an uploaded style guide", File{Path: "guides", MIMEType: "application/pdf", Origin: OriginUpload}, "", Decision{Full, false}},
		{"a large CSV anywhere", File{Path: "exports", MIMEType: "text/csv", SizeBytes: 2 << 20}, "", Decision{None, false}},
		{"a small CSV falls to the built-in rule", File{Path: "exports", MIMEType: "text/csv; charset=utf-8", SizeBytes: 100}, "", Decision{Metadata, false}},
		{"a webhook window", File{Path: "webhooks/crm", MIMEType: "application/vnd.apache.parquet", Origin: OriginWebhook}, "", Decision{None, false}},
		{"a connection's schema document", File{Path: "catalog/api/billing", MIMEType: "application/yaml", Origin: OriginCatalog}, "", Decision{Metadata, false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, r.Resolve(c.file, c.requested))
		})
	}
}

func TestDefaultResolverKeepsTablesOfValuesOnMetadata(t *testing.T) {
	r := Default()
	assert.Equal(t, Metadata, r.Resolve(File{MIMEType: "application/x-ndjson"}, "").Policy)
	assert.Equal(t, Full, r.Resolve(File{MIMEType: "text/markdown"}, "").Policy)
	// A writer may still ask a table of values to be read in full.
	assert.Equal(t, Decision{Full, true}, r.Resolve(File{MIMEType: "text/csv"}, Full))
	var nilResolver *Resolver
	assert.Equal(t, Full, nilResolver.Resolve(File{MIMEType: "text/plain"}, "").Policy)
}

func TestValidateRefusesWhatItCannotRead(t *testing.T) {
	cases := map[string]Config{
		"default":        {Default: "some"},
		"index":          {Rules: []Rule{{Index: "partial"}}},
		"max":            {Rules: []Rule{{Max: "all"}}},
		"neither":        {Rules: []Rule{{Path: "x/**"}}},
		"scope":          {Rules: []Rule{{Scope: "team", Index: "none"}}},
		"origin":         {Rules: []Rule{{Origin: []string{"robot"}, Index: "none"}}},
		"size":           {Rules: []Rule{{SizeOver: "lots", Index: "none"}}},
		"negative size":  {Rules: []Rule{{SizeOver: "-1", Index: "none"}}},
		"malformed glob": {Rules: []Rule{{Path: "a/[", Index: "none"}}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewResolver(cfg)
			assert.Error(t, err)
		})
	}
}

func TestParse(t *testing.T) {
	p, err := Parse(" Metadata ")
	require.NoError(t, err)
	assert.Equal(t, Metadata, p)
	_, err = Parse("")
	require.ErrorIs(t, err, ErrInvalid)
	assert.True(t, Full.Indexed())
	assert.True(t, Metadata.Indexed())
	assert.False(t, None.Indexed())
	assert.True(t, Full.ReadsContent())
	assert.False(t, Metadata.ReadsContent())
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{"1MiB": 1 << 20, "500KB": 500_000, "2 GiB": 2 << 30, "77": 77, "10B": 10}
	for in, want := range cases {
		got, err := ParseSize(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
}

func TestScopeMatcher(t *testing.T) {
	r, err := NewResolver(Config{Rules: []Rule{{Scope: "user", Index: "metadata"}}})
	require.NoError(t, err)
	assert.Equal(t, Metadata, r.Resolve(File{Scope: "user", MIMEType: "text/plain"}, "").Policy)
	assert.Equal(t, Full, r.Resolve(File{Scope: "global", MIMEType: "text/plain"}, "").Policy)
}

func TestConfigResolverFallsBackToTheBuiltinRules(t *testing.T) {
	good := Config{Rules: []Rule{{Path: "x/**", Index: "none"}}}
	assert.Equal(t, None, good.Resolver().Resolve(File{Path: "x/y"}, "").Policy)
	bad := Config{Default: "some"}
	assert.Equal(t, Metadata, bad.Resolver().Resolve(File{MIMEType: "text/csv"}, "").Policy)
}
