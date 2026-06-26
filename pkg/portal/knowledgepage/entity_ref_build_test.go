package knowledgepage

import "testing"

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
	}
	for name, got := range cases {
		if got != "" {
			t.Errorf("%s: expected empty reference, got %q", name, got)
		}
	}
}
