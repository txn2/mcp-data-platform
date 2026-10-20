//go:build integration

package acceptance

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// Issue #2216: v1.146.0 removed features.query_enrichment and
// features.storage_enrichment from platform_info, and every release up to
// v1.145.0 advertised both as required, so a client still holding one of
// those tool lists refused platform_info, the first call of every session.
// The criterion reads platform_info on the running platform, as each session
// does, and validates it against the output schema v1.135.2 advertised
// (testdata/issue_1971_platform_info_v1.135.2.json) and the one v1.145.0, the
// last release before the removal, advertised
// (testdata/issue_2216_platform_info_v1.145.0.json), both as their tools/list
// returned them.
//
// Wire forms: platform_info takes no parameters and is called with none, by
// the connect that opens each session.

// issue2216Schema resolves a recorded platform_info output schema.
func issue2216Schema(t *testing.T, file string) *jsonschema.Resolved {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("%s does not parse: %v", file, err)
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		t.Fatalf("resolving %s: %v", file, err)
	}
	return resolved
}

// TestIssue2216_PlatformInfoPassesEveryEarlierSchema covers both expectations:
// the result passes the schemas of v1.135.2 and v1.145.0, and the two keys are
// present and false, since no deployment has DataHub enrichment.
func TestIssue2216_PlatformInfoPassesEveryEarlierSchema(t *testing.T) {
	info := connect(t).info
	features, _ := info["features"].(map[string]any)
	for _, key := range []string{"query_enrichment", "storage_enrichment"} {
		if v, ok := features[key]; !ok || v != false {
			t.Errorf("features.%s = %v (present %v), want false", key, v, ok)
		}
	}

	var instance any
	raw, _ := json.Marshal(info)
	_ = json.Unmarshal(raw, &instance)
	for version, file := range map[string]string{
		"v1.135.2": "testdata/issue_1971_platform_info_v1.135.2.json",
		"v1.145.0": "testdata/issue_2216_platform_info_v1.145.0.json",
	} {
		if err := issue2216Schema(t, file).Validate(instance); err != nil {
			t.Errorf("platform_info is rejected by the schema %s advertised: %v", version, err)
		}
	}
}
