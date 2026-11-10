package portal

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// SiteContentInput is a manage_asset call aimed at a site's files
// (mcp:site:<site>/<environment>[/<path>]) rather than at an asset (#2588).
type SiteContentInput struct {
	Action, Target                   string
	Find, Pattern, Section, Selector string
	Occurrence, ChangeSummary        string
	LineStart, LineEnd, ContextBytes int
	Limit, Offset, BaseVersion       int
	FromVersion, ToVersion           int
	DryRun                           bool
	Edits                            []textpatch.Edit
}

// SiteContent carries out the content actions on a site's files. They follow
// the site's rules, not an asset's, so the sites layer owns them.
type SiteContent interface {
	Handle(ctx context.Context, in SiteContentInput) (*mcp.CallToolResult, any, error)
}

// SetSiteContent binds what answers a site reference; nil is a deployment with
// no sites.
func (t *Toolkit) SetSiteContent(c SiteContent) { t.siteContent = c }

// handleSiteTarget answers a manage_asset call naming a site reference.
func (t *Toolkit) handleSiteTarget(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	if t.siteContent == nil {
		return middleware.UnavailableResult("sites are not available on this deployment",
			"manage_site reports whether this deployment serves sites."), nil, nil
	}
	if len(input.References) > 0 || len(input.Sources) > 0 {
		return toolkit.ErrorResult("references and sources apply to assets, not site files: a site loads its own files by URL"), nil, nil
	}
	return t.siteContent.Handle(ctx, SiteContentInput{ //nolint:wrapcheck // the sites layer writes the result
		Action: input.Action, Target: input.AssetID,
		Find: input.Find, Pattern: input.Pattern, Section: input.Section, Selector: input.Selector,
		Occurrence: input.Occurrence, ChangeSummary: input.ChangeSummary,
		LineStart: input.LineStart, LineEnd: input.LineEnd, ContextBytes: input.ContextBytes,
		Limit: input.Limit, Offset: input.Offset, BaseVersion: input.BaseVersion,
		FromVersion: input.FromVersion, ToVersion: input.ToVersion, DryRun: input.DryRun, Edits: input.Edits,
	})
}

// namesSite reports whether an asset_id argument is a site reference.
func namesSite(assetID string) bool { return site.IsRef(assetID) }
