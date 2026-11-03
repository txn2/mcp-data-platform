// Package migrationsapi serves the admin surface for an application
// database's schema migrations (#2404): the migrations the platform keeps for
// a connection, which of them the target's ledger records as applied, the
// history of each, and the plan of the pending ones. manage_schema is how
// migrations are added and applied; this is what the portal's connection page
// reads.
//
// It is a decomposition seam of pkg/admin, which sits near its package size
// budget; the parent mounts it on the admin mux and hands it the live toolkit
// registry.
package migrationsapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// Config carries what the routes need from the parent.
type Config struct {
	// Toolkits enumerates the live toolkits, so a connection added after
	// startup is reachable without a restart. nil disables the routes.
	Toolkits func() []registry.Toolkit
}

// reporter is the part of the postgres toolkit these routes read.
type reporter interface {
	HasConnection(name string) bool
	Report(ctx context.Context, connection, schema string) (*postgreskit.SchemaReport, error)
	PendingPlan(ctx context.Context, connection, schema string) (*postgreskit.Plan, error)
}

type handler struct {
	cfg Config
}

// Register mounts the migration routes on the admin mux.
func Register(mux *http.ServeMux, cfg Config) {
	if cfg.Toolkits == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.HandleFunc("GET /api/v1/admin/connection-instances/appdb/{name}/migrations", h.list)
	mux.HandleFunc("POST /api/v1/admin/connection-instances/appdb/{name}/migrations/plan", h.plan)
}

// list handles GET /api/v1/admin/connection-instances/appdb/{name}/migrations.
//
// @Summary      List an application database's migrations
// @Description  Reports every migration the platform keeps for the connection's schema, whether the target's ledger records it as applied (applied, pending, or drift when the ledger and the platform disagree), and the history of adds, applies, reverts and failures, newest first.
// @Tags         Connections
// @Produce      json
// @Param        name    path   string  true   "Application database connection name"
// @Param        schema  query  string  false  "Schema the migrations belong to; defaults to public"
// @Success      200  {object}  postgres.SchemaReport
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Router       /admin/connection-instances/appdb/{name}/migrations [get]
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	name, schema := r.PathValue("name"), schemaOf(r)
	tk := h.toolkitFor(name)
	if tk == nil {
		httpjson.WriteError(w, http.StatusNotFound, "no application database named "+name)
		return
	}
	report, err := tk.Report(r.Context(), name, schema)
	if err != nil {
		writeFailure(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, report)
}

// plan handles POST /api/v1/admin/connection-instances/appdb/{name}/migrations/plan.
//
// @Summary      Plan an application database's pending migrations
// @Description  Runs the pending migrations in a transaction, reports the relations and columns they change and the locks they take, and rolls back. A migration that runs outside a transaction is listed as unplanned.
// @Tags         Connections
// @Produce      json
// @Param        name    path   string  true   "Application database connection name"
// @Param        schema  query  string  false  "Schema the migrations belong to; defaults to public"
// @Success      200  {object}  postgres.Plan
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Router       /admin/connection-instances/appdb/{name}/migrations/plan [post]
func (h *handler) plan(w http.ResponseWriter, r *http.Request) {
	name, schema := r.PathValue("name"), schemaOf(r)
	tk := h.toolkitFor(name)
	if tk == nil {
		httpjson.WriteError(w, http.StatusNotFound, "no application database named "+name)
		return
	}
	plan, err := tk.PendingPlan(r.Context(), name, schema)
	if err != nil {
		writeFailure(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, plan)
}

// schemaOf is the schema query parameter, public when absent.
func schemaOf(r *http.Request) string {
	if s := r.URL.Query().Get("schema"); s != "" {
		return s
	}
	return postgreskit.DefaultSchema
}

// writeFailure answers a refusal as 400 and anything else as 503: the
// target database answered, or did not, and neither is the platform's fault.
// Not 502, whose body a CDN in front of the deployment replaces (#1704). Both
// texts are the toolkit's value-free summaries.
func writeFailure(w http.ResponseWriter, err error) {
	if _, ok := errors.AsType[*postgreskit.RefusedError](err); ok {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if errors.Is(err, postgreskit.ErrNoMigrationStore) {
		httpjson.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httpjson.WriteError(w, http.StatusServiceUnavailable, err.Error())
}

// toolkitFor is the postgres toolkit serving name, or nil.
func (h *handler) toolkitFor(name string) reporter {
	for _, tk := range h.cfg.Toolkits() {
		if r, ok := tk.(reporter); ok && r.HasConnection(name) {
			return r
		}
	}
	return nil
}
