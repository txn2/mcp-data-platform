package portal

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
	"github.com/txn2/mcp-data-platform/pkg/textpatch/patchmcp"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// JSON result keys the asset verbs add on top of the shared body fields.
const (
	fieldVersion     = "version"
	fieldContentType = "content_type"
)

// assetIdentity is the asset half of every content-verb response: which asset,
// at which version, in what type. The body half comes from pkg/textpatch, so
// the two tools answer the same shape.
func assetIdentity(asset *portal.Asset) map[string]any {
	return map[string]any{
		fieldAssetID:     asset.ID,
		fieldVersion:     asset.CurrentVersion,
		fieldContentType: asset.ContentType,
	}
}

// contentVerb runs a read-only content verb: load the asset's text, build the
// body fields, and stamp the asset's identity onto them.
func (t *Toolkit) contentVerb(
	ctx context.Context,
	assetID string,
	build func(body string) (map[string]any, error),
) (*mcp.CallToolResult, any, error) {
	asset, body, errResult := t.readAssetText(ctx, assetID)
	if errResult != nil {
		return errResult, nil, nil
	}
	fields, err := build(body)
	if err != nil {
		return patchmcp.ErrorResult(err), nil, nil
	}
	maps.Copy(fields, assetIdentity(asset))
	return toolkit.JSONResultTyped(fields)
}

// handleOutline returns the asset's heading tree: level, line, and byte size
// per section. It is the cheapest way to decide where to patch a long document
// without reading any of it.
func (t *Toolkit) handleOutline(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	return t.contentVerb(ctx, input.AssetID, func(body string) (map[string]any, error) {
		return textpatch.OutlineFields(body), nil
	})
}

// handleStats returns the asset's size, line count, current version, content
// type, and body hash, with none of the body.
func (t *Toolkit) handleStats(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	return t.contentVerb(ctx, input.AssetID, func(body string) (map[string]any, error) {
		return textpatch.StatsFields(body), nil
	})
}

// handleGetContent reads a span of the asset: the whole body, one section, or a
// line range, always with the document's size, line count, version, and type.
func (t *Toolkit) handleGetContent(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	return t.contentVerb(ctx, input.AssetID, func(body string) (map[string]any, error) {
		return textpatch.ContentFields(body, textpatch.ContentRequest{
			Section:   input.Section,
			LineStart: input.LineStart,
			LineEnd:   input.LineEnd,
		})
	})
}

// handleLocate reports every match of a literal or regex anchor: line number,
// byte offset, enclosing section, and a context window wide enough to copy
// verbatim into a patch anchor.
func (t *Toolkit) handleLocate(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	return t.contentVerb(ctx, input.AssetID, func(body string) (map[string]any, error) {
		return textpatch.LocateFields(body, textpatch.LocateQuery{
			Find:         input.Find,
			Pattern:      input.Pattern,
			Section:      input.Section,
			ContextBytes: input.ContextBytes,
			Limit:        input.Limit,
		}, t.patchOptions())
	})
}

