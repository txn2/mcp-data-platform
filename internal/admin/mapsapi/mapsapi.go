// Package mapsapi is the admin REST surface for the maps settings (#2068):
// whether maps are on and where their archives are written, the regions kept,
// a refresh of one, and the size a region would be before it is saved.
package mapsapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/maps"
)

// mapsPath is the section's route.
const mapsPath = "/api/v1/admin/settings/maps"

// maxBodyBytes bounds a request body: a settings write or a region.
const maxBodyBytes = 16 << 10

// Service is what the routes act through. *maps.Service satisfies it.
type Service interface {
	View(ctx context.Context, bucketOf maps.ResolvedBucket) (maps.View, error)
	SaveSettings(ctx context.Context, in maps.SettingsInput, author string) error
	AddRegion(ctx context.Context, in maps.RegionInput, author string) (*maps.Region, error)
	RefreshRegion(ctx context.Context, id string) (*maps.Region, error)
	DeleteRegion(ctx context.Context, id string) error
	Estimate(ctx context.Context, in maps.EstimateInput) (maps.Estimate, error)
}

// Config carries the service and the parent-owned helpers.
type Config struct {
	// Service is the maps surface. nil mounts nothing.
	Service Service
	// BucketOf names the bucket archives go to under a settings value.
	BucketOf maps.ResolvedBucket
	// Author resolves the acting admin.
	Author func(*http.Request) string
}

type handler struct{ cfg Config }

// Register mounts the maps routes, each behind wrap, the admin API's
// authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.Service == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.Handle("GET "+mapsPath, wrap(http.HandlerFunc(h.get)))
	mux.Handle("PUT "+mapsPath, wrap(http.HandlerFunc(h.put)))
	mux.Handle("POST "+mapsPath+"/regions", wrap(http.HandlerFunc(h.addRegion)))
	mux.Handle("DELETE "+mapsPath+"/regions/{id}", wrap(http.HandlerFunc(h.deleteRegion)))
	mux.Handle("POST "+mapsPath+"/regions/{id}/refresh", wrap(http.HandlerFunc(h.refreshRegion)))
	mux.Handle("POST "+mapsPath+"/estimate", wrap(http.HandlerFunc(h.estimate)))
}

// get handles GET /api/v1/admin/settings/maps.
//
// @Summary      Get the maps settings
// @Description  Returns whether maps are enabled, the S3 connection and bucket basemap archives are written to, the maximum zoom a fetch extracts, the source build, every region with the state of its last fetch (queued, fetching with progress, ready, or failed with the reason) and the archive it serves, the regions offered by name, and the bucket prefix an archive is uploaded to by hand.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  maps.View
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/maps [get]
func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	h.writeView(w, r, http.StatusOK)
}

func (h *handler) writeView(w http.ResponseWriter, r *http.Request, status int) {
	v, err := h.cfg.Service.View(r.Context(), h.cfg.BucketOf)
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	httpjson.WriteJSON(w, status, v)
}

// put handles PUT /api/v1/admin/settings/maps.
//
// @Summary      Update the maps settings
// @Description  Turns maps on or off and sets where archives are written (an empty s3_connection or bucket is the managed-resources one), the maximum zoom a fetch extracts (1 to 15; 14 by default), and the source a fetch reads (empty for the newest Protomaps daily build, or the URL of a .pmtiles archive the operator hosts). Turning maps off stops the archive routes answering and keeps the archives, so turning them on again serves them at once. A change to the zoom or the source applies to the next fetch; refresh a region to fetch it again. A change to the connection or the bucket leaves the archives already written where they are: refresh each region to write it to the new place.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        body  body  maps.SettingsInput  true  "Maps settings"
// @Success      200  {object}  maps.View
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/maps [put]
func (h *handler) put(w http.ResponseWriter, r *http.Request) {
	var in maps.SettingsInput
	if !decode(w, r, &in) {
		return
	}
	if err := h.cfg.Service.SaveSettings(r.Context(), in, h.cfg.Author(r)); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	h.writeView(w, r, http.StatusOK)
}

