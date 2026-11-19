package portal

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/portal/assetbulk"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Bulk manage_asset (#2507): update, delete and share take asset_ids, several
// assets at once, so an agent tidies a library the way a person does from the
// Assets tab. Each asset goes through the action's own single-asset handler,
// with its own authorization; nothing is applied to an asset the caller may
// not act on, and an asset the caller cannot open is reported with the one
// reason assetbulk.ReasonHidden whether or not it exists.

// bulkActions are the actions asset_ids applies to.
var bulkActions = map[string]bool{actionUpdate: true, actionDelete: true, actionShare: true}

// wantsBulk reports whether a manage_asset call acts on several assets, or
// changes one asset's tags by adding or removing rather than replacing them.
func wantsBulk(input manageAssetInput) bool {
	if len(input.AssetIDs) > 0 {
		return true
	}
	return input.Action == actionUpdate && input.AssetID != "" && (len(input.AddTags) > 0 || len(input.RemoveTags) > 0)
}

// bulkResult is what a bulk manage_asset call did.
type bulkResult struct {
	Action    string              `json:"action"`
	Requested int                 `json:"requested"`
	Applied   []string            `json:"applied"`
	Skipped   []assetbulk.Skipped `json:"skipped"`
	Message   string              `json:"message"`
}

// handleBulk applies one action to every asset asset_ids names.
func (t *Toolkit) handleBulk(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	ids, refusal := bulkTargets(input)
	if refusal != "" || ids == nil {
		return toolkit.ErrorResult(refusal), nil, nil
	}
	out := bulkResult{Action: input.Action, Requested: len(ids), Applied: []string{}, Skipped: []assetbulk.Skipped{}}
	for _, id := range ids {
		if reason := t.applyOne(ctx, input, id); reason != "" {
			out.Skipped = append(out.Skipped, assetbulk.Skipped{AssetID: id, Reason: reason})
			continue
		}
		out.Applied = append(out.Applied, id)
	}
	out.Message = fmt.Sprintf("Applied %s to %d of %d assets.", input.Action, len(out.Applied), out.Requested)
	if len(out.Skipped) > 0 {
		out.Message += fmt.Sprintf(" %d skipped; each skipped asset says why.", len(out.Skipped))
	}
	return toolkit.JSONResultTyped(out)
}

// bulkTargets checks a bulk call and returns the assets it names, or the
// refusal.
func bulkTargets(input manageAssetInput) (ids []string, refusal string) {
	ids = assetbulk.Distinct(input.AssetIDs)
	if len(ids) == 0 {
		ids = []string{input.AssetID}
	} else if strings.TrimSpace(input.AssetID) != "" {
		return nil, "name the assets with asset_ids or one asset with asset_id, not both"
	}
	switch {
	case !bulkActions[input.Action]:
		return nil, fmt.Sprintf("asset_ids applies to update, delete and share; %q acts on one asset_id", input.Action)
	case len(ids) > assetbulk.MaxAssets:
		return nil, fmt.Sprintf("at most %d assets per call; this one names %d", assetbulk.MaxAssets, len(ids))
	}
	return ids, bulkArgsRefusal(input)
}

// bulkArgsRefusal refuses the arguments a bulk call cannot carry.
func bulkArgsRefusal(input manageAssetInput) string {
	changesTags := len(input.AddTags) > 0 || len(input.RemoveTags) > 0
	switch {
	case input.Content != "" || len(input.Edits) > 0 || input.contentStructured:
		return "content is changed one asset at a time: a call with asset_ids changes name, description, tags, status, retention, sharing or deletes"
	case len(input.Tags) > 0 && changesTags:
		return "pass tags to replace an asset's tags, or add_tags and remove_tags to change them, not both"
	}
	return ""
}

// applyOne acts on one asset, returning "" when it was applied and why it was
// skipped otherwise.
func (t *Toolkit) applyOne(ctx context.Context, input manageAssetInput, id string) string {
	asset, refused := t.loadReadableAsset(ctx, id)
	if refused != nil {
		return assetbulk.ReasonHidden
	}
	one := input
	one.AssetID, one.AssetIDs, one.AddTags, one.RemoveTags = id, nil, nil, nil
	if len(input.AddTags) > 0 || len(input.RemoveTags) > 0 {
		one.Tags = assetbulk.Retagged(assetbulk.Retagged(asset.Tags, input.RemoveTags, false), input.AddTags, true)
		if one.Tags == nil {
			one.Tags = []string{}
		}
	}
	result, _, err := t.actions[input.Action](ctx, one)
	switch {
	case err != nil:
		return err.Error()
	case result != nil && result.IsError:
		return skipReason(result)
	}
	return ""
}

// skipReason is the sentence a refused single-asset call answered with.
func skipReason(result *mcp.CallToolResult) string {
	for _, c := range result.Content {
		if text, ok := c.(*mcp.TextContent); ok && strings.TrimSpace(text.Text) != "" {
			return strings.TrimSpace(text.Text)
		}
	}
	return "refused"
}