// handlePatch applies an ordered list of anchored edits and writes the result
// as an ordinary new version, so revert and version history keep working.
//
// Nothing is written unless every edit resolves, and the response never echoes
// the new body: the point of patching is that the document crosses the wire in
// neither direction.
func (t *Toolkit) handlePatch(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	asset, body, errResult := t.readAssetText(ctx, input.AssetID)
	if errResult != nil {
		return errResult, nil, nil
	}
	if !t.isAdmin(ctx) && asset.OwnerID != resolveOwnerID(ctx) {
		return middleware.UnauthorizedResult("you can only patch your own assets",
			"Ask the owner to apply the change, or save your own copy with save_asset."), nil, nil
	}
	if input.BaseVersion > 0 && input.BaseVersion != asset.CurrentVersion {
		return patchmcp.ErrorResult(textpatch.StaleBaseError(input.BaseVersion, asset.CurrentVersion)), nil, nil
	}

	res, err := textpatch.Apply(body, input.Edits, t.patchOptions())
	if err != nil {
		return patchmcp.ErrorResult(err), nil, nil
	}

	summary := patchChangeSummary(input.ChangeSummary, len(input.Edits))
	result := patchResponse(asset, res, summary)
	if input.DryRun {
		result["dry_run"] = true
		result[fieldVersion] = asset.CurrentVersion
		result[fieldMessage] = "Dry run: no version was created."
		return toolkit.JSONResultTyped(result)
	}

	version, err := t.uploadContentUpdate(ctx, asset, res.Body, asset.ContentType, summary)
	if err != nil {
		return toolkit.ErrorResult("failed to write patched content: " + err.Error()), nil, nil
	}
	result[fieldVersion] = version
	result[fieldMessage] = fmt.Sprintf("Patched asset; new version %d.", version)
	return toolkit.JSONResultTyped(result)
}

// patchResponse builds the per-edit report and diff shared by a dry run and a
// real patch, so a dry run reports exactly what the write would.
func patchResponse(asset *portal.Asset, res textpatch.Result, summary string) map[string]any {
	fields := textpatch.PatchFields(res)
	fields[fieldAssetID] = asset.ID
	fields[fieldContentType] = asset.ContentType
	fields["change_summary"] = summary
	return fields
}

// patchChangeSummary returns the caller's summary, or a generated one, so
// version history reads as a list of changes rather than a repeated constant.
func patchChangeSummary(supplied string, edits int) string {
	if supplied != "" {
		return supplied
	}
	if edits == 1 {
		return "1 edit via patch"
	}
	return fmt.Sprintf("%d edits via patch", edits)
}

// handleDiff compares two stored versions of an asset and returns a unified
// diff. from_version defaults to the version before to_version, and to_version
// defaults to the current version.
func (t *Toolkit) handleDiff(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	if input.AssetID == "" {
		return middleware.MissingParameterResult(fieldAssetID), nil, nil
	}
	asset, errResult := t.loadReadableAsset(ctx, input.AssetID)
	if errResult != nil {
		return errResult, nil, nil
	}

	to := input.ToVersion
	if to <= 0 {
		to = asset.CurrentVersion
	}
	from := input.FromVersion
	if from <= 0 {
		from = to - 1
	}
	if from < 1 {
		return middleware.BuildErrorResult(middleware.ClientInputError(middleware.CodeMissingParameter,
			"there is no earlier version to compare against",
			"This asset has only one version. Call action=list_versions once it has more.")), nil, nil
	}

	// The two versions are independent reads, each a store lookup plus an
	// object fetch, so they run concurrently rather than one round trip
	// after the other.
	var oldBody, newBody string
	var oldErr, newErr *mcp.CallToolResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		oldBody, oldErr = t.readVersionText(ctx, asset, from)
	}()
	go func() {
		defer wg.Done()
		newBody, newErr = t.readVersionText(ctx, asset, to)
	}()
	wg.Wait()

	// Report the older side's failure first so the message names the version
	// the caller is most likely to have gotten wrong.
	if oldErr != nil {
		return oldErr, nil, nil
	}
	if newErr != nil {
		return newErr, nil, nil
	}

	return toolkit.JSONResultTyped(map[string]any{
		fieldAssetID:   asset.ID,
		"from_version": from,
		"to_version":   to,
		textpatch.FieldDiff: textpatch.UnifiedDiffLabeled(
			oldBody, newBody, fmt.Sprintf("v%d", from), fmt.Sprintf("v%d", to), 0),
	})
}

