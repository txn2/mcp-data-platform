package resource

import (
	"net/http"
	"time"
)

// downloadLinkResponse is a minted download link (#2847).
type downloadLinkResponse struct {
	URL       string    `json:"url" example:"https://data.example.com/api/v1/resources/downloads/eyJ...Q/report.pdf"`
	Expires   string    `json:"expires" example:"single_download"`
	ExpiresAt time.Time `json:"expires_at"`
}

// handleDownloadLink handles GET /api/v1/resources/{id}/download-link.
//
// @Summary      Get a download link
// @Description  Returns a short-lived link anybody holding it can download the file from with no sign-in (#2847), for handing a file to a connection or service that takes one by URL. expires is single_download (the default), which ends after the first full download or an hour, or 6h, which works for six hours. The caller must be able to read the file.
// @Tags         Resources
// @Produce      json
// @Param        id   path   string   true   "Resource ID"
// @Param        expires  query  string  false  "single_download (default) or 6h"
// @Success      200  {object}  resource.downloadLinkResponse
// @Failure      400  {object}  resource.errorResponse
// @Failure      401  {object}  resource.errorResponse
// @Failure      404  {object}  resource.errorResponse
// @Failure      503  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/download-link [get]
func (h *Handler) handleDownloadLink(w http.ResponseWriter, r *http.Request) {
	res, _, ok := h.resolveReadable(w, r)
	if !ok {
		return
	}
	if h.deps.DownloadLink == nil {
		writeError(w, http.StatusServiceUnavailable, "this deployment cannot give out download links")
		return
	}
	url, expires, err := h.deps.DownloadLink(res, r.URL.Query().Get("expires"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	kind := r.URL.Query().Get("expires")
	if kind == "" {
		kind = "single_download"
	}
	writeJSON(w, http.StatusOK, downloadLinkResponse{URL: url, Expires: kind, ExpiresAt: expires})
}
