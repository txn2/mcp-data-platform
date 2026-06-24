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
