// This file adds the gate that keeps the anchored-editing grammar from being
// hand-copied (issue #1804).
//
// pkg/textpatch declares the edit grammar once — the operations, the keys each
// one reads, and an items schema closed to everything else — so that the
// grammar an agent meets on manage_asset is literally the grammar it meets on
// manage_prompt. manage_script did not splice it. It published a paraphrase
// whose edit items were a bare {"type": "object"}, which declared no keys and
// closed nothing, so the argument validator that refuses an unknown property at
// the top level of a tool call had nothing to check inside an edit: a
// replacement sent under a misspelled key was accepted, the anchor was replaced
// with an empty string, and the call reported a saved new version.
//
// The copy is the defect, not the wording of it. A tool that splices the shared
// fragment cannot drift; a tool that writes its own can, and the drift is
// invisible until someone sends the wrong key.
//
// Run: go test -run TestPatchGrammarIsNeverHandCopied .
package structure_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// handCopiedEditsProperty matches a declaration of the "edits" schema property,
// in either form the repository writes a schema in: a Go map literal, or a
// raw-string JSON fragment. Prose naming the argument does not match, because
// both forms require the colon and the opening brace.
var handCopiedEditsProperty = regexp.MustCompile(`"edits"\s*:\s*(map\[string\]any)?\s*\{`)

// TestPatchGrammarIsNeverHandCopied fails when a package other than
// pkg/textpatch declares the "edits" schema property itself.
//
// The remedy is never to fix the copy: splice textpatch.PropertiesMap() (or
// textpatch.AddProperties) into the tool's schema, which is what manage_asset,
// manage_prompt and manage_script all do.
func TestPatchGrammarIsNeverHandCopied(t *testing.T) {
	root := moduleRoot(t)
	owner := filepath.Join("pkg", "textpatch")

	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != root && skipWalkDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return fmt.Errorf("resolving %s against the module root: %w", path, relErr)
		}
		if strings.HasPrefix(rel, owner) {
			return nil
		}
		body, readErr := os.ReadFile(path) //nolint:gosec // walking the repository's own source
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", rel, readErr)
		}
		if handCopiedEditsProperty.Match(body) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	require.NoError(t, err)

	require.Empty(t, offenders,
		"these files declare the anchored-edit schema property themselves instead of splicing "+
			"pkg/textpatch: %v\nA hand-written copy is how manage_script came to advertise edit items "+
			"as a bare object, which left every key inside an edit unvalidated (#1804). "+
			"Use textpatch.PropertiesMap() or textpatch.AddProperties.", offenders)
}
