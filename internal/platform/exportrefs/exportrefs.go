// Package exportrefs is platform.export's references= argument (#1834): what
// it may hold, the manage_asset call that declares it on the asset an export
// wrote, and the reading of a document, and of a script's source, for the
// references nothing declares.
//
// A document a script publishes names a managed resource or another asset by
// its reference, and the viewing surfaces rewrite a reference into a working
// URL only when the asset declares it. The declaration is made through
// manage_asset rather than here, so the author-can-read check and the grant
// notice are the ones an agent's save gets. It is its own package, as
// internal/platform/exporttable is, for scriptrun's size budget.
package exportrefs

import (
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/portal/contentrefs"
)

// Keyword is the platform.export argument a declaration is passed as.
const Keyword = "references"

// Tool is the tool a declaration is made through, the one an agent declares an
// asset's references with, and Call is how the write reads in a draft's list.
const (
	Tool = "manage_asset"
	Call = Tool + " action=update"
)

// Parse reads a references= argument, already converted to plain Go values,
// into the list of reference strings, each trimmed as the rewrite will match
// it. An empty list is returned as one: it is a decision to clear the asset's
// references.
func Parse(v any) ([]string, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("references must be a list of reference strings, got %T", v)
	}
	refs := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("each reference is a managed resource's mcp:// URI or an asset's mcp:asset:<id> reference, got %v", item)
		}
		refs = append(refs, strings.TrimSpace(s))
	}
	return refs, nil
}

// ErrNotADocument refuses a declaration on an output that has no markup to
// reference anything from: a bucket object or a library file has no
// references to record, and a tabular output is serialized by the platform.
var ErrNotADocument = errors.New("references are declared on a portal document, the output of a string body " +
	"written to \"portal\"; this output has no markup to reference anything from")

// Args is the manage_asset call that declares refs on the asset.
func Args(assetID string, refs []string) map[string]any {
	list := make([]any, len(refs))
	for i, uri := range refs {
		list[i] = uri
	}
	return map[string]any{"action": "update", "asset_id": assetID, Keyword: list}
}

// LogLine is the run log's record of the references an output's body names
// and does not declare, so a scheduled run's history says which pictures will
// not load whether or not the script printed its result.
func LogLine(output string, undeclared []string) string {
	return "undeclared_references: " + output + ": " + strings.Join(undeclared, ", ") +
		" (not declared by this export; unless the asset declares them some other way " + contentrefs.UndeclaredConsequence + ")"
}
