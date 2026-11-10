package sitetool

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
	"github.com/txn2/mcp-data-platform/pkg/textpatch/patchmcp"
	portalkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

// The manage_asset actions a site reference answers.
const (
	actionGetContent = "get_content"
	actionOutline    = "outline"
	actionLocate     = "locate"
	actionStats      = "stats"
	actionDiff       = "diff"
	actionPatch      = "patch"
)

// The fields a content answer carries beside what describes the document.
const (
	fieldRef     = "asset_id"
	fieldVersion = "version"
	fieldType    = "content_type"
	fieldEnv     = "environment"
	fieldFiles   = "files"
	fieldTotal   = "total"
)

var contentActions = []string{actionGetContent, actionOutline, actionLocate, actionStats, actionDiff, actionPatch}

// Content answers manage_asset's content actions for a site reference (#2588),
// through the same service manage_site uses.
type Content struct {
	Service *sitesvc.Service
	// ToolAllowed reports whether a persona holds manage_site: a change to a
	// site through manage_asset must not reach further than manage_site does.
	ToolAllowed func(persona string) bool
}

// contentCall is one site content request.
type contentCall struct {
	in  portalkit.SiteContentInput
	ref site.Ref
}

// Handle runs one content action.
func (c *Content) Handle(ctx context.Context, in portalkit.SiteContentInput) (*mcp.CallToolResult, any, error) {
	if !c.Service.Config.IsEnabled() {
		return errorResult("sites are turned off on this deployment (sites.enabled is false)")
	}
	if !slices.Contains(contentActions, in.Action) {
		return errorResult(fmt.Sprintf("manage_asset %s does not apply to a site file. A site reference takes %v; "+
			"sharing and deleting a site is manage_site's.", in.Action, contentActions))
	}
	ref, err := site.ParseRef(in.Target)
	if err != nil {
		return errorResult(err.Error())
	}
	if in.Action == actionPatch && !c.mayWrite(ctx) {
		return middleware.UnauthorizedResult("your persona does not hold manage_site, so it cannot change a site",
			"Ask an administrator to add manage_site to your persona."), nil, nil
	}
	fields, err := c.run(ctx, actorOf(ctx), contentCall{in: in, ref: ref})
	if err != nil {
		return fail(err), nil, nil
	}
	fields[fieldRef] = in.Target
	return jsonResult(fields)
}

func (c *Content) mayWrite(ctx context.Context) bool {
	pc := middleware.GetPlatformContext(ctx)
	return pc != nil && c.ToolAllowed != nil && c.ToolAllowed(pc.PersonaName)
}

func (c *Content) run(ctx context.Context, actor sitesvc.Actor, q contentCall) (map[string]any, error) {
	switch q.in.Action {
	case actionPatch:
		return c.patch(ctx, actor, q)
	case actionDiff:
		return c.diff(ctx, actor, q)
	}
	if !q.ref.IsFile() {
		return c.onTree(ctx, actor, q)
	}
	return c.onFile(ctx, actor, q)
}

// onFile answers a read of one file, with the fields an asset answers with.
func (c *Content) onFile(ctx context.Context, actor sitesvc.Actor, q contentCall) (map[string]any, error) {
	file, err := c.Service.ReadFile(ctx, actor, q.ref)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by fail
	}
	syntax := textpatch.SyntaxForContentType(file.ContentType)
	var fields map[string]any
	switch q.in.Action {
	case actionOutline:
		fields = textpatch.OutlineFields(file.Body, syntax)
	case actionStats:
		fields = textpatch.StatsFields(file.Body)
	case actionLocate:
		fields, err = textpatch.LocateFields(file.Body, locateQuery(q.in), textpatch.Options{Syntax: syntax})
	default:
		fields, err = textpatch.ContentFields(file.Body, textpatch.ContentRequest{
			Syntax: syntax, Section: q.in.Section, Selector: q.in.Selector, Occurrence: q.in.Occurrence, LineStart: q.in.LineStart, LineEnd: q.in.LineEnd,
		})
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // read by fail
	}
	fields[fieldVersion], fields[fieldType], fields[fieldEnv] = file.Release, file.ContentType, file.Environment
	return fields, nil
}

func locateQuery(in portalkit.SiteContentInput) textpatch.LocateQuery {
	return textpatch.LocateQuery{
		Find: in.Find, Pattern: in.Pattern, Section: in.Section, Selector: in.Selector, Occurrence: in.Occurrence,
		ContextBytes: in.ContextBytes, Limit: in.Limit,
	}
}

