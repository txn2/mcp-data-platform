package instructions

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The positioning statement is only worth anything if every surface says the
// same thing. This test is the enforcement: it fails when the portal's copy or
// the documentation's copy drifts from the Go constant the agent is served.
//
// Each language keeps one definition (the Go constant here, the TypeScript
// constant the portal surfaces render, the paragraph the concepts page quotes),
// and this test asserts all three are byte-identical, so grepping the statement
// finds every place it is stated and none of them can be edited alone.

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine caller path")
	}
	// file = <root>/pkg/platform/instructions/positioning_verbatim_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func TestResourcePositioningIsVerbatim(t *testing.T) {
	root := repoRoot(t)

	// Every file that states the split. The portal renders the TypeScript
	// constant in both its resources empty state and the upload dialog, so those
	// two surfaces are covered by covering their one definition.
	for _, rel := range []string{
		"ui/src/lib/positioning.ts",
		"docs/concepts/content-model.md",
		"docs/server/portal-user.md",
	} {
		body, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // test reads project sources
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		if !strings.Contains(string(body), ResourcePositioning) {
			t.Errorf("%s does not carry the positioning statement verbatim; it must match "+
				"instructions.ResourcePositioning exactly:\n%s", rel, ResourcePositioning)
		}
	}
}

// The portal must render the shared constant rather than an inline copy of the
// same words, so a future edit to one surface cannot silently leave the other
// stating the old split.
func TestPortalResourceSurfacesRenderTheSharedConstant(t *testing.T) {
	root := repoRoot(t)

	// The two portal surfaces that state the split: the empty resources library,
	// and the dialog someone uploads through. Both render the constant; neither
	// may restate the words.
	for _, rel := range []string{
		"ui/src/pages/resources/parts/ResourceResults.tsx",
		"ui/src/pages/resources/modals/UploadModal.tsx",
	} {
		body, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // test reads project sources
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		src := string(body)
		if !strings.Contains(src, `from "@/lib/positioning"`) {
			t.Errorf("%s does not import the shared positioning constant", rel)
		}
		if !strings.Contains(src, "{RESOURCE_POSITIONING}") {
			t.Errorf("%s does not render RESOURCE_POSITIONING", rel)
		}
	}
}
