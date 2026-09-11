package connoauth

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// Which legacy OAuth config key becomes which canonical one is asked in two
// languages. Go asks it at the write boundary, to decide what gets persisted;
// the browser asks it when the connection editor loads a row, to decide which
// field to put a stored value in. The two disagreeing is the whole of #1681:
// the editor went on reading and writing oauth2_* after the server and a
// migration had moved to oauth_*, so an OAuth connection rendered with no auth
// configuration at all.
//
// Each language keeps one definition -- legacyPairs here, OAUTH_LEGACY_PAIRS
// in ui/src/pages/settings/connections/oauthVocabulary.ts -- and this reads the
// TypeScript one and fails when the two disagree on the pairs or their order.

// pairRe matches one entry of the TypeScript table. Whitespace is permissive
// because the formatter wraps a long entry across lines; a rewrite that changes
// the entry's shape matches nothing, which is reported rather than passing
// vacuously.
var pairRe = regexp.MustCompile(
	`\{\s*legacy:\s*"([^"]+)",\s*canonical:\s*"([^"]+)",?\s*\}`)

// vocabularyTS is the browser's copy of the vocabulary, read from source.
const vocabularyTS = "ui/src/pages/settings/connections/oauthVocabulary.ts"

func readVocabularyTS(t *testing.T) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine caller path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(vocabularyTS))) //nolint:gosec // test reads project sources
	if err != nil {
		t.Fatalf("reading %s: %v", vocabularyTS, err)
	}
	return body
}

// browserPairs is the TypeScript table, read as the legacy/canonical pairs it
// names, in the order it names them.
func browserPairs(t *testing.T) []legacyPair {
	t.Helper()
	body := readVocabularyTS(t)
	matches := pairRe.FindAllStringSubmatch(string(body), -1)
	pairs := make([]legacyPair, 0, len(matches))
	for _, m := range matches {
		pairs = append(pairs, legacyPair{legacy: m[1], canonical: m[2]})
	}
	if len(pairs) == 0 {
		t.Fatalf("%s: no legacy/canonical pairs found; the table's shape has changed "+
			"and this test can no longer read it", vocabularyTS)
	}
	return pairs
}

func TestGoAndBrowserAgreeOnTheOAuthVocabulary(t *testing.T) {
	browser := browserPairs(t)
	if len(browser) != len(legacyPairs) {
		t.Fatalf("pair counts disagree: Go has %d, the browser has %d (%v vs %v)",
			len(legacyPairs), len(browser), legacyPairs, browser)
	}
	for i := range legacyPairs {
		if legacyPairs[i] != browser[i] {
			t.Errorf("pair %d disagrees: Go says %v, the browser says %v", i, legacyPairs[i], browser[i])
		}
	}
}

// The legacy auth_mode values are the other half of the vocabulary, and the
// editor decides which grant to preselect from them.
func TestGoAndBrowserAgreeOnTheLegacyAuthModes(t *testing.T) {
	body := readVocabularyTS(t)
	for mode, grant := range map[string]string{
		legacyAuthModeClientCredentials: GrantClientCredentials,
		legacyAuthModeAuthorizationCode: GrantAuthorizationCode,
	} {
		want := regexp.MustCompile(regexp.QuoteMeta(mode) + `:\s*"` + regexp.QuoteMeta(grant) + `"`)
		if !want.Match(body) {
			t.Errorf("%s does not map %s to the %s grant", vocabularyTS, mode, grant)
		}
	}
}
