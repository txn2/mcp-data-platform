package notifylayer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// ResourceReader is the managed-resource read an attachment needs: the record,
// which carries the scope the caller's entitlement is checked against.
type ResourceReader interface {
	Get(ctx context.Context, id string) (*resource.Resource, error)
}

// resourceURIPrefix is the form a managed resource's reference takes in
// prose and in a knowledge page, accepted beside the bare id.
const resourceURIPrefix = "mcp:resource:"

// attachmentInput is one entry of the attachments parameter in its object
// form. The string form is the same reference with nothing else said.
type attachmentInput struct {
	AssetID    string `json:"asset_id,omitempty"`
	ResourceID string `json:"resource_id,omitempty"`
	Name       string `json:"name,omitempty"`
}

// parseAttachments reads the attachments parameter in either of its JSON
// forms -- a list of strings, a list of objects, or a mix of the two -- into
// unresolved references.
//
// A string is an asset id, an asset's portal URL, a resource id, or a
// resource's mcp:resource: reference. Which of asset and resource a bare id
// names is not knowable from its shape, so it is carried as both candidates
// and resolveAttachment settles it against the stores.
func parseAttachments(raw []any) ([]attachmentInput, error) {
	out := make([]attachmentInput, 0, len(raw))
	for i, entry := range raw {
		switch v := entry.(type) {
		case string:
			ref := strings.TrimSpace(v)
			if ref == "" {
				return nil, fmt.Errorf("attachment %d is empty", i+1)
			}
			if id, ok := strings.CutPrefix(ref, resourceURIPrefix); ok {
				out = append(out, attachmentInput{ResourceID: id})
				continue
			}
			id := assetID(ref)
			out = append(out, attachmentInput{AssetID: id, ResourceID: id})
		case map[string]any:
			in := attachmentInput{
				AssetID:    assetID(stringField(v, "asset_id")),
				ResourceID: strings.TrimPrefix(stringField(v, "resource_id"), resourceURIPrefix),
				Name:       stringField(v, "name"),
			}
			if (in.AssetID == "") == (in.ResourceID == "") {
				return nil, fmt.Errorf("attachment %d must name exactly one of asset_id and resource_id", i+1)
			}
			out = append(out, in)
		default:
			return nil, fmt.Errorf("attachment %d must be a string or an object with asset_id or resource_id", i+1)
		}
	}
	if len(out) > notification.MaxAttachments {
		return nil, fmt.Errorf("a notification carries at most %d attachments, not %d", notification.MaxAttachments, len(out))
	}
	return out, nil
}

// stringField reads a string member of a decoded JSON object, trimmed.
func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

// resolveAttachments turns the caller's references into the attachments a
// queue row records, refusing any the caller cannot read.
//
// The entitlement is checked here, at enqueue, against the caller making the
// send, and the version is pinned on the row. The worker that later reads the
// file has no caller to check against, so this is the one place "may this
// person attach this" can be asked -- and an id the caller cannot read is
// reported as not found, for the reason a channel they cannot reach is.
func (h *Handle) resolveAttachments(ctx context.Context, in []attachmentInput, c caller) ([]notification.Attachment, error) {
	out := make([]notification.Attachment, 0, len(in))
	for _, ref := range in {
		att, err := h.resolveAttachment(ctx, ref, c)
		if err != nil {
			return nil, err
		}
		out = append(out, att)
	}
	return out, nil
}

// resolveAttachment settles one reference against the asset store, then the
// resource store.
func (h *Handle) resolveAttachment(ctx context.Context, ref attachmentInput, c caller) (notification.Attachment, error) {
	if ref.AssetID != "" {
		if asset, ok := h.attachableAsset(ctx, ref.AssetID, c); ok {
			return notification.Attachment{
				AssetID: asset.ID,
				Version: asset.CurrentVersion,
				Name:    firstNonEmpty(ref.Name, fileNameOf(asset)),
				Link:    h.assetLink(asset.ID),
			}, nil
		}
	}
	if ref.ResourceID != "" {
		if res, ok := h.attachableResource(ctx, ref.ResourceID); ok {
			return notification.Attachment{
				ResourceID: res.ID,
				Name:       firstNonEmpty(ref.Name, res.Filename),
				Link:       h.resourceLink(res.ID),
			}, nil
		}
	}
	return notification.Attachment{}, fmt.Errorf("no asset or resource %q is available to this session to attach",
		firstNonEmpty(ref.AssetID, ref.ResourceID))
}