// readVersionText loads one stored version's bytes, refusing a non-textual one.
func (t *Toolkit) readVersionText(ctx context.Context, asset *portal.Asset, version int) (string, *mcp.CallToolResult) {
	ver, err := t.versionStore.GetByVersion(ctx, asset.ID, version)
	if err != nil {
		return "", middleware.NotFoundResult(fmt.Sprintf("version %d not found: %v", version, err),
			"Call manage_asset action=list_versions to see valid version numbers.")
	}
	if !contenttype.IsTextual(ver.ContentType) {
		return "", patchmcp.ErrorResult(textpatch.NotTextError(ver.ContentType))
	}
	if t.s3Client == nil {
		return "", middleware.UnavailableResult("content storage not configured",
			"This deployment has no object storage bound to the portal toolkit.")
	}
	data, _, err := t.s3Client.GetObject(ctx, ver.S3Bucket, ver.S3Key)
	if err != nil {
		return "", toolkit.ErrorResult(fmt.Sprintf("failed to read version %d content: %v", version, err))
	}
	return string(data), nil
}

// patchOptions binds the deployment's content-size ceiling to the edit engine
// so a patch can never grow an asset past what save_asset would accept.
func (t *Toolkit) patchOptions() textpatch.Options {
	return textpatch.Options{MaxResultBytes: t.maxContentSize}
}

// readAssetText loads an asset's current bytes for the text verbs, refusing a
// binary asset rather than corrupting it or dumping it as garbage.
func (t *Toolkit) readAssetText(ctx context.Context, assetID string) (*portal.Asset, string, *mcp.CallToolResult) {
	if assetID == "" {
		return nil, "", middleware.MissingParameterResult(fieldAssetID)
	}
	asset, errResult := t.loadReadableAsset(ctx, assetID)
	if errResult != nil {
		return nil, "", errResult
	}
	if !contenttype.IsTextual(asset.ContentType) {
		return nil, "", patchmcp.ErrorResult(textpatch.NotTextError(asset.ContentType))
	}
	if t.s3Client == nil {
		return nil, "", middleware.UnavailableResult("content storage not configured",
			"This deployment has no object storage bound to the portal toolkit.")
	}
	data, _, err := t.s3Client.GetObject(ctx, asset.S3Bucket, asset.S3Key)
	if err != nil {
		return nil, "", toolkit.ErrorResult("failed to read asset content: " + err.Error())
	}
	return asset, string(data), nil
}

// loadReadableAsset resolves an asset the caller may read: one they own, or one
// shared with them directly or through a collection.
func (t *Toolkit) loadReadableAsset(ctx context.Context, assetID string) (*portal.Asset, *mcp.CallToolResult) {
	asset, err := t.assetStore.Get(ctx, assetID)
	if err != nil {
		return nil, middleware.NotFoundResult("asset not found: "+err.Error(), assetNotFoundHint)
	}
	if asset.DeletedAt != nil {
		return nil, middleware.NotFoundResult("asset has been deleted", assetNotFoundHint)
	}
	if !t.canReadAsset(ctx, asset) {
		return nil, middleware.UnauthorizedResult("you do not have access to this asset",
			"Ask the owner to share it with you, or call manage_asset action=list to see the assets you can read.")
	}
	return asset, nil
}

// canReadAsset reports whether the caller owns the asset or holds any share
// grant on it, directly or through a collection. Read access is deliberately
// wider than the owner-only write checks: a viewer share is enough to read.
// An admin is unrestricted by design and reads any asset.
func (t *Toolkit) canReadAsset(ctx context.Context, asset *portal.Asset) bool {
	if t.isAdmin(ctx) {
		return true
	}
	userID, email := resolveOwnerID(ctx), resolveOwnerEmail(ctx)
	if asset.OwnerID == userID {
		return true
	}
	if t.shareStore == nil {
		return false
	}
	if share, err := t.shareStore.GetActiveShareForTarget(ctx, threadTargetAsset, asset.ID, userID, email); err == nil && share != nil {
		return true
	}
	perm, _ := t.shareStore.GetUserAssetPermissionViaCollection(ctx, asset.ID, userID, email)
	return perm != ""
}
