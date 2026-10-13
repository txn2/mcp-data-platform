// Package catalogscanapi is the admin REST surface for the platform catalog's
// scan (#1918): its settings (how often every connection is scanned, and what
// each connection keeps out of the catalog), each connection's last scan, and
// a request to scan now.
package catalogscanapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/catalogscan"
)

// The routes.
const (
	settingsPath = "/api/v1/admin/catalog/settings"
	scansPath    = "/api/v1/admin/catalog/scans"
)

// maxBodyBytes bounds a settings body.
const maxBodyBytes = 256 << 10

// Store reads and saves the settings and reads the last scans.
// catalogscan.Store satisfies it.
type Store interface {
	Get(ctx context.Context) (catalogscan.Settings, error)
	Set(ctx context.Context, s catalogscan.Settings, author string) error
	Outcomes(ctx context.Context) ([]catalogscan.Outcome, error)
}

// Scanner records a request for a scan without waiting for it.
// catalogscan.Loop satisfies it.
type Scanner interface {
	ScanNow(ctx context.Context, by string) error
}

// Config carries the store, the scanner and the parent-owned helpers.
type Config struct {
	// Store holds the settings. nil mounts nothing.
	Store Store
	// Scanner runs a requested scan. nil leaves the request route unmounted.
	Scanner Scanner
	// Author resolves the acting admin.
	Author func(*http.Request) string
}

// ScanList is the last scan of every connection.
type ScanList struct {
	Scans []catalogscan.Outcome `json:"scans"`
}

// ScanRequested acknowledges a scan request.
type ScanRequested struct {
	Status string `json:"status"`
}

type handler struct{ cfg Config }

// Register mounts the routes, each behind wrap, the admin API's
// authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.Store == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.Handle("GET "+settingsPath, wrap(http.HandlerFunc(h.getSettings)))
	mux.Handle("PUT "+settingsPath, wrap(http.HandlerFunc(h.putSettings)))
	mux.Handle("GET "+scansPath, wrap(http.HandlerFunc(h.listScans)))
	if cfg.Scanner != nil {
		mux.Handle("POST "+scansPath, wrap(http.HandlerFunc(h.requestScan)))
	}
}

// getSettings handles GET /api/v1/admin/catalog/settings.
//
// @Summary      Get catalog settings
// @Description  Returns how often every connection is scanned into the catalog and the dotted catalog, schema or operation prefixes each connection keeps out of it. A deployment that never saved settings reads the defaults.
// @Tags         Catalog
// @Produce      json
// @Success      200  {object}  catalogscan.Settings
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/catalog/settings [get]
func (h *handler) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.cfg.Store.Get(r.Context())
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "reading catalog settings failed")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, s)
}

// putSettings handles PUT /api/v1/admin/catalog/settings.
//
// @Summary      Save catalog settings
// @Description  Replaces the catalog settings. scan_interval is a duration such as 6h, at least 5m. Each exclusion names a connection by kind and name and lists dotted prefixes: a Trino catalog, catalog.schema, or catalog.schema.table; an operation id; a GraphQL query or mutation path. The next scan leaves excluded datasets out and marks any it cataloged before as removed.
// @Tags         Catalog
// @Accept       json
// @Produce      json
// @Param        body  body      catalogscan.Settings  true  "The settings"
// @Success      200   {object}  catalogscan.Settings
// @Failure      400   {object}  httpjson.ProblemDetail
// @Failure      500   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/catalog/settings [put]
func (h *handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var in catalogscan.Settings
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, fmt.Sprintf("the body is not valid catalog settings: %v", err))
		return
	}
	if in.Exclusions == nil {
		in.Exclusions = make([]catalogscan.Exclusion, 0)
	}
	if err := in.Validate(); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.cfg.Store.Set(r.Context(), in, h.author(r)); err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "saving catalog settings failed")
		return
	}
	saved, err := h.cfg.Store.Get(r.Context())
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "reading catalog settings failed")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, saved)
}

// listScans handles GET /api/v1/admin/catalog/scans.
//
// @Summary      List catalog scans
// @Description  Returns the last scan of every connection: when it ran, how many datasets it wrote, how many it marked removed, and why it stopped when it failed. A failed scan marks nothing removed.
// @Tags         Catalog
// @Produce      json
// @Success      200  {object}  ScanList
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/catalog/scans [get]
func (h *handler) listScans(w http.ResponseWriter, r *http.Request) {
	scans, err := h.cfg.Store.Outcomes(r.Context())
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "reading catalog scans failed")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, ScanList{Scans: scans})
}

// requestScan handles POST /api/v1/admin/catalog/scans.
//
// @Summary      Scan every connection now
// @Description  Records a request to scan every connection without waiting for the interval, and returns before the scan runs. Whichever replica next takes the scan lock runs it, so a request made while another replica is scanning is kept. Read its outcome from GET /admin/catalog/scans.
// @Tags         Catalog
// @Produce      json
// @Success      202  {object}  ScanRequested
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/catalog/scans [post]
func (h *handler) requestScan(w http.ResponseWriter, r *http.Request) {
	if err := h.cfg.Scanner.ScanNow(r.Context(), h.author(r)); err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "requesting a scan failed")
		return
	}
	httpjson.WriteJSON(w, http.StatusAccepted, ScanRequested{Status: "requested"})
}

// author names the acting admin, or nothing when no resolver is wired.
func (h *handler) author(r *http.Request) string {
	if h.cfg.Author == nil {
		return ""
	}
	return h.cfg.Author(r)
}
