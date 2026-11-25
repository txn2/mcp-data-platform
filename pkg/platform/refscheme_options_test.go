package platform

import (
	"context"
	"slices"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// A managed script's session reads results canonical, and a script's code
// and what it reports are kept verbatim through manage_script and run_script
// (#2788).
func TestReferenceSchemeOptions(t *testing.T) {
	o := referenceSchemeOptions()
	if !o.Canonical(middleware.WithSource(context.Background(), middleware.SourceScript)) {
		t.Error("a script's session is not canonical")
	}
	if o.Canonical(context.Background()) || o.Canonical(middleware.WithSource(context.Background(), middleware.SourceREST)) {
		t.Error("an agent's or a REST caller's session is canonical")
	}
	for _, f := range []string{"source", "edits", "log", "result", "tests"} {
		if !slices.Contains(o.Verbatim["manage_script"], f) {
			t.Errorf("manage_script does not keep %s verbatim", f)
		}
	}
	if slices.Contains(o.Verbatim["run_script"], "source") || !slices.Contains(o.Verbatim["run_script"], "log") {
		t.Errorf("run_script's verbatim fields: %v", o.Verbatim["run_script"])
	}
	if !slices.Equal(o.PlatformText, []string{defaultInitTool}) {
		t.Errorf("platform text tools: %v", o.PlatformText)
	}
}
