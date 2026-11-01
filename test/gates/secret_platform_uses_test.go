package gates

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/botcheck"
	"github.com/txn2/mcp-data-platform/internal/scripttoken"
)

// platformUseRe matches one entry of PLATFORM_USES in the secret editor's form
// model: its reserved name and whether it refuses personas.
var platformUseRe = regexp.MustCompile(`\{\s*name:\s*"([^"]+)",\s*label:\s*"[^"]*",\s*noPersonas:\s*(true|false)\s*\}`)

// TestSecretEditorOffersEveryPlatformUse is #2388: a reserved secret use the
// platform reads (a bot check's provider key, a script token's signing key)
// is listed in allow_connections under a name that is no connection, and the
// secret editor offers exactly those names as Platform uses. A use added in Go
// and not offered there is a secret no administrator can create in the
// portal, which is how a provider bot check could not be set up.
func TestSecretEditorOffersEveryPlatformUse(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "ui", "src", "pages", "secrets", "secretForm.ts"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range platformUseRe.FindAllStringSubmatch(string(src), -1) {
		got[m[1]] = m[2]
	}
	want := map[string]string{
		botcheck.SecretUse:    "true",  // a provider key is read for every caller; it names no personas
		scripttoken.SecretUse: "false", // a token key's personas are checked against the script author's
	}
	if len(got) != len(want) {
		t.Errorf("PLATFORM_USES lists %v, want exactly %v", got, want)
	}
	for use, noPersonas := range want {
		if got[use] != noPersonas {
			t.Errorf("PLATFORM_USES entry %q: noPersonas %q, want %q", use, got[use], noPersonas)
		}
	}
}
