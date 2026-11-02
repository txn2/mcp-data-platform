package assetrefs

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/blobserve"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Path parameter names on the serving route.
const (
	pathKeyAsset = "id"
	pathKeyRef   = "ref"
)

// Refusal messages. They are deliberately identical for a token that names no
// reference and a token that names another asset: the two are the same answer
// to a caller who does not hold the capability, and distinguishing them would
// let one be probed for the other.
const (
	msgRefNotFound      = "no such reference"
	msgResourceMissing  = "the referenced file is no longer available"
	msgStorageNotReady  = "content storage not configured"
	msgContentUnavail   = "failed to retrieve the referenced file"
	msgMethodNotAllowed = "method not allowed"
)

// Resources is the managed-resource layer as this package reads it: one
// resource by id, for serving a reference, and one by the mcp:// URI an
// author declared, for checking a declaration. resource.Store satisfies it.
//
// The two reads are one interface because they are one dependency. Declaring a
// reference and serving one are the two ends of the same fact -- this asset
// names that file -- and splitting the dependency in two would let the
// declaration path and the serving path drift onto different notions of what a
// managed resource is.
type Resources interface {
	Get(ctx context.Context, id string) (*resource.Resource, error)
	GetByURI(ctx context.Context, uri string) (*resource.Resource, error)
	// GetByIDs reads a whole set at once, keyed by id, with a missing id
	// simply absent. Re-checking a copied asset's references against the
	// person copying it asks about every reference the asset has, and asking
	// one row at a time would put the reference cap's worth of round trips in
	// front of an interactive copy.
	GetByIDs(ctx context.Context, ids []string) (map[string]*resource.Resource, error)
}

// BlobReader reads a stored object. Both the portal's and the resource
// layer's S3 clients satisfy it.
type BlobReader interface {
	GetObject(ctx context.Context, bucket, key string) (body []byte, contentType string, err error)
}

// Deps are the serving route's dependencies. Any of them absent leaves the
// route answering that the target is unavailable rather than panicking, which
// is what a deployment missing that layer should serve.
type Deps struct {
	Refs      Store
	Resources Resources
	Blobs     BlobReader
	// Bucket is the bucket managed-resource blobs live in. It is the resource
	// layer's bucket, not the portal's: a resource reference points at a
	// resource. An asset reference needs no bucket here, because an asset
	// carries the bucket its own content is stored in.
	Bucket string

	// Assets and AssetBlobs serve a reference to another asset (#1488):
	// the asset row for its content type and location, and the portal's own
	// blob client for its bytes.
	Assets     Assets
	AssetBlobs BlobReader
	// PublicBaseURL is the configured origin, used to rewrite the references a
	// referenced asset makes of its own. Empty falls back to the origin of the
	// request being answered, exactly as every other serving surface does.
	PublicBaseURL string
}

// Server answers the reference-serving route.
type Server struct{ deps Deps }

// New builds the serving handler.
func New(deps Deps) *Server { return &Server{deps: deps} }

// Ready reports whether this deployment can serve references at all. A handler
// that is not ready is not registered, so the route 404s as an unknown path
// rather than as a missing file.
//
// One kind is enough. A deployment with no managed-resource layer still serves
// asset references, and the kind it cannot serve answers that its storage is
// not configured rather than taking the other kind down with it.
func (s *Server) Ready() bool {
	return s != nil && s.deps.Refs != nil && (s.resourcesReady() || s.assetsReady())
}

func (s *Server) resourcesReady() bool {
	return s.deps.Resources != nil && s.deps.Blobs != nil
}

func (s *Server) assetsReady() bool {
	return s.deps.Assets != nil && s.deps.AssetBlobs != nil
}

