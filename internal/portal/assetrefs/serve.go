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
// route answering that the file is unavailable rather than panicking, which is
// what a deployment with no managed-resource layer should serve.
type Deps struct {
	Refs      portaldomain.AssetResourceRefStore
	Resources Resources
	Blobs     BlobReader
	// Bucket is the bucket managed-resource blobs live in. It is the resource
	// layer's bucket, not the portal's: a reference points at a resource.
	Bucket string
}

// Server answers the reference-serving route.
type Server struct{ deps Deps }

// New builds the serving handler.
func New(deps Deps) *Server { return &Server{deps: deps} }

// Ready reports whether this deployment can serve references at all. A handler
// that is not ready is not registered, so the route 404s as an unknown path
// rather than as a missing file.
func (s *Server) Ready() bool {
	return s != nil && s.deps.Refs != nil && s.deps.Resources != nil && s.deps.Blobs != nil
}

// ServeHTTP answers GET /portal/refs/{id}/{ref} with the referenced resource's
// bytes.
//
// It takes no session and reads no identity. Authorization is the token in the
// path: it was minted for one (asset, resource) pair, it reaches a reader only
// inside content that reader was already allowed to open, and it resolves to
// nothing on any other asset's path. That is the grant the declaration made --
// this file becomes readable by everyone the asset is shared with -- expressed
// as the only thing a sandboxed frame or an anonymous share reader can carry.
//
// A reference whose resource has been deleted answers 404 and leaves the asset
// rendering with one image missing, which is the rule prompt attachments
// already follow for a deleted attachment.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		httpjson.WriteError(w, http.StatusMethodNotAllowed, msgMethodNotAllowed)
		return
	}
	if !s.Ready() {
		httpjson.WriteError(w, http.StatusServiceUnavailable, msgStorageNotReady)
		return
	}

	res, ok := s.resolve(w, r)
	if !ok {
		return
	}

	body, contentType, err := s.deps.Blobs.GetObject(r.Context(), s.deps.Bucket, res.S3Key)
	if err != nil {
		if resource.IsObjectNotFound(err) {
			httpjson.WriteError(w, http.StatusNotFound, msgResourceMissing)
			return
		}
		slog.Error("asset resource reference: blob read failed",
			"resource_id", logsan.SanitizeForLog(res.ID),
			"error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, msgContentUnavail)
		return
	}

	blobserve.Serve(w, r, blobserve.Options{
		Name:        cmp.Or(res.Filename, res.DisplayName),
		ContentType: cmp.Or(contentType, res.MIMEType),
		ModTime:     res.UpdatedAt,
		Data:        body,
	})
}

// resolve turns the path's asset and token into the resource they name,
// writing the refusal itself when they name nothing.
//
// A token that matches no reference and one that names a different asset than
// the path does are answered identically, because they are the same answer to
// a caller who does not hold the capability; distinguishing them would let one
// be probed for the other. A reference whose resource has been deleted is a
// separate answer, because the asset around it is intact and the reader is
// being told about one missing image.
func (s *Server) resolve(w http.ResponseWriter, r *http.Request) (*resource.Resource, bool) {
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

	res, err := s.deps.Resources.Get(r.Context(), ref.ResourceID)
	if err != nil || res == nil {
		httpjson.WriteError(w, http.StatusNotFound, msgResourceMissing)
		return nil, false
	}
	return res, true
}
