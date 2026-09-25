package portalstore

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/platform/resourceholds"
	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/internal/unarchive"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	portalkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

// ResourceWriteDeps is what the portal layer's resource writes are assembled
// from once the managed-resource layer exists: its store and blob client, the
// resources.managed settings they honor, the platform's MCP re-registration
// callbacks, and the prompt attachments a delete counts.
type ResourceWriteDeps struct {
	Store          resource.Store
	Blobs          resource.S3Client
	Bucket         string
	URIScheme      string
	MaxVersions    int
	MaxUploadBytes int64
	Extract        unarchive.Limits
	// Registered re-announces a written resource so resources/list_changed
	// reaches a client that already listed it; Unregistered takes a deleted
	// one out of that list.
	Registered   func(*resource.Resource)
	Unregistered func(uri string)
	Attachments  resourceholds.Attachments
}

// BindResourceWrites builds and binds everything that writes a managed resource
// on the portal layer's behalf: the writer behind manage_resource (#1487), the
// lander every export's resource destination uses (#1663), the extractor
// behind manage_resource extract (#1879), and the holds a delete is refused by
// (#1665).
//
// A deployment with no blob client gets no writer and binds nothing, and
// manage_resource says so. A blob client that cannot read by range gets no
// extractor, and extract says so.
func (h *Handle) BindResourceWrites(d ResourceWriteDeps) {
	w := resourcewrite.New(resourcewrite.Deps{
		Store: d.Store, Blobs: d.Blobs, Bucket: d.Bucket, URIScheme: d.URIScheme, MaxVersions: d.MaxVersions,
		Registered: d.Registered, Unregistered: d.Unregistered,
		// The same record the asset write funnels fill, so an agent's resource
		// write and its asset write name the same producer (#1569).
		Producers: h.Producers(),
	})
	if w == nil {
		return
	}
	h.BindResourceWriter(w)
	lander := resourcewrite.NewLander(resourcewrite.LanderDeps{Writer: w, MaxUploadBytes: d.MaxUploadBytes})
	// The follower reaches the registrar the composition root binds onto the
	// asset toolkit later.
	lander.SetTableFollower(h.FollowResourceTables)
	h.BindResourceLander(lander)
	ranges, _ := d.Blobs.(resourcewrite.RangeReader)
	if x := resourcewrite.NewExtractor(resourcewrite.ExtractorDeps{
		Lander: lander, Ranges: ranges, Limits: d.Extract,
	}); x != nil && h != nil && h.toolkit != nil {
		h.toolkit.SetResourceExtractor(extractorAdapter{x: x})
	}
	h.BindResourceHolds(d.Attachments)
}

// extractorAdapter carries an extraction across between the asset toolkit's
// types and the writer's. The two are the same fields, kept apart so the
// toolkit depends on no writer and the writer on no toolkit, the way
// holdReader keeps the delete's holds apart.
type extractorAdapter struct {
	x interface {
		ExtractArchive(ctx context.Context, req resourcewrite.Extraction, claims resource.Claims) (*resourcewrite.Extracted, error)
	}
}

// ExtractArchive runs the extraction and reports what it wrote, including on a
// failure part way through.
func (a extractorAdapter) ExtractArchive(
	ctx context.Context, req portalkit.ArchiveExtraction, claims resource.Claims,
) (*portalkit.ExtractedArchive, error) {
	out, err := a.x.ExtractArchive(ctx, resourcewrite.Extraction{
		ArchiveID: req.ArchiveID, Scope: req.Scope, ScopeID: req.ScopeID, Path: req.Path,
		Members: req.Members, Filename: req.Filename, Replace: req.Replace,
		Description: req.Description, Tags: req.Tags, ChangeSummary: req.ChangeSummary,
	}, claims)
	if out == nil {
		return nil, err //nolint:wrapcheck // the writer's sentence names the archive and what stopped it
	}
	result := &portalkit.ExtractedArchive{
		Format: out.Format, Members: make([]portalkit.ExtractedMember, 0, len(out.Members)), Skipped: out.Skipped,
	}
	for _, m := range out.Members {
		result.Members = append(result.Members, portalkit.ExtractedMember{Member: m.Member, ResourceLanding: m.Landing})
	}
	return result, err //nolint:wrapcheck // as above
}