// ServeHTTP answers GET /portal/refs/{id}/{ref} with the referenced target's
// bytes.
//
// It takes no session and reads no identity. Authorization is the token in the
// path: it was minted for one (asset, target) pair, it reaches a reader only
// inside content that reader was already allowed to open, and it resolves to
// nothing on any other asset's path. That is the grant the declaration made --
// this target becomes readable by everyone the asset is shared with --
// expressed as the only thing a sandboxed frame or an anonymous share reader
// can carry.
//
// A reference whose target has been deleted answers 404 and leaves the asset
// rendering with one image or one data file missing, which is the rule prompt
// attachments already follow for a deleted attachment.
//
// With ?thumbnail=1 it answers the target's stored tile instead of its bytes,
// through the same token and so under the same grant: the asset's References
// panel shows a referenced file as a picture, and a reader of a shared asset
// may have no access to the target's own thumbnail route (#1794). With
// ?download=1 it answers the bytes as an attachment: the URL a download
// anchor naming the reference is rewritten to (#2127).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		httpjson.WriteError(w, http.StatusMethodNotAllowed, msgMethodNotAllowed)
		return
	}
	if !s.Ready() {
		httpjson.WriteError(w, http.StatusServiceUnavailable, msgStorageNotReady)
		return
	}

	ref, ok := s.reference(w, r)
	if !ok {
		return
	}

	if wantsThumbnail(r) {
		if opts, ok := s.thumbnailContent(w, r, ref); ok {
			blobserve.Serve(w, r, opts)
		}
		return
	}
	download := wantsDownload(r)
	switch ref.TargetKind {
	case TargetResource:
		s.serveResource(w, r, ref.TargetID, download)
	case TargetAsset:
		s.serveAsset(w, r, ref.TargetID, download)
	default:
		// A kind this build does not know is data from a future version. It is
		// answered as a missing target rather than guessed at, so an asset
		// rolled back onto an older binary renders around the reference
		// instead of serving the wrong bytes for it.
		httpjson.WriteError(w, http.StatusNotFound, msgResourceMissing)
	}
}

// wantsDownload reports whether the URL is a download anchor's, which serves
// the target as an attachment so the reader saves it (#2127).
func wantsDownload(r *http.Request) bool {
	return r.URL.Query().Get("download") == "1"
}

// reference turns the path's asset and token into the reference they name,
// writing the refusal itself when they name nothing.
//
// A token that matches no reference and one that names a different asset than
// the path does are answered identically, because they are the same answer to
// a caller who does not hold the capability; distinguishing them would let one
// be probed for the other. A reference whose target has been deleted is a
// separate answer, because the asset around it is intact and the reader is
// being told about one missing file.
func (s *Server) reference(
	w http.ResponseWriter, r *http.Request,
) (*Ref, bool) {
	assetID := r.PathValue(pathKeyAsset)
	token := r.PathValue(pathKeyRef)
	if assetID == "" || token == "" {
		httpjson.WriteError(w, http.StatusNotFound, msgRefNotFound)
		return nil, false
	}

	ref, err := s.deps.Refs.GetByToken(r.Context(), assetID, token)
	if err != nil || ref == nil {
		httpjson.WriteError(w, http.StatusNotFound, msgRefNotFound)
		return nil, false
	}
	return ref, true
}

// serveResource answers with a referenced managed resource's bytes.
//
// The object is read by range, a block at a time, whether or not the request
// names one (#2395). A <video> or <audio> element in an HTML asset issues many
// range requests while it plays and seeks, and each used to pull the whole file
// from storage and hold it in memory, on public share links as well.
func (s *Server) serveResource(w http.ResponseWriter, r *http.Request, resourceID string, download bool) {
	if !s.resourcesReady() {
		httpjson.WriteError(w, http.StatusServiceUnavailable, msgStorageNotReady)
		return
	}
	res, err := s.deps.Resources.Get(r.Context(), resourceID)
	if err != nil || res == nil {
		httpjson.WriteError(w, http.StatusNotFound, msgResourceMissing)
		return
	}
	opts := blobserve.Options{
		Name:            cmp.Or(res.Filename, res.DisplayName),
		ContentType:     res.MIMEType,
		ModTime:         res.UpdatedAt,
		ForceAttachment: download,
	}
	if res.MIMEType != "" && blobserve.ServeRanged(w, r, opts, blobserve.Object{
		Store: s.deps.Blobs, Bucket: s.deps.Bucket, Key: res.S3Key, Size: res.SizeBytes,
	}) {
		return
	}
	body, contentType, err := s.deps.Blobs.GetObject(r.Context(), s.deps.Bucket, res.S3Key)
	if err != nil {
		if resource.IsObjectNotFound(err) {
			httpjson.WriteError(w, http.StatusNotFound, msgResourceMissing)
			return
		}
		contentFailed(w, "resource_id", res.ID, err)
		return
	}
	opts.ContentType = cmp.Or(contentType, res.MIMEType)
	opts.Data = body
	blobserve.Serve(w, r, opts)
}