// addRegion handles POST /api/v1/admin/settings/maps/regions.
//
// @Summary      Add a basemap region
// @Description  Saves a region and queues its fetch: a preset by id (its id and name are the preset's unless given), or an id, a name and a bounding box in degrees. The fetch extracts the region from the source build at the zoom the settings name and writes it to the bucket; its state moves from queued to fetching to ready, or to failed with the reason. The id is the archive's name at /portal/maps/{id}.pmtiles.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        body  body  maps.RegionInput  true  "The region"
// @Success      201  {object}  maps.Region
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/maps/regions [post]
func (h *handler) addRegion(w http.ResponseWriter, r *http.Request) {
	var in maps.RegionInput
	if !decode(w, r, &in) {
		return
	}
	region, err := h.cfg.Service.AddRegion(r.Context(), in, h.cfg.Author(r))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusCreated, region)
}

// deleteRegion handles DELETE /api/v1/admin/settings/maps/regions/{id}.
//
// @Summary      Delete a basemap region
// @Description  Removes the region and its archive from the bucket. An uploaded region is removed by deleting its file, which this does; a map that names the region stops drawing its basemap.
// @Tags         Settings
// @Param        id  path  string  true  "Region id"
// @Success      204
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/maps/regions/{id} [delete]
func (h *handler) deleteRegion(w http.ResponseWriter, r *http.Request) {
	if err := h.cfg.Service.DeleteRegion(r.Context(), r.PathValue("id")); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// refreshRegion handles POST /api/v1/admin/settings/maps/regions/{id}/refresh.
//
// @Summary      Refresh a basemap region
// @Description  Queues a fetch of the newest build at the zoom the settings name now. The archive being served keeps serving until the new one is complete, and stays if the fetch fails. A region being fetched, and an uploaded region, are refused.
// @Tags         Settings
// @Produce      json
// @Param        id  path  string  true  "Region id"
// @Success      202  {object}  maps.Region
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/maps/regions/{id}/refresh [post]
func (h *handler) refreshRegion(w http.ResponseWriter, r *http.Request) {
	region, err := h.cfg.Service.RefreshRegion(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusAccepted, region)
}

// estimate handles POST /api/v1/admin/settings/maps/estimate.
//
// @Summary      Estimate a basemap region
// @Description  Reads the source build's directories for a preset or a bounding box at the zoom the settings name, and returns the exact size the archive would be, its tile count and the build it would come from, without writing anything. Seconds for a country; the extract itself copies that many bytes.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        body  body  maps.EstimateInput  true  "The region"
// @Success      200  {object}  maps.Estimate
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/maps/estimate [post]
func (h *handler) estimate(w http.ResponseWriter, r *http.Request) {
	var in maps.EstimateInput
	if !decode(w, r, &in) {
		return
	}
	est, err := h.cfg.Service.Estimate(r.Context(), in)
	var inputErr *maps.InputError
	switch {
	case errors.As(err, &inputErr):
		httpjson.WriteError(w, http.StatusBadRequest, inputErr.Error())
	case err != nil:
		httpjson.WriteError(w, http.StatusServiceUnavailable, "the source build could not be read: "+err.Error())
	default:
		httpjson.WriteJSON(w, http.StatusOK, est)
	}
}

// decode reads one JSON value, refusing unknown fields, and answers 400 when
// it cannot.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, fmt.Sprintf("the body is not valid: %v", err))
		return false
	}
	if dec.More() {
		httpjson.WriteError(w, http.StatusBadRequest, "the body holds more than one JSON value")
		return false
	}
	return true
}

// writeError maps the service's refusals to their status. A refusal's text
// names what to change; any other failure is logged and not returned.
func writeError(ctx context.Context, w http.ResponseWriter, err error) {
	var inputErr *maps.InputError
	switch {
	case errors.As(err, &inputErr):
		httpjson.WriteError(w, http.StatusBadRequest, inputErr.Error())
	case errors.Is(err, maps.ErrRegionNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "region not found")
	case errors.Is(err, maps.ErrRegionBusy):
		httpjson.WriteError(w, http.StatusConflict, "the region is being fetched; refresh it once the fetch ends")
	default:
		slog.ErrorContext(ctx, "maps settings request failed", "error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "the maps settings could not be read or saved; see the server log")
	}
}