// attachableAsset returns the asset when this deployment can read assets and
// the caller may read this one.
func (h *Handle) attachableAsset(ctx context.Context, id string, c caller) (*portaldomain.Asset, bool) {
	if h.cfg.Assets == nil || h.cfg.Access == nil {
		return nil, false
	}
	asset, err := h.cfg.Assets.Get(ctx, id)
	if err != nil || asset == nil || asset.DeletedAt != nil {
		return nil, false
	}
	return asset, h.cfg.Access.CanRead(ctx, asset, c.userID, c.email)
}

// attachableResource returns the resource when this deployment can read
// resources and the caller may read this one, under the same claims the
// resources middleware derives from the call.
func (h *Handle) attachableResource(ctx context.Context, id string) (*resource.Resource, bool) {
	if h.cfg.Resources == nil {
		return nil, false
	}
	res, err := h.cfg.Resources.Get(ctx, id)
	if err != nil || res == nil {
		return nil, false
	}
	return res, resource.CanAccessResource(callerClaims(ctx), res)
}

// callerClaims builds the resource-permission claims of the call. A call
// with no platform context resolves as nobody, which reaches only global
// resources.
func callerClaims(ctx context.Context) resource.Claims {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return resource.Claims{}
	}
	return resource.BuildClaims(pc.UserID, pc.UserEmail, pc.PersonaName, pc.Roles, pc.IsAdmin).
		ActingFor(pc.OnBehalfOfEmail, pc.OnBehalfOfSub)
}

// resourceLink builds the portal URL of a resource's page.
func (h *Handle) resourceLink(id string) string {
	return notification.PortalLink(h.cfg.PortalURL, "/resources/"+id)
}

// fileNameOf names an asset's file for a reader: its name, with the
// extension its content type implies when the name has none.
func fileNameOf(asset *portaldomain.Asset) string {
	name := strings.TrimSpace(asset.Name)
	if name == "" {
		name = asset.ID
	}
	if strings.Contains(name, ".") {
		return name
	}
	if ext := extensionFor(asset.ContentType); ext != "" {
		return name + ext
	}
	return name
}

// extensions are the file extensions a reader's client opens the content
// types an asset is commonly saved as by.
var extensions = map[string]string{
	"text/markdown":    ".md",
	"text/csv":         ".csv",
	"text/html":        ".html",
	"application/json": ".json",
	"image/png":        ".png",
	"image/jpeg":       ".jpg",
	"image/svg+xml":    ".svg",
	"application/pdf":  ".pdf",
	"text/plain":       ".txt",
}

// extensionFor is the file extension for a content type, or empty.
func extensionFor(contentType string) string {
	media, _, _ := strings.Cut(contentType, ";")
	return extensions[strings.ToLower(strings.TrimSpace(media))]
}

// firstNonEmpty returns the first argument that is not blank.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// errNoAttachments is what a send with an attachments parameter answers when
// the deployment can read neither assets nor resources.
var errNoAttachments = errors.New("attachments are unavailable in this deployment; link to the file instead")

// attach resolves the attachments parameter onto doc. An absent or empty
// parameter attaches nothing.
func (h *Handle) attach(ctx context.Context, doc *notification.Document, raw []any, c caller) error {
	if len(raw) == 0 {
		return nil
	}
	if (h.cfg.Assets == nil || h.cfg.Access == nil) && h.cfg.Resources == nil {
		return errNoAttachments
	}
	refs, err := parseAttachments(raw)
	if err != nil {
		return err
	}
	atts, err := h.resolveAttachments(ctx, refs, c)
	if err != nil {
		return err
	}
	doc.Attachments = atts
	return nil
}