// serveAsset answers with a referenced asset's current stored content (#1488).
//
// The bytes are the asset's own, read live on every request, which is the point
// of the reference: a scheduled script rewrites the data file and the dashboard
// that names it serves the new numbers with nothing re-saved and no version of
// its own.
//
// Textual content is rewritten through the referenced asset's OWN references
// before it is served, so a referenced page renders with its pictures rather
// than with dead mcp:// URIs. That is one level and never a walk: the rewrite
// writes URLs, and following one is the reader's next request, not this
// handler's recursion. A cycle -- two assets referencing each other, or an
// asset referencing itself -- therefore answers with content each time instead
// of being followed here. Binary content is never rewritten and is read by
// range (#2395).
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, assetID string, download bool) {
	if !s.assetsReady() {
		httpjson.WriteError(w, http.StatusServiceUnavailable, msgStorageNotReady)
		return
	}
	asset, err := s.deps.Assets.Get(r.Context(), assetID)
	if err != nil || asset == nil || asset.DeletedAt != nil {
		httpjson.WriteError(w, http.StatusNotFound, msgResourceMissing)
		return
	}
	opts := blobserve.Options{
		Name:            asset.Name,
		ContentType:     asset.ContentType,
		ModTime:         asset.UpdatedAt,
		ForceAttachment: download,
	}
	if asset.ContentType != "" && !contenttype.IsTextual(asset.ContentType) && blobserve.ServeRanged(w, r, opts, blobserve.Object{
		Store: s.deps.AssetBlobs, Bucket: asset.S3Bucket, Key: asset.S3Key, Size: asset.SizeBytes,
	}) {
		return
	}
	body, contentType, err := s.deps.AssetBlobs.GetObject(r.Context(), asset.S3Bucket, asset.S3Key)
	if err != nil {
		if resource.IsObjectNotFound(err) {
			httpjson.WriteError(w, http.StatusNotFound, msgResourceMissing)
			return
		}
		contentFailed(w, "target_asset_id", asset.ID, err)
		return
	}
	opts.ContentType = cmp.Or(asset.ContentType, contentType)
	opts.Data = s.rewriteTarget(r, asset, opts.ContentType, body)
	blobserve.Serve(w, r, opts)
}

// rewriteTarget resolves the referenced asset's own references in the content
// about to be served. A failure to read them serves the content as stored,
// which is the rule every other serving surface follows: an unresolved URI
// costs one picture, and refusing the read would cost the whole file.
func (s *Server) rewriteTarget(
	r *http.Request, asset *portaldomain.Asset, contentType string, body []byte,
) []byte {
	refs, err := s.deps.Refs.ListByAsset(r.Context(), asset.ID)
	if err != nil {
		slog.Warn("asset reference: listing the referenced asset's own references failed",
			"target_asset_id", logsan.SanitizeForLog(asset.ID),
			"error", logsan.SanitizeForLog(err.Error()))
		return body
	}
	return Rewrite(body, contentType, BaseURL(r, s.deps.PublicBaseURL), asset.ID, refs)
}

// contentFailed answers a storage fault and records which target it was on.
func contentFailed(w http.ResponseWriter, idKey, id string, err error) {
	slog.Error("asset reference: content read failed",
		idKey, logsan.SanitizeForLog(id),
		"error", logsan.SanitizeForLog(err.Error()))
	httpjson.WriteError(w, http.StatusInternalServerError, msgContentUnavail)
}
