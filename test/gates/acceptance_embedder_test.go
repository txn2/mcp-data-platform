package gates

// An acceptance stack whose embedding model could not be pulled refuses to
// start (#2349). On E2E Nightly run 40640670647 the model's one download failed
// on two of the eight groups, dev/start.sh warned and went on, and nine
// criteria that search or embed failed after it, each naming its own symptom.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAnAcceptanceStackNeedsTheEmbedder: release-acceptance.sh starts the
// stack with DEV_REQUIRE_EMBEDDER=1, and start.sh retries the pull and fails
// under it, both for the model and for an Ollama that never answered.
func TestAnAcceptanceStackNeedsTheEmbedder(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "release-acceptance.sh"))
	require(t, err)
	if !strings.Contains(string(raw), `DEV_REQUIRE_EMBEDDER=1 bash dev/start.sh`) {
		t.Error("release-acceptance.sh does not start the stack with DEV_REQUIRE_EMBEDDER=1")
	}
	raw, err = os.ReadFile(filepath.Join("..", "..", "dev", "start.sh"))
	require(t, err)
	start := string(raw)
	for _, want := range []string{
		"for attempt in 1 2 3 4 5; do",
		`fail "could not pull nomic-embed-text after 5 attempts`,
		`fail "Ollama did not become ready`,
	} {
		if !strings.Contains(start, want) {
			t.Errorf("dev/start.sh does not contain %q", want)
		}
	}
	if n := strings.Count(start, `"${DEV_REQUIRE_EMBEDDER:-}" = "1"`); n != 2 {
		t.Errorf("dev/start.sh tests DEV_REQUIRE_EMBEDDER %d times, want 2 (the model pull and the Ollama wait)", n)
	}
}
