// Package tablesapi is the admin REST surface over table registrations
// (#2136): every registration on the platform, and a connection's scratch
// schema reconciled against them -- tables no registration holds,
// registrations whose table is gone, tables reading somewhere their
// registration does not say -- with the repairs the platform can prove are
// its own to make.
package tablesapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/tablereconcile"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
)

// The routes.
const (
	registrationsPath = "/api/v1/admin/tables/registrations"
	reconcilePath     = "/api/v1/admin/tables/reconcile"
)

// maxBodyBytes bounds a repair request.
const maxBodyBytes = 256 << 10

// maxRepairs bounds the repairs one request asks for.
const maxRepairs = 500

// Registrations lists registrations. tableregister.Registrar satisfies it.
type Registrations interface {
	List(ctx context.Context, f tableregister.Filter) ([]tableregister.Registration, int, error)
}

// Reconciler reconciles and repairs a connection. tablereconcile.Reconciler
// satisfies it.
type Reconciler interface {
	Reconcile(ctx context.Context, connection string) (*tablereconcile.Reconciliation, error)
	Repair(ctx context.Context, caller tableregister.Caller, connection string, items []tablereconcile.RepairItem) ([]tablereconcile.RepairOutcome, error)
}

// Config carries what the routes act through.
type Config struct {
	// Registrations lists. Nil mounts nothing.
	Registrations Registrations
	// Reconciler reconciles. Nil mounts the listing alone.
	Reconciler Reconciler
	// Caller is the acting administrator, as a repair's audit event records
	// them.
	Caller func(*http.Request) tableregister.Caller
}

type handler struct{ cfg Config }

// Register mounts the routes, each behind wrap, the admin API's
// authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.Registrations == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.Handle("GET "+registrationsPath, wrap(http.HandlerFunc(h.list)))
	if cfg.Reconciler != nil {
		mux.Handle("GET "+reconcilePath, wrap(http.HandlerFunc(h.reconcile)))
		mux.Handle("POST "+reconcilePath, wrap(http.HandlerFunc(h.repair)))
	}
}

// list handles GET /api/v1/admin/tables/registrations.
//
// @Summary      List table registrations
// @Description  Every table registration on the platform, newest first, filtered by connection, by the persona a table was registered under (the prefix its name carries), by source kind and id, and by a free-text match on the qualified name.
// @Tags         Tables
// @Produce      json
// @Param        connection   query  string  false  "Connection"
// @Param        persona      query  string  false  "Persona the table was registered under"
// @Param        source_kind  query  string  false  "resource, asset or webhook"
// @Param        source_id    query  string  false  "Source id; read with source_kind"
// @Param        q            query  string  false  "Matches the qualified name"
// @Param        limit        query  int     false  "Page size, at most 200"
// @Param        offset       query  int     false  "Offset"
// @Success      200  {object}  RegistrationList
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/tables/registrations [get]
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := tableregister.Filter{
		AllConnections: true, SourceKind: q.Get("source_kind"), SourceID: q.Get("source_id"),
		Persona: q.Get("persona"), Query: q.Get("q"),
		Limit: atoi(q.Get("limit")), Offset: atoi(q.Get("offset")),
	}
	if c := strings.TrimSpace(q.Get("connection")); c != "" {
		f.AllConnections, f.Connections = false, []string{c}
	}
	page, total, err := h.cfg.Registrations.List(r.Context(), f)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "listing the registrations failed")
		return
	}
	if page == nil {
		page = []tableregister.Registration{}
	}
	httpjson.WriteJSON(w, http.StatusOK, RegistrationList{Registrations: page, Total: total})
}

// reconcile handles GET /api/v1/admin/tables/reconcile.
//
// @Summary      Reconcile a scratch schema against its registrations
// @Description  Lists the tables in a connection's scratch schema that no registration holds (orphans), with the location each reads and the stored file it belongs to when there is one; the registrations whose table is gone (missing); and the registrations whose table reads somewhere else (drifted). A webhook source's own tables are neither. An orphan is provable when its location is the directory of a file the platform stores; only provable orphans and missing registrations can be repaired.
// @Tags         Tables
// @Produce      json
// @Param        connection  query  string  true  "Connection"
// @Success      200  {object}  tablereconcile.Reconciliation
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/tables/reconcile [get]
func (h *handler) reconcile(w http.ResponseWriter, r *http.Request) {
	connection := strings.TrimSpace(r.URL.Query().Get("connection"))
	if connection == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "name the connection to reconcile")
		return
	}
	out, err := h.cfg.Reconciler.Reconcile(r.Context(), connection)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// repair handles POST /api/v1/admin/tables/reconcile.
//
// @Summary      Repair what a reconcile found
// @Description  Carries out each repair asked for: "drop" removes an orphan table by name when its location is the directory of a file the platform stores, and "forget" removes a registration whose table is gone. Each is proved again when it is made; one that cannot be is reported and not made. Every repair carried out is audited.
// @Tags         Tables
// @Accept       json
// @Produce      json
// @Param        body  body  RepairRequest  true  "The repairs"
// @Success      200  {object}  RepairResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/tables/reconcile [post]
func (h *handler) repair(w http.ResponseWriter, r *http.Request) {
	var in RepairRequest
	if err := decode(w, r, &in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(in.Connection) == "" || len(in.Repair) == 0 {
		httpjson.WriteError(w, http.StatusBadRequest, "name the connection and at least one repair")
		return
	}
	if len(in.Repair) > maxRepairs {
		httpjson.WriteError(w, http.StatusBadRequest, fmt.Sprintf("at most %d repairs at once", maxRepairs))
		return
	}
	var caller tableregister.Caller
	if h.cfg.Caller != nil {
		caller = h.cfg.Caller(r)
	}
	out, err := h.cfg.Reconciler.Repair(r.Context(), caller, in.Connection, in.Repair)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, RepairResponse{Outcomes: out})
}

// writeError maps a reconcile failure to its status: a connection with no
// scratch target is the caller's to change; anything else is the engine's
// or the store's, and its text says which.
func writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, tableregister.ErrNoScratchTarget) {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httpjson.WriteError(w, http.StatusServiceUnavailable, err.Error())
}

// decode reads a JSON body, refusing unknown fields and trailing data.
func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("the body is not a valid repair request: %w", err)
	}
	if dec.More() {
		return errors.New("the body holds more than one JSON value")
	}
	return nil
}

// atoi reads a non-negative integer, or zero.
func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
