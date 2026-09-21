package notifylayer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// publishBodyBytes bounds how much of an asset's content becomes the body of
// a published document.
//
// It is far below the queue's own document cap. What is being built is a
// message somebody reads in a chat client, and an asset is not written to that
// size: past this much, the excerpt plus the link is a better message than the
// whole file would be, so the cut is made here rather than by each kind's
// transport.
const publishBodyBytes = 8000

// handlePublish posts a portal asset to a channel.
func (h *Handle) handlePublish(ctx context.Context, input notifyInput) (*mcp.CallToolResult, any, error) {
	if h.cfg.Assets == nil || h.cfg.Access == nil {
		return toolkit.ErrorResult("notify: publishing an asset is unavailable in this deployment; " +
			"use action=send with a link instead"), nil, nil
	}
	c := h.resolveCaller(ctx)
	ch, err := h.resolveChannel(ctx, input.Channel, c)
	if err != nil {
		return toolkit.ErrorResult("notify: " + err.Error()), nil, nil
	}
	asset, err := h.readableAsset(ctx, input.Asset, c)
	if err != nil {
		return toolkit.ErrorResult("notify: " + err.Error()), nil, nil
	}
	doc, err := h.documentFromAsset(ctx, asset, input.Message)
	if err != nil {
		return toolkit.ErrorResult("notify: " + err.Error()), nil, nil
	}
	return h.enqueue(ctx, *ch, c, doc)
}

// readableAsset resolves the asset a publish names and refuses one the caller
// cannot read.
//
// An asset the caller cannot read is reported as not found, for the reason a
// channel they cannot reach is: distinguishing "exists but is not yours" from
// "does not exist" turns an id field into a way to enumerate other people's
// work.
func (h *Handle) readableAsset(ctx context.Context, id string, c caller) (*portaldomain.Asset, error) {
	id = assetID(id)
	if id == "" {
		return nil, errors.New("asset is required for action=publish")
	}
	asset, err := h.cfg.Assets.Get(ctx, id)
	if err != nil || asset == nil {
		return nil, fmt.Errorf("no asset %q is available to this session", id)
	}
	if !h.cfg.Access.CanRead(ctx, asset, c.userID, c.email) {
		return nil, fmt.Errorf("no asset %q is available to this session", id)
	}
	return asset, nil
}

// assetID accepts the id on its own or the portal URL of the asset's page, so
// a person who pasted the link they were looking at does not have to find the
// id inside it.
func assetID(in string) string {
	in = strings.TrimSpace(in)
	if in == "" || !strings.Contains(in, "/") {
		return in
	}
	// Everything after the last separator, with any query or fragment the
	// browser's address bar carried removed.
	id := in[strings.LastIndex(in, "/")+1:]
	if at := strings.IndexAny(id, "?#"); at >= 0 {
		id = id[:at]
	}
	return id
}

// documentFromAsset turns an asset into the document a channel carries: the
// asset's name as the title, an excerpt of its content as the body, and the
// portal link.
//
// Content types a chat client cannot show as text -- a rendered HTML page, a
// JSX component, an image -- contribute no body. The message is then the
// asset's name, the sender's message and the link, which is what a reader
// would act on anyway: the page itself is the thing to open.
func (h *Handle) documentFromAsset(ctx context.Context, asset *portaldomain.Asset, message string) (notification.Document, error) {
	doc := notification.Document{
		Title: asset.Name,
		Link:  h.assetLink(asset.ID),
	}
	var parts []string
	if message = strings.TrimSpace(message); message != "" {
		parts = append(parts, message)
	}
	if asset.Description != "" {
		parts = append(parts, asset.Description)
	}
	if readableAsText(asset.ContentType) {
		content, err := h.cfg.Assets.Content(ctx, asset)
		if err != nil {
			return doc, fmt.Errorf("reading the content of asset %q failed", asset.ID)
		}
		if excerpt := excerptOf(string(content)); excerpt != "" {
			parts = append(parts, excerpt)
		}
	}
	doc.Body = strings.Join(parts, "\n\n")
	if err := notification.ValidateDocument(doc); err != nil {
		return doc, err //nolint:wrapcheck // the message is already the caller's
	}
	return doc, nil
}

// assetLink builds the portal URL of an asset's page, or nothing when the
// deployment does not know its own public address.
func (h *Handle) assetLink(id string) string {
	if h.cfg.PortalURL == "" {
		return ""
	}
	return strings.TrimRight(h.cfg.PortalURL, "/") + "/portal/assets/" + id
}

// readableAsText reports whether an asset's content is worth putting in a
// message as it is stored.
func readableAsText(contentType string) bool {
	media, _, _ := strings.Cut(contentType, ";")
	switch strings.ToLower(strings.TrimSpace(media)) {
	case "text/markdown", "text/plain", "text/csv", "text/tab-separated-values",
		"application/json", "application/x-ndjson", "application/sql", "text/x-sql":
		return true
	default:
		return false
	}
}

// excerptOf cuts content to what a message should carry, at a line boundary,
// noting that it was cut. The link is already on the document, so a reader who
// wants the rest has it.
func excerptOf(content string) string {
	content = strings.TrimSpace(content)
	if content == "" || len(content) <= publishBodyBytes {
		return content
	}
	cut := content[:publishBodyBytes]
	if at := strings.LastIndex(cut, "\n"); at > publishBodyBytes/2 {
		cut = cut[:at]
	}
	return cut + "\n\n[truncated; open the asset for the rest]"
}
