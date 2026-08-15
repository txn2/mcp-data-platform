package knowledgepage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReferenceBuilders_RoundTrip(t *testing.T) {
	const promptUUID = "11111111-1111-1111-1111-111111111111"
	tests := []struct {
		name    string
		got     string
		want    string
		wantTyp string
	}{
		{"asset", AssetRef("a1b2"), "mcp:asset:a1b2", RefTargetAsset},
		{"knowledge_page", PageReference("kp_36d8"), "mcp:knowledge_page:kp_36d8", RefTargetKnowledgePage},
		{"prompt uuid", PromptRef(promptUUID), "mcp:prompt:" + promptUUID, RefTargetPrompt},
		{"connection", ConnectionRef("api", "prometheus"), "mcp:connection:(api,prometheus)", RefTargetConnection},
		{"insight", InsightRef("ins_36d8"), "mcp:insight:ins_36d8", RefTargetInsight},
		{"memory", MemoryRef("mem_36d8"), "mcp:memory:mem_36d8", RefTargetMemory},
		{"resource", ResourceRef("res_36d8"), "mcp:resource:res_36d8", RefTargetResource},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("ref = %q, want %q", tc.got, tc.want)
			}
			parsed, err := ParseEntityRef(tc.got)
			if err != nil {
				t.Fatalf("ref %q does not round-trip: %v", tc.got, err)
			}
			if parsed.TargetType != tc.wantTyp {
				t.Errorf("parsed type = %q, want %q", parsed.TargetType, tc.wantTyp)
			}
		})
	}
}

func TestReferenceBuilders_EmptyForUnresolvable(t *testing.T) {
	cases := map[string]string{
		"empty asset id":     AssetRef(""),
		"empty page id":      PageReference(""),
		"empty prompt id":    PromptRef(""),
		"non-uuid prompt id": PromptRef("prompt_a1b2c3d4"),
		"empty connkind":     ConnectionRef("", "prometheus"),
		"empty connname":     ConnectionRef("api", ""),
		"empty insight id":   InsightRef(""),
		"empty memory id":    MemoryRef(""),
		"empty resource id":  ResourceRef(""),
	}
	for name, got := range cases {
		if got != "" {
			t.Errorf("%s: expected empty reference, got %q", name, got)
		}
	}
}

// TestScriptRef proves the one minter of a script reference: it round-trips
// through the parser, and refuses to emit anything for an empty id rather than
// producing a reference that resolves to nothing.
func TestScriptRef(t *testing.T) {
	ref := ScriptRef("script_01HK7")
	assert.Equal(t, "mcp:script:script_01HK7", ref)

	parsed, err := ParseEntityRef(ref)
	require.NoError(t, err)
	assert.Equal(t, "script_01HK7", parsed.ScriptID)

	assert.Empty(t, ScriptRef(""), "an empty id has no reference")
}
