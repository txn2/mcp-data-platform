package portal

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"

	"github.com/txn2/mcp-data-platform/internal/indexpolicy"
)

// resourceUpdateEmpty is what an update naming nothing to change is told.
const resourceUpdateEmpty = "update changes display_name, description, tags or index; pass at least one. " +
	"Nothing was changed."

// handleUpdateResource changes a file's display name, description, tags or
// how search indexes it (#2111), leaving its content and its address alone.
// Only the fields the call names change.
func (t *Toolkit) handleUpdateResource(
	ctx context.Context, input manageResourceInput,
) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Reference) == "" && strings.TrimSpace(input.Path) != "" {
		return t.describeFolder(ctx, input)
	}
	id, err := parseResourceReference(input.Reference)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	if err := validateIndex(input.Index); err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	u := updateOf(input)
	if !u.Fields() {
		return toolkit.ErrorResult(resourceUpdateEmpty), nil, nil
	}
	res, err := t.resourceWriter.Update(ctx, id, u, refClaims(ctx))
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	return toolkit.JSONResultTyped(resourceReport(res, 0, updateMessage(res)))
}

// updateOf is the edit a call names: a field left empty is not changed.
func updateOf(input manageResourceInput) resource.Update {
	u := resource.Update{Origin: indexpolicy.OriginAgent}
	if v := strings.TrimSpace(input.DisplayName); v != "" {
		u.DisplayName = &v
	}
	if v := strings.TrimSpace(input.Description); v != "" {
		u.Description = &v
	}
	if input.Tags != nil {
		u.Tags = normalizeTags(input.Tags)
	}
	if v := strings.TrimSpace(input.Index); v != "" {
		u.Index = &v
	}
	return u
}

// updateMessage says what the update left the file as, and for a file taken
// out of search, how it is still found.
func updateMessage(res *resource.Resource) string {
	switch res.IndexPolicy {
	case "none":
		return "Updated. This file is not in search one by one: list its folder to find it, " +
			"and search finds the folder."
	case "metadata":
		return "Updated. Search finds this file by its name, description, folder, filename and tags."
	default:
		return "Updated. Search finds this file by its details and its contents."
	}
}

// folderDescribed is what describing a folder reports.
type folderDescribed struct {
	Scope       string `json:"scope"`
	ScopeID     string `json:"scope_id,omitempty"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Message     string `json:"message"`
}

// describeFolder sets what a folder holds: an update naming a path and no
// reference is about the folder, not a file in it.
func (t *Toolkit) describeFolder(ctx context.Context, input manageResourceInput) (*mcp.CallToolResult, any, error) {
	desc := strings.TrimSpace(input.Description)
	if desc == "" {
		return toolkit.ErrorResult("describing a folder takes its path and a description. Nothing was changed."), nil, nil
	}
	addr := toolkit.ResourceAddress{Scope: input.Scope, ScopeID: input.ScopeID, Path: input.Path}
	if err := t.resourceWriter.DescribeFolder(ctx, addr, desc, refClaims(ctx)); err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	return toolkit.JSONResultTyped(folderDescribed{
		Scope: libraryOrUser(input.Scope), ScopeID: input.ScopeID, Path: input.Path, Description: desc,
		Message: "Described. When the folder holds files that are not in search one by one, " +
			"search finds the folder by this description, its path, and what it holds.",
	})
}

// libraryOrUser is the library an update names: the caller's own when it
// names none, as every manage_resource action defaults.
func libraryOrUser(scope string) string {
	if strings.TrimSpace(scope) == "" {
		return string(resource.ScopeUser)
	}
	return scope
}
