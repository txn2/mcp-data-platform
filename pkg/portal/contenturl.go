package portal

import (
	"cmp"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/contenturl"
	"github.com/txn2/mcp-data-platform/pkg/blobserve"
)

// Expiring, signed content URLs (#1848): a reader of an asset mints a link to
// one version that anybody holding it can download until it expires, with no
// portal session. An application hands it to a browser.

// contentURLResponse is a minted link.
type contentURLResponse struct {
	// URL is the absolute link where the deployment names its public base
	// URL; Path is the same link relative to this server.
	URL       string    `json:"url,omitempty" example:"https://data.example.com/api/v1/portal/content/eyJ...Q"`
	Path      string    `json:"path" example:"/api/v1/portal/content/eyJ...Q"`
	Version   int       `json:"version" example:"3"`
	ExpiresAt time.Time `json:"expires_at"`
}

// mintContentURL handles GET /api/v1/portal/assets/{id}/content-url.
//
// @Summary      Get a signed content URL
// @Description  Returns an expiring, signed URL that downloads one version of the asset without a portal session: the current version, or the one named by version. ttl is its lifetime in seconds (default 300, at most 86400). The caller must be able to view the asset. The link keeps serving that exact version until it expires and answers 403 afterwards.
// @Tags         Assets
// @Produce      json
// @Param        id       path   string   true   "Asset ID"
// @Param        version  query  integer  false  "Version to link to (default: current)"
// @Param        ttl      query  integer  false  "Lifetime in seconds (default 300, max 86400)"
// @Success      200  {object}  contentURLResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/content-url [get]
func (h *Handler) mintContentURL(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	id := r.PathValue(pathKeyID)
	asset, err := h.deps.AssetStore.Get(r.Context(), id)
	if err != nil || asset.DeletedAt != nil {
		writeError(w, http.StatusNotFound, errAssetNotFound)
		return
	}
	if !h.canViewAsset(w, r, id, asset, user) {
		return
	}
	version, ttl, ok := contentURLParams(w, r, asset.CurrentVersion)
	if !ok {
		return
	}
	if _, err := h.deps.VersionStore.GetByVersion(r.Context(), id, version); err != nil {
		writeError(w, http.StatusNotFound, "version not found")
		return
	}
	target := contenturl.Target{AssetID: id, Version: version, Expires: time.Now().Add(ttl).Truncate(time.Second)}
	path := contenturl.Path + contenturl.Sign(h.deps.ContentURLKey, target)
	resp := contentURLResponse{Path: path, Version: version, ExpiresAt: target.Expires}
	if h.deps.PublicBaseURL != "" {
		resp.URL = h.deps.PublicBaseURL + path
	}
	writeJSON(w, http.StatusOK, resp)
}

// contentURLParams reads version (default: current) and ttl in seconds.
func contentURLParams(w http.ResponseWriter, r *http.Request, current int) (int, time.Duration, bool) {
	version := current
	if raw := r.URL.Query().Get("version"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "version must be a version number")
			return 0, 0, false
		}
		version = n
	}
	var ttl time.Duration
	if raw := r.URL.Query().Get("ttl"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "ttl must be a whole number of seconds")
			return 0, 0, false
		}
		ttl = time.Duration(n) * time.Second
	}
	return version, contenturl.ClampTTL(ttl), true
}

// serveSignedContent handles GET /api/v1/portal/content/{token}: the version a
// signed URL names, to whoever holds it, until it expires.
//
// @Summary      Download through a signed URL
// @Description  Serves the asset version a signed content URL names, with no session. An expired or altered link answers 403; a version or asset since removed answers 404.
// @Tags         Assets
// @Param        token  path  string  true  "The signed token"
// @Success      200
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Router       /portal/content/{token} [get]
func (h *Handler) serveSignedContent(w http.ResponseWriter, r *http.Request) {
	target, err := contenturl.Verify(h.deps.ContentURLKey, r.PathValue("token"), time.Now())
	if errors.Is(err, contenturl.ErrExpired) {
		writeError(w, http.StatusForbidden, contenturl.ErrExpired.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusForbidden, contenturl.ErrInvalid.Error())
		return
	}
	asset, err := h.deps.AssetStore.Get(r.Context(), target.AssetID)
	if err != nil || asset.DeletedAt != nil {
		writeError(w, http.StatusNotFound, errAssetNotFound)
		return
	}
	ver, err := h.deps.VersionStore.GetByVersion(r.Context(), target.AssetID, target.Version)
	if err != nil {
		writeError(w, http.StatusNotFound, "version not found")
		return
	}
	data, contentType, err := h.deps.S3Client.GetObject(r.Context(), ver.S3Bucket, ver.S3Key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retrieve content")
		return
	}
	blobserve.Serve(w, r, blobserve.Options{
		Name:        asset.Name,
		ContentType: cmp.Or(contentType, ver.ContentType),
		ModTime:     ver.CreatedAt,
		Data:        data,
	})
}

// contentURLsReady reports whether this deployment can mint and serve signed
// content URLs: a signing key and versioned storage.
func (h *Handler) contentURLsReady() bool {
	return len(h.deps.ContentURLKey) > 0 && h.versionedStorageReady()
}