// onTree answers a read of a whole tree or folder.
func (c *Content) onTree(ctx context.Context, actor sitesvc.Actor, q contentCall) (map[string]any, error) {
	switch q.in.Action {
	case actionLocate:
		found, err := c.Service.LocateTree(ctx, actor, q.ref, locateQuery(q.in))
		if err != nil {
			return nil, err //nolint:wrapcheck // read by fail
		}
		return map[string]any{
			fieldVersion: found.Release, fieldEnv: found.Environment, fieldFiles: found.Files, textpatch.FieldCount: found.Count,
			"files_scanned": found.FilesScanned, "files_skipped": found.FilesSkipped, textpatch.FieldTruncated: found.Truncated,
		}, nil
	case actionOutline:
		list, err := c.Service.Tree(ctx, actor, q.ref, q.in.Offset, q.in.Limit)
		if err != nil {
			return nil, err //nolint:wrapcheck // read by fail
		}
		return map[string]any{fieldVersion: list.Release, fieldEnv: list.Environment, fieldFiles: list.Files, fieldTotal: list.Total, textpatch.FieldTruncated: list.Truncated}, nil
	case actionStats:
		list, err := c.Service.Tree(ctx, actor, q.ref, 0, 1)
		if err != nil {
			return nil, err //nolint:wrapcheck // read by fail
		}
		return map[string]any{fieldVersion: list.Release, fieldEnv: list.Environment, fieldTotal: list.Total}, nil
	}
	return nil, &sitesvc.RefusedError{Msg: "get_content reads one file: use outline to list them, then name one with mcp:site:<site>/<environment>/<path>"}
}

func (c *Content) patch(ctx context.Context, actor sitesvc.Actor, q contentCall) (map[string]any, error) {
	summary := q.in.ChangeSummary
	if summary == "" {
		summary = fmt.Sprintf("%d edits via patch", len(q.in.Edits))
		if len(q.in.Edits) == 1 {
			summary = "1 edit via patch"
		}
	}
	done, err := c.Service.PatchFile(ctx, actor, sitesvc.PatchRequest{
		Ref: q.ref, Base: q.in.BaseVersion, Edits: q.in.Edits, Summary: summary, DryRun: q.in.DryRun,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // read by fail
	}
	fields := textpatch.PatchFields(done.Result)
	maps.Copy(fields, map[string]any{fieldVersion: done.Release, fieldType: done.ContentType, "change_summary": summary, "address": done.Address})
	switch {
	case q.in.DryRun:
		fields["dry_run"], fields["message"] = true, "Dry run: no release was created."
	case done.Unchanged:
		fields["unchanged"], fields["message"] = true, "The edits left the file as it was, so no release was created."
	default:
		fields["message"] = fmt.Sprintf("Patched %s; new release %d.", done.Path, done.Release)
	}
	return fields, nil
}

func (c *Content) diff(ctx context.Context, actor sitesvc.Actor, q contentCall) (map[string]any, error) {
	got, err := c.Service.Diff(ctx, actor, q.ref, q.in.FromVersion, q.in.ToVersion)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by fail
	}
	fields := map[string]any{"from_version": got.From, "to_version": got.To}
	if got.Comparison != nil {
		maps.Copy(fields, map[string]any{
			"changed": got.Comparison.Changed, "added": got.Comparison.Added, "removed": got.Comparison.Removed,
			"redirects_added": got.Comparison.RedirectsAdded, "redirects_removed": got.Comparison.RedirectsRemoved,
		})
		return fields, nil
	}
	fields[textpatch.FieldDiff] = got.Diff
	return fields, nil
}

// fail is the result for an error: an edit's own code where it has one, so an
// agent can branch on it as it does on an asset.
func fail(err error) *mcp.CallToolResult {
	var editErr *textpatch.Error
	switch {
	case errors.As(err, &editErr):
		return patchmcp.ErrorResult(err)
	case errors.Is(err, site.ErrNotAllowed):
		return middleware.UnauthorizedResult(err.Error(), "Ask the site's owner for access.")
	case errors.Is(err, site.ErrStale):
		return patchmcp.ErrorResult(textpatch.ConcurrentWriteError())
	}
	res, _, _ := errorResult(message(err))
	return res
}
