package knowledgepage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func scannedURNs(refs []EntityRef) []string {
	urns := make([]string, 0, len(refs))
	for _, r := range refs {
		urns = append(urns, r.URN())
	}
	return urns
}

func TestScanBodyRefs(t *testing.T) {
	body := `# Daily Sales

The [daily sales asset](mcp:asset:asset-001) is built from the
[warehouse connection](mcp:connection:(trino,warehouse)) and the
underlying dataset urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD).

See also the glossary term <urn:li:glossaryTerm:revenue> and the
related page [calendar](mcp:knowledge_page:kp-2). A duplicate
[asset link](mcp:asset:asset-001) should collapse.

This is just prose mentioning mcp but no real reference.`

	refs := ScanBodyRefs(body)
	got := scannedURNs(refs)

	assert.ElementsMatch(t, []string{
		"mcp:asset:asset-001",
		"mcp:connection:(trino,warehouse)",
		"urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)",
		"urn:li:glossaryTerm:revenue",
		"mcp:knowledge_page:kp-2",
	}, got, "should extract each distinct reference exactly once")

	for _, r := range refs {
		assert.Equal(t, RefSourceInline, r.Source, "scanned refs are inline")
	}
}

func TestScanBodyRefs_IgnoresCode(t *testing.T) {
	body := "A real ref [a](mcp:asset:real-1).\n\n" +
		"```\nmcp:asset:in-fence\nurn:li:dataset:(urn:li:dataPlatform:trino,in.fence.t,PROD)\n```\n\n" +
		"Inline `mcp:asset:in-code` example."
	got := scannedURNs(ScanBodyRefs(body))
	assert.Equal(t, []string{"mcp:asset:real-1"}, got, "refs inside code must be ignored")
}

func TestScanBodyRefs_NoneAndUnparseable(t *testing.T) {
	assert.Nil(t, ScanBodyRefs("just regular prose, no refs here"))
	// A malformed prompt id is skipped rather than producing a bad ref.
	assert.Empty(t, ScanBodyRefs("see [bad](mcp:prompt:not-a-uuid) and [also](mcp:bogus:x)"))
}

// TestScanBodyRefs_TrimsTrailingSentencePunctuation proves that an inline
// reference written in prose immediately before sentence punctuation scans to
// the intended target rather than absorbing that punctuation into the id (#704).
func TestScanBodyRefs_TrimsTrailingSentencePunctuation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{"period", "The fact lives in mcp:asset:asset-001.", []string{"mcp:asset:asset-001"}},
		{"comma", "See mcp:asset:asset-001, which is built nightly.", []string{"mcp:asset:asset-001"}},
		{"semicolon", "Use mcp:asset:asset-001; it is current.", []string{"mcp:asset:asset-001"}},
		{"colon", "Source: mcp:asset:asset-001: nightly load.", []string{"mcp:asset:asset-001"}},
		{"exclamation", "Check mcp:asset:asset-001!", []string{"mcp:asset:asset-001"}},
		{"question", "Did you read mcp:asset:asset-001?", []string{"mcp:asset:asset-001"}},
		{"bare urn tag", "Tagged urn:li:tag:revenue.", []string{"urn:li:tag:revenue"}},
		{"run of punctuation", "Really, mcp:asset:asset-001?!", []string{"mcp:asset:asset-001"}},
		{
			"parenthesized connection unaffected",
			"Connect via mcp:connection:(trino,acme).",
			[]string{"mcp:connection:(trino,acme)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scannedURNs(ScanBodyRefs(tt.body)))
		})
	}
}

// TestScanBodyRefs_PreservesInternalDot proves the trim only removes a trailing
// run of punctuation and never touches a "." inside the token. The bare
// mcp:asset: form is undelimited, so its id class includes "." and the whole
// dotted id reaches TrimRight: this is the case that actually exercises the trim
// (the parenthesized dataset form below terminates at ")" in the regex and never
// reaches TrimRight, so it is a control that proves the regex path is untouched).
func TestScanBodyRefs_PreservesInternalDot(t *testing.T) {
	// Undelimited token with both internal and trailing dots: only the trailing
	// dot is trimmed, the internal dots survive.
	assert.Equal(t,
		[]string{"mcp:asset:a.b.c"},
		scannedURNs(ScanBodyRefs("The rollup lives in mcp:asset:a.b.c.")),
		"internal dots in a bare id are preserved; only the trailing period is trimmed",
	)

	// Parenthesized dataset form: terminates at ")" in the regex, so the trailing
	// period is never part of the token (control for the regex, not the trim).
	assert.Equal(t,
		[]string{"urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"},
		scannedURNs(ScanBodyRefs("Dataset urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD).")),
		"parenthesized dataset path is matched whole and unaffected by the trim",
	)
}
