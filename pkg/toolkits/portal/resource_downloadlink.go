package portal

import (
	"context"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/resourcedownload"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// resourceActionDownloadLink hands back a short-lived link anybody holding it
// can download a file from (#2847): what a connection that takes a file by URL
// is given, so the file never has to be encoded into a call.
const resourceActionDownloadLink = "download_link"

// DownloadLinks mints download links (resourcedownload.Service).
type DownloadLinks interface {
	Mint(res *resource.Resource, expires string) (resourcedownload.Link, error)
}

// SetDownloadLinks gives manage_resource action=download_link its minter;
// without one the action says what the deployment lacks.
func (t *Toolkit) SetDownloadLinks(d DownloadLinks) {
	t.downloadLinks = d
}

// downloadLinkOutput is a link as the caller is handed it.
type downloadLinkOutput struct {
	resourcedownload.Link
	Reference   string `json:"reference"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	Message     string `json:"message"`
}

// handleDownloadLink mints a link to the file a reference or an address
// names, which the caller must be able to read.
func (t *Toolkit) handleDownloadLink(ctx context.Context, input manageResourceInput) (*mcp.CallToolResult, any, error) {
	if t.downloadLinks == nil {
		return toolkit.ErrorResult(resourcedownload.ErrUnavailable.Error()), nil, nil
	}
	res, refusal := t.readableResource(ctx, input)
	if refusal != "" {
		return toolkit.ErrorResult(refusal), nil, nil
	}
	link, err := t.downloadLinks.Mint(res, strings.TrimSpace(input.Expires))
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	return toolkit.JSONResultTyped(downloadLinkOutput{
		Link: link, Reference: knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetResource, ResourceID: res.ID}.URN(),
		Filename:    res.Filename,
		ContentType: res.MIMEType, SizeBytes: res.SizeBytes,
		Message: downloadMessage(link.Expires),
	})
}

// readableResource is the file input names by reference or by address, as
// the caller may read it, or the refusal.
func (t *Toolkit) readableResource(ctx context.Context, input manageResourceInput) (res *resource.Resource, refusal string) {
	claims := refClaims(ctx)
	if ref := strings.TrimSpace(input.Reference); ref != "" {
		id, err := parseResourceReference(ref)
		if err != nil {
			return nil, err.Error()
		}
		res, err := t.resourceWriter.Get(ctx, id, claims)
		if errors.Is(err, resource.ErrNoSuchResource) {
			return nil, "Nothing you can read is filed under " + ref + "."
		}
		if err != nil {
			return nil, err.Error()
		}
		return res, ""
	}
	if err := requireAddress(input); err != nil {
		return nil, err.Error()
	}
	res, uri, err := t.resourceWriter.Locate(ctx, addressOf(input), claims)
	if err != nil {
		return nil, err.Error()
	}
	if res == nil {
		return nil, "Nothing you can read is filed at " + uri + "."
	}
	return res, ""
}

// downloadMessage says how long the link works and for whom.
func downloadMessage(expires string) string {
	until := "until its first full download or expires_at, whichever comes first (a HEAD request or a byte-range " +
		"read does not count as the download)"
	if expires == resourcedownload.SixHours {
		until = "until expires_at"
	}
	return "Anybody holding url can download this file, with no sign-in, " + until + ". Pass it to the connection " +
		"or service that needs the file; it serves the file as it is when downloaded."
}
