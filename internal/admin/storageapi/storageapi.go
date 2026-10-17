// Package storageapi is the admin REST surface for storage capacity (#2148):
// every store the platform writes to, used and free, with its growth and when
// it would fill, and a store's history for its chart.
package storageapi

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/storagecap"
)

// The routes.
const (
	reportPath  = "/api/v1/admin/storage"
	historyPath = "/api/v1/admin/storage/history"
)

// The history a chart may ask for.
const (
	defaultDays = 30
	maxDays     = 3650
)

// storeKey is the shape of a store key: database, seaweedfs, bucket:<name>,
// spool:<replica>.
var storeKey = regexp.MustCompile(`^(database|seaweedfs|(bucket|spool):\S{1,256})$`)

// Service is what the routes read. *storagecap.Service satisfies it.
type Service interface {
	Report(ctx context.Context) (storagecap.Report, error)
	History(ctx context.Context, store string, days int) ([]storagecap.Point, error)
}

// HistoryView is a store's samples.
type HistoryView struct {
	Store  string             `json:"store"`
	Days   int                `json:"days"`
	Points []storagecap.Point `json:"points"`
}

type handler struct{ svc Service }

// Register mounts the routes behind wrap, the admin API's authentication. A
// nil service mounts nothing.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, svc Service) {
	if svc == nil {
		return
	}
	h := &handler{svc: svc}
	mux.Handle("GET "+reportPath, wrap(http.HandlerFunc(h.report)))
	mux.Handle("GET "+historyPath, wrap(http.HandlerFunc(h.history)))
}

// report handles GET /api/v1/admin/storage.
//
// @Summary      Get storage capacity
// @Description  Reads every store the platform writes to now: the database against its limit, the SeaweedFS cluster's disks, each bucket against its budget, and each replica's webhook spool volume. Each store carries its percent used, its growth fitted over 7 and 30 days, the rate the projection uses (the slower growing one) and when it would fill, and its alert level. Beside them: each bucket's usage by purpose and when it was last listed, the last reconcile's orphaned objects and dangling references, the largest tables and indexes, and each spool with its volume.
// @Tags         Storage
// @Produce      json
// @Success      200  {object}  storagecap.Report
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/storage [get]
func (h *handler) report(w http.ResponseWriter, r *http.Request) {
	rep, err := h.svc.Report(r.Context())
	if err != nil {
		fail(r.Context(), w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, rep)
}

// history handles GET /api/v1/admin/storage/history.
//
// @Summary      Get a store's storage history
// @Description  Returns one store's samples, oldest first: used bytes and capacity, over the last days (default 30). The store is a key a storage report names: database, seaweedfs, bucket:<name> or spool:<replica>.
// @Tags         Storage
// @Produce      json
// @Param        store  query  string  true   "Store key, e.g. database or bucket:portal"
// @Param        days   query  int     false  "Days of history (default 30)"
// @Success      200  {object}  HistoryView
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/storage/history [get]
func (h *handler) history(w http.ResponseWriter, r *http.Request) {
	store := r.URL.Query().Get("store")
	if !storeKey.MatchString(store) {
		httpjson.WriteError(w, http.StatusBadRequest, "store must be database, seaweedfs, bucket:<name> or spool:<replica>")
		return
	}
	days := defaultDays
	if raw := r.URL.Query().Get("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxDays {
			httpjson.WriteError(w, http.StatusBadRequest, "days must be a whole number from 1 to 3650")
			return
		}
		days = n
	}
	points, err := h.svc.History(r.Context(), store, days)
	if err != nil {
		fail(r.Context(), w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, HistoryView{Store: store, Days: days, Points: points})
}

// fail logs a read that failed and answers 500 without its detail.
func fail(ctx context.Context, w http.ResponseWriter, err error) {
	slog.ErrorContext(ctx, "storage capacity request failed", "error", logsan.SanitizeForLog(err.Error()))
	httpjson.WriteError(w, http.StatusInternalServerError, "storage capacity could not be read; see the server log")
}
