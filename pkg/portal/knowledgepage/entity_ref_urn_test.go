package knowledgepage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEntityRef_URNRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		ref  EntityRef
		urn  string
	}{
		{"asset", EntityRef{TargetType: RefTargetAsset, AssetID: "asset-001"}, "mcp:asset:asset-001"},
		{"prompt", EntityRef{TargetType: RefTargetPrompt, PromptID: "11111111-1111-1111-1111-111111111111"}, "mcp:prompt:11111111-1111-1111-1111-111111111111"},
		{"collection", EntityRef{TargetType: RefTargetCollection, CollectionID: "coll-001"}, "mcp:collection:coll-001"},
		{"page", EntityRef{TargetType: RefTargetKnowledgePage, RefPageID: "kp-2"}, "mcp:knowledge_page:kp-2"},
		{"connection", EntityRef{TargetType: RefTargetConnection, ConnectionKind: "trino", ConnectionName: "warehouse"}, "mcp:connection:(trino,warehouse)"},
		{"datahub", EntityRef{TargetType: RefTargetDataHub, EntityURN: "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"}, "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"},
		{"insight", EntityRef{TargetType: RefTargetInsight, InsightID: "ins_01HK7"}, "mcp:insight:ins_01HK7"},
		{"memory", EntityRef{TargetType: RefTargetMemory, MemoryID: "mem_01HK7"}, "mcp:memory:mem_01HK7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.urn, c.ref.URN(), "serialize")
			got, err := ParseEntityRef(c.urn)
			require.NoError(t, err, "parse")
			assert.Equal(t, c.ref, got, "round-trip")
		})
	}
}

func TestEntityRef_URN_UnknownType(t *testing.T) {
	assert.Empty(t, EntityRef{TargetType: "bogus"}.URN())
}

func TestParseEntityRef_Errors(t *testing.T) {
	for _, s := range []string{
		"",
		"asset-001",                   // no scheme
		"mcp:",                        // no type/id
		"mcp:asset",                   // no id
		"mcp:asset:",                  // empty id
		"mcp:bogus:x",                 // unknown type
		"mcp:prompt:not-a-uuid",       // prompt id must be a UUID
		"mcp:connection:trino,wh",     // missing parens
		"mcp:connection:(trino)",      // missing name
		"mcp:connection:(,warehouse)", // empty kind
		// Crossed namespaces: a urn: reference embedding the internal mcp: scheme
		// (the exact corruption an agent produced) must be rejected, not stored.
		"urn:li:mcp:connection:(prometheus)",
		"urn:li:mcp:asset:a1",
	} {
		_, err := ParseEntityRef(s)
		assert.Error(t, err, "want error for %q", s)
	}
}

// TestParseEntityRef_RejectsCrossedScheme verifies the crossed-namespace error
// names the correct internal form so the caller can self-correct.
func TestParseEntityRef_RejectsCrossedScheme(t *testing.T) {
	_, err := ParseEntityRef("urn:li:mcp:connection:(prometheus)")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mixes the urn: and mcp: schemes")
}

// TestParseEntityRef_ExternalPassthrough checks any urn: prefix is treated as an
// external (DataHub) reference and stored verbatim.
func TestParseEntityRef_ExternalPassthrough(t *testing.T) {
	ref, err := ParseEntityRef("  urn:li:glossaryTerm:revenue  ")
	require.NoError(t, err)
	assert.Equal(t, RefTargetDataHub, ref.TargetType)
	assert.Equal(t, "urn:li:glossaryTerm:revenue", ref.EntityURN)
}

func TestParseCitableRef_RejectsPerUserForms(t *testing.T) {
	// The per-user forms parse and are fetchable, but are not citable on a shared
	// page (#699): ParseCitableRef rejects them while ParseEntityRef accepts them.
	for _, ref := range []string{"mcp:memory:mem_01HK7", "mcp:insight:ins_01HK7"} {
		parsed, err := ParseEntityRef(ref)
		require.NoError(t, err, "ParseEntityRef accepts %q (fetch uses it)", ref)
		assert.Contains(t, []string{RefTargetMemory, RefTargetInsight}, parsed.TargetType)

		_, err = ParseCitableRef(ref)
		require.Error(t, err, "ParseCitableRef must reject %q", ref)
		assert.Contains(t, err.Error(), "cannot be cited", "error explains why")
	}
}

func TestParseCitableRef_AllowsSharedForms(t *testing.T) {
	for _, ref := range []string{
		"mcp:asset:a1",
		"mcp:knowledge_page:kp1",
		"mcp:connection:(trino,wh)",
		"urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)",
	} {
		got, err := ParseCitableRef(ref)
		require.NoError(t, err, "citable ref %q", ref)
		assert.NotContains(t, []string{RefTargetMemory, RefTargetInsight}, got.TargetType)
	}
	// A malformed reference still errors through ParseCitableRef.
	if _, err := ParseCitableRef("mcp:bogus:x"); err == nil {
		t.Error("ParseCitableRef should propagate a parse error")
	}
}
