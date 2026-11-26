package platform

import (
	"context"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/refscheme"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// scriptCodeFields are manage_script's argument and result fields that hold
// a script's code or a piece of it: the source, the edits a patch applies and
// what it finds, and the text, lines and diff a read, locate or dry run
// answers with.
var scriptCodeFields = []string{"source", "edits", "find", "pattern", "replace", "text", "before", "after", "diff", "lines"}

// scriptOutputFields are the result fields that report what a script did:
// what it printed and returned, the state it saved, its error, and its tests
// with their logs and failures. A script runs canonical, so these hold the
// mcp: form; shown under the scheme, an agent wrote a test expecting acme:
// that the script never produces, and its failure read `got "acme:..." want
// "acme:..."` (#2788).
var scriptOutputFields = []string{"log", "result", "state", "checkpoint", "state_discarded", "error", "failure", "tests"}

// referenceSchemeOptions is what the reference scheme's MCP boundary needs to
// know about the platform: platform_info's result is the platform's text, a
// managed script's session reads results canonical, and a script's code and
// what it reports are never rewritten, through manage_script or run_script
// (#2788). The scheme still applies where a script's output leaves for a
// person: an endpoint's answer and a delivered notification.
func referenceSchemeOptions() refscheme.Options {
	return refscheme.Options{
		PlatformText: []string{defaultInitTool},
		Canonical: func(ctx context.Context) bool {
			return middleware.GetSource(ctx) == middleware.SourceScript
		},
		Verbatim: map[string][]string{
			"manage_script": slices.Concat(scriptCodeFields, scriptOutputFields),
			"run_script":    scriptOutputFields,
		},
	}
}
