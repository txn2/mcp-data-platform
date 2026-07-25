package apigen

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/pkseed"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// TestCommittedArtifactsMatch is the reproducibility gate: the committed
// specs and study task set must regenerate byte-identically from the
// fixed seeds (the bench/internal/gen pattern). Regenerate with
// `make bench-api-gen` after an intentional generator change.
func TestCommittedArtifactsMatch(t *testing.T) {
	c := BuildCatalog()
	root := "../.."
	for tier, name := range TierNames() {
		raw, err := c.SpecJSON(tier)
		if err != nil {
			t.Fatal(err)
		}
		compareFile(t, filepath.Join(root, "specs", name+".json"), raw)
	}
	committed, err := task.Load(filepath.Join(root, "tasks-api"))
	if err != nil {
		t.Fatalf("load committed tasks: %v", err)
	}
	generated := Tasks(GenerateState(c))
	if got, want := task.Hash(committed), task.Hash(generated); got != want {
		t.Errorf("committed task-set hash %s != regenerated %s; run `make bench-api-gen`", got, want)
	}
	if len(committed) != len(generated) {
		t.Errorf("committed %d tasks, regenerated %d", len(committed), len(generated))
	}
	smoke, err := json.MarshalIndent(ScriptedSmoke(generated), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	compareFile(t, filepath.Join(root, "tasks-api", "scripted-smoke.json"), append(smoke, '\n'))
}

// TestCommittedPerishableArtifactsMatch is the same gate for the
// perishable-knowledge study (#1054). Its three artifacts fix what a cell
// means: the spec the connection is registered from, the world registry
// each cell names a profile in, and the resolved fixture data every ground
// truth is computed against. A generator change that moves any of them has
// to move the committed copy in the same commit, where a reviewer sees it.
func TestCommittedPerishableArtifactsMatch(t *testing.T) {
	root := "../.."
	raw, err := BuildPerishableCatalog().SpecJSON(Tier0)
	if err != nil {
		t.Fatal(err)
	}
	compareFile(t, filepath.Join(root, "specs", PerishableSpecName+".json"), raw)
	compareIndentedJSON(t, filepath.Join(root, "specs", PerishableSpecName+"-world.json"),
		map[string]any{"profiles": WorldProfiles()})
	compareIndentedJSON(t, filepath.Join(root, "specs", PerishableSpecName+"-fixture.json"), BuildFixture())
	compareIndentedJSON(t, filepath.Join(root, "specs", PerishableSpecName+"-seeds.json"), map[string]any{
		"hash":    pkseed.Hash(),
		"beliefs": pkseed.Beliefs(),
		"seeds":   pkseed.Seeds(),
	})
}

// compareIndentedJSON diffs one committed JSON artifact against its
// regeneration, in the form the generator writes it.
func compareIndentedJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	compareFile(t, path, append(raw, '\n'))
}

// compareFile diffs one committed artifact against its regeneration.
func compareFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path) // #nosec G304 -- repo-relative test fixture path
	if err != nil {
		t.Errorf("read %s: %v (run `make bench-api-gen`)", path, err)
		return
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s does not match regeneration; run `make bench-api-gen`", path)
	}
}
