package resource

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/indexpolicy"
)

// FolderIndexKind is the index-jobs source kind a folder's one search entry is
// kept under (#2111). A folder is given one when it holds files that are not
// indexed one by one, so the folder itself is what search finds.
const FolderIndexKind = "resource_folders"

// folderKeySep separates a folder key's library from its path.
const folderKeySep = ":"

// FolderKey names one folder of one library as the index-jobs unit it is
// indexed under. The library's id is escaped, and the path follows the second
// separator whole, so a path holding the separator still reads back.
func FolderKey(lib ScopeFilter, path string) string {
	return string(lib.Scope) + folderKeySep + url.QueryEscape(lib.ScopeID) + folderKeySep + path
}

// ParseFolderKey reads a FolderKey back.
func ParseFolderKey(key string) (ScopeFilter, string, error) {
	scope, rest, ok := strings.Cut(key, folderKeySep)
	if !ok {
		return ScopeFilter{}, "", fmt.Errorf("folder key %q has no library", key)
	}
	escaped, path, ok := strings.Cut(rest, folderKeySep)
	if !ok {
		return ScopeFilter{}, "", fmt.Errorf("folder key %q has no path", key)
	}
	id, err := url.QueryUnescape(escaped)
	if err != nil {
		return ScopeFilter{}, "", fmt.Errorf("folder key %q: %w", key, err)
	}
	return ScopeFilter{Scope: Scope(scope), ScopeID: id}, path, nil
}

// decideIndex resolves the policy a new or revised file is indexed under: the
// writer's request, capped by the operator's rules, or the rules' default.
func decideIndex(deps Deps, f indexpolicy.File, requested string) (indexpolicy.Decision, error) {
	var want indexpolicy.Policy
	if requested != "" {
		p, err := indexpolicy.Parse(requested)
		if err != nil {
			return indexpolicy.Decision{}, fmt.Errorf("resource index: %w", err)
		}
		want = p
	}
	return deps.IndexPolicy.Resolve(f, want), nil
}

// originOf is where a write came from for the operator's rules. A write made
// under a managed script's principal is the script's, whichever surface it
// went through, and one under the webhook principal is the webhook's; anything
// else is what the surface says it is.
func originOf(claims *Claims, surface indexpolicy.Origin) indexpolicy.Origin {
	if claims != nil {
		switch {
		case strings.HasPrefix(claims.Sub, scriptPrincipalPrefix):
			return indexpolicy.OriginScript
		case claims.Sub == webhookPrincipal:
			return indexpolicy.OriginWebhook
		}
	}
	if surface == "" {
		return indexpolicy.OriginUpload
	}
	return surface
}

// The principals a write's origin is read from.
const (
	scriptPrincipalPrefix = "script:"
	webhookPrincipal      = "system:webhooks"
)

// indexTarget is one written resource as the index cares about it: whether it
// is indexed itself, and which folder it is counted in when it is not.
type indexTarget struct {
	id     string
	policy string
	lib    ScopeFilter
	path   string
}

// notifyIndex enqueues the work a committed write owes the index: the
// resource's own job when it is indexed, and its folder's when it is not. A
// policy change is both: the resource may have left its folder's entry or
// joined it.
func (s *postgresStore) notifyIndex(ctx context.Context, policyChanged bool, targets ...indexTarget) {
	for _, t := range targets {
		indexed := indexpolicy.Policy(t.policy).Indexed()
		if indexed {
			s.index.NotifyWrite(ctx, t.id)
		}
		if !indexed || policyChanged {
			s.folderIndex.NotifyWrite(ctx, FolderKey(t.lib, t.path))
		}
	}
}

// notifyMoved enqueues what a committed move owes the index. An indexed file
// is re-embedded on its new folder path; a file that is not indexed changed
// the summary of the folder it left and of the one it joined.
func (s *postgresStore) notifyMoved(ctx context.Context, moves []Move) {
	for _, m := range moves {
		if m.From.IndexPolicy == "" || indexpolicy.Policy(m.From.IndexPolicy).Indexed() {
			s.index.NotifyWrite(ctx, m.ID)
			continue
		}
		s.folderIndex.NotifyWrite(ctx, FolderKey(m.From.Library, m.From.Path))
		s.folderIndex.NotifyWrite(ctx, FolderKey(ScopeFilter{Scope: m.Scope, ScopeID: m.ScopeID}, m.Path))
	}
}

// indexPolicyOrFull is the policy a row is written with: a store write that
// did not resolve one (a fixture, a caller predating #2111) is indexed in
// full, which is what every resource was before policies existed.
func indexPolicyOrFull(p string) string {
	if p == "" {
		return string(indexpolicy.Full)
	}
	return p
}
