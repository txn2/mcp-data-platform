// Package appdbapi serves the admin surface for operating an application
// database (#2406, #2409, #2410, #2427): its health and history, drift and
// adopting an existing schema, maintenance, canceling and terminating a
// backend, and the administrator's console. It is mounted behind the admin
// API's authentication; every route is for administrators only, and every
// action is audited.
package appdbapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/appdbconsole"
	"github.com/txn2/mcp-data-platform/internal/appdbdrift"
	"github.com/txn2/mcp-data-platform/internal/appdbhealth"
	"github.com/txn2/mcp-data-platform/internal/appdbops"
	"github.com/txn2/mcp-data-platform/internal/appdbwatch"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// base opens every per-connection route.
const base = "/api/v1/admin/connection-instances/appdb/{name}"

// settingsPath is the health settings route.
const settingsPath = "/api/v1/admin/appdb-health/settings"

// Audit sources and the tool names an action is audited under.
const (
	SourceConsole = "admin_console"
	sourceAdmin   = "admin"

	toolConsole     = "appdb_console"
	toolMaintenance = "appdb_maintenance"
	toolAdopt       = "appdb_adopt"
	toolSignal      = "appdb_signal"
	toolSettings    = "appdb_health_settings"
)

// A backend's process id is a decimal int4.
const (
	decimalBase = 10
	pidBits     = 32
)

// keyConnection names the connection in an audit row's parameters.
const keyConnection = "connection"

// Bounds.
const (
	defaultDays         = 7
	maxDays             = appdbwatch.MaxHistoryDays
	defaultMaintenance  = 10 * time.Minute
	maxMaintenance      = time.Hour
	maxRequestBodyBytes = appdbconsole.MaxConsoleSQLBytes + 64<<10
)

// Kit is the postgres toolkit as these routes use it (Wrap).
type Kit interface {
	appdbdrift.AdoptKit
	appdbops.Maintainer
	appdbconsole.Signaller
	appdbconsole.Transactor
	HasConnection(name string) bool
	ListConnections() []toolkit.ConnectionDetail
}

// wrapped is the postgres toolkit with its two transaction adapters.
type wrapped struct{ *postgres.Toolkit }

// Wrap is the postgres toolkit as these routes use it.
func Wrap(pg *postgres.Toolkit) Kit { return wrapped{pg} }

// ReadTx implements appdbdrift.Reader.
func (w wrapped) ReadTx(ctx context.Context, connection string) (appdbdrift.Tx, error) {
	return appdbdrift.Toolkit{Toolkit: w.Toolkit}.ReadTx(ctx, connection) //nolint:wrapcheck // the adapter's error
}

// ConsoleTx implements appdbconsole.Transactor.
func (w wrapped) ConsoleTx(ctx context.Context, connection string, readOnly bool) (appdbconsole.Tx, error) {
	return appdbconsole.Toolkit{Toolkit: w.Toolkit}.ConsoleTx(ctx, connection, readOnly) //nolint:wrapcheck // the adapter's error
}

// Config carries what the routes need from the parent.
type Config struct {
	// Kit is the live postgres toolkit, nil when none is registered.
	Kit func() Kit
	// Store keeps the platform's migrations.
	Store appdbdrift.Store
	// Watch is the sampling service: the history and the settings.
	Watch *appdbwatch.Service
	// Audit records every action; nil records none.
	Audit middleware.AuditLogger
	// Actor names who made a request: the administrator's address, or their
	// user id without one.
	Actor func(r *http.Request) string
	// TakeOn puts a connection another replica saved into service here
	// before the reload bus announces it, so a request that follows the save
	// to this replica is answered as the saving replica answers it. Nil takes
	// nothing on.
	TakeOn func(ctx context.Context, kind, name string)
}

type handler struct{ cfg Config }

// Register mounts the routes, each wrapped by wrap (the admin API's
// authentication and persona check).
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.Kit == nil || cfg.Store == nil || cfg.Watch == nil {
		return
	}
	if cfg.Actor == nil {
		cfg.Actor = func(*http.Request) string { return "" }
	}
	h := &handler{cfg: cfg}
	route := func(pattern string, f http.HandlerFunc) { mux.Handle(pattern, wrap(f)) }
	route("GET "+base+"/health", h.health)
	route("GET "+base+"/health/history", h.history)
	route("GET "+base+"/drift", h.drift)
	route("POST "+base+"/adopt", h.adopt)
	route("POST "+base+"/maintenance", h.maintenance)
	route("POST "+base+"/backends/{pid}/{signal}", h.signal)
	route("POST "+base+"/console", h.console)
	route("GET "+settingsPath, h.getSettings)
	route("PUT "+settingsPath, h.putSettings)
}

// kitFor is the toolkit serving the request's connection, answering 404
// when there is none.
func (h *handler) kitFor(w http.ResponseWriter, r *http.Request) (Kit, string, bool) {
	name := r.PathValue("name")
	if h.cfg.TakeOn != nil {
		h.cfg.TakeOn(r.Context(), postgres.Kind, name)
	}
	kit := h.cfg.Kit()
	if kit == nil || !kit.HasConnection(name) {
		httpjson.WriteError(w, http.StatusNotFound, "no application database named "+name)
		return nil, "", false
	}
	return kit, name, true
}

// health handles GET .../appdb/{name}/health.
//
// @Summary      Read an application database's health
// @Description  Reads the database's own statistics views now: server version and extensions, table and index sizes, row estimates, dead tuples and the last vacuum and analyze of each table, unused, duplicate and invalid indexes, transaction id age, connections against max_connections, long-running and idle-in-transaction sessions, blocking lock chains, the cache hit ratio, replication lag, and the application's statements pg_stat_statements ranks first when it is installed (the platform's own statements left out, and those of other roles whose text the connection cannot read counted in hidden_other_roles). status (healthy, warning, critical) and findings judge the report against the health settings by the rule the alerts are posted by: a finding past an alert threshold carries its level and the threshold, and advice no alert is posted for (an unused or duplicate index, a blocked session, a long-running statement) is level info. Session and statement text can repeat a literal and is served here alone: it is never stored, logged or sent in an alert. A section the database refused is listed under errors and the rest of the report stands.
// @Tags         Connections
// @Produce      json
// @Param        name  path  string  true  "Application database connection name"
// @Success      200  {object}  appdbhealth.Report
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-instances/appdb/{name}/health [get]
func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	kit, name, ok := h.kitFor(w, r)
	if !ok {
		return
	}
	view, err := h.cfg.Watch.Settings(r.Context())
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "reading the health settings failed")
		return
	}
	report := appdbhealth.Read(r.Context(), kit, name, appdbhealth.Options{
		LongRunning: time.Duration(view.LongRunningSeconds) * time.Second,
	})
	appdbwatch.Assess(report, view.Settings)
	httpjson.WriteJSON(w, http.StatusOK, report)
}

// historyResponse is a connection's stored samples.
type historyResponse struct {
	Connection string             `json:"connection"`
	Days       int                `json:"days"`
	Points     []appdbwatch.Point `json:"points"`
}

// history handles GET .../appdb/{name}/health/history.
//
// @Summary      Read an application database's health history
// @Description  The samples the health loop kept, oldest first: sizes, transaction id age, connections, the counts of idle-in-transaction, long-running and blocked sessions and of unused, duplicate and invalid indexes, the highest dead tuple ratio, and each managed schema's drift. No statement or session text is kept.
// @Tags         Connections
// @Produce      json
// @Param        name  path   string   true   "Application database connection name"
// @Param        days  query  integer  false  "How many days back; defaults to 7"
// @Success      200  {object}  historyResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-instances/appdb/{name}/health/history [get]
func (h *handler) history(w http.ResponseWriter, r *http.Request) {
	_, name, ok := h.kitFor(w, r)
	if !ok {
		return
	}
	days := defaultDays
	if raw := r.URL.Query().Get("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxDays {
			httpjson.WriteError(w, http.StatusBadRequest, "days must be a whole number of days between 1 and "+strconv.Itoa(maxDays))
			return
		}
		days = n
	}
	points, err := h.cfg.Watch.History(r.Context(), name, days)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "reading the health history failed")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, historyResponse{Connection: name, Days: days, Points: points})
}

// driftResponse is a drift check of each schema.
type driftResponse struct {
	Connection string              `json:"connection"`
	Schemas    []appdbdrift.Result `json:"schemas"`
}

// drift handles GET .../appdb/{name}/drift.
//
// @Summary      Check an application database's schema drift
// @Description  Builds the schema the applied migrations produce on a throwaway database and compares it with the live one: tables, columns with their types, nullability and defaults, constraints, indexes, triggers, policies, row-level security, partitions, views, functions, enums and domains. Each difference names the object and whether it was added outside migrations, is missing, or changed. Without schema, every schema the platform keeps migrations for is checked.
// @Tags         Connections
// @Produce      json
// @Param        name    path   string  true   "Application database connection name"
// @Param        schema  query  string  false  "One schema to check"
// @Success      200  {object}  driftResponse
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-instances/appdb/{name}/drift [get]
func (h *handler) drift(w http.ResponseWriter, r *http.Request) {
	kit, name, ok := h.kitFor(w, r)
	if !ok {
		return
	}
	out := driftResponse{Connection: name}
	if schema := r.URL.Query().Get("schema"); schema != "" {
		out.Schemas = []appdbdrift.Result{appdbdrift.Check(r.Context(), kit, h.cfg.Store, name, schema)}
	} else {
		out.Schemas = appdbwatch.DriftOf(r.Context(), kit, h.cfg.Store, name)
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// adopt handles POST .../appdb/{name}/adopt.
//
// @Summary      Adopt an existing schema
// @Description  Reads a schema the platform keeps no migrations for and writes it as a baseline migration (version 1) marked applied, so a database created before the platform knew it is managed from then on. The baseline is first applied from zero on a throwaway database and compared with the live schema; it is saved only when nothing differs. Objects a baseline cannot write (aggregates, composite and range types, rules, inheritance, operators, collations, statistics objects, text search configurations, foreign tables) are listed and refuse the adoption. Roles and grants are not written.
// @Tags         Connections
// @Produce      json
// @Param        name    path   string  true   "Application database connection name"
// @Param        schema  query  string  false  "Schema to adopt; defaults to public"
// @Success      200  {object}  appdbdrift.AdoptResult
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-instances/appdb/{name}/adopt [post]
func (h *handler) adopt(w http.ResponseWriter, r *http.Request) {
	kit, name, ok := h.kitFor(w, r)
	if !ok {
		return
	}
	schema := r.URL.Query().Get("schema")
	if schema == "" {
		schema = postgres.DefaultSchema
	}
	res, err := appdbdrift.Adopt(r.Context(), kit, h.cfg.Store, appdbdrift.Target{Connection: name, Schema: schema, Actor: h.cfg.Actor(r)})
	params := map[string]any{keyConnection: name, "schema": schema}
	if res != nil {
		params["adopted"], params["unsupported"], params["differences"] = res.Adopted, len(res.Unsupported), len(res.Differences)
	}
	h.record(r, toolAdopt, sourceAdmin, params, err)
	switch {
	case errors.Is(err, appdbdrift.ErrManaged):
		httpjson.WriteError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeFailure(w, err)
	default:
		httpjson.WriteJSON(w, http.StatusOK, res)
	}
}

// maintenanceRequest is one maintenance command.
type maintenanceRequest struct {
	// Command is analyze, vacuum, vacuum_full or reindex.
	Command string `json:"command"`
	// Table is table or schema.table; analyze and vacuum act on the whole
	// database without one.
	Table          string `json:"table,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// maintenance handles POST .../appdb/{name}/maintenance.
//
// @Summary      Run maintenance on an application database
// @Description  Runs analyze, vacuum (with ANALYZE), vacuum_full (VACUUM FULL, which holds an ACCESS EXCLUSIVE lock on the table while it rewrites it) or reindex (REINDEX TABLE CONCURRENTLY) outside a transaction, as the connection's own login, under a 5 second lock_timeout. vacuum_full and reindex need a table. Creating or dropping an index is a schema change and is refused naming manage_schema.
// @Tags         Connections
// @Accept       json
// @Produce      json
// @Param        name  path  string              true  "Application database connection name"
// @Param        body  body  maintenanceRequest  true  "The command"
// @Success      200  {object}  appdbops.Outcome
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-instances/appdb/{name}/maintenance [post]
func (h *handler) maintenance(w http.ResponseWriter, r *http.Request) {
	kit, name, ok := h.kitFor(w, r)
	if !ok {
		return
	}
	var req maintenanceRequest
	if !decode(w, r, &req) {
		return
	}
	timeout := defaultMaintenance
	if req.TimeoutSeconds > 0 {
		timeout = min(time.Duration(req.TimeoutSeconds)*time.Second, maxMaintenance)
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout+time.Minute)
	defer cancel()
	out, err := appdbops.Maintain(ctx, kit, appdbops.Request{Connection: name, Command: req.Command, Table: req.Table, Timeout: timeout})
	h.record(r, toolMaintenance, sourceAdmin, map[string]any{keyConnection: name, "command": req.Command, "table": req.Table}, err)
	if err != nil {
		writeFailure(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// signal handles POST .../appdb/{name}/backends/{pid}/{signal}.
//
// @Summary      Cancel or terminate an application database backend
// @Description  Cancels the statement a backend runs (cancel) or ends the backend (terminate). Only a backend in the connection's database logged in as one of the connection's roles (its user and each persona role) is acted on; the check and the signal are one statement. Any other backend is refused.
// @Tags         Connections
// @Produce      json
// @Param        name    path  string   true  "Application database connection name"
// @Param        pid     path  integer  true  "The backend's process id"
// @Param        signal  path  string   true  "cancel or terminate"
// @Success      200  {object}  appdbconsole.SignalResult
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-instances/appdb/{name}/backends/{pid}/{signal} [post]
func (h *handler) signal(w http.ResponseWriter, r *http.Request) {
	kit, name, ok := h.kitFor(w, r)
	if !ok {
		return
	}
	pid, err := strconv.ParseInt(r.PathValue("pid"), decimalBase, pidBits)
	if err != nil || pid <= 0 {
		httpjson.WriteError(w, http.StatusBadRequest, "pid must be a backend's process id")
		return
	}
	sig := r.PathValue("signal")
	out, err := appdbconsole.Signal(r.Context(), kit, name, sig, pid)
	h.record(r, toolSignal, sourceAdmin, map[string]any{keyConnection: name, "signal": sig, "pid": pid}, err)
	if err != nil {
		writeFailure(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// console handles POST .../appdb/{name}/console.
//
// @Summary      Run statements in an application database's console
// @Description  Runs one statement, several separated by semicolons, or a list, in one transaction as the connection's own login through its pool, READ ONLY unless read_only is false. A write commits only with read_only false, and the answer says it committed. Every request is audited with source admin_console: the SQL text and each bound parameter's name or position and type, never a value. What the server said about a failure, DETAIL included, is answered here alone.
// @Tags         Connections
// @Accept       json
// @Produce      json
// @Param        name  path  string                   true  "Application database connection name"
// @Param        body  body  appdbconsole.ConsoleRequest  true  "The statements"
// @Success      200  {object}  appdbconsole.ConsoleResult
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-instances/appdb/{name}/console [post]
func (h *handler) console(w http.ResponseWriter, r *http.Request) {
	kit, name, ok := h.kitFor(w, r)
	if !ok {
		return
	}
	var req appdbconsole.ConsoleRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := appdbconsole.RunConsole(r.Context(), kit, name, req)
	h.recordConsole(r, name, req, res, err)
	if err != nil {
		writeFailure(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, res)
}

// cmpErr is the first non-nil error.
func cmpErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// recordConsole audits a console request: the SQL text and the shape of
// each statement's parameters, whether it committed, and the value-free
// summary of a failure. No bound value and no DETAIL.
func (h *handler) recordConsole(r *http.Request, name string, req appdbconsole.ConsoleRequest, res *appdbconsole.ConsoleResult, err error) {
	stmts, parseErr := req.Parse()
	err = cmpErr(parseErr, err)
	readOnly := req.IsReadOnly()
	list := make([]map[string]any, 0, len(stmts))
	for _, s := range stmts {
		list = append(list, map[string]any{"sql": s.SQL, "params": appdbconsole.Shapes(s.Params)})
	}
	params := map[string]any{keyConnection: name, "read_only": readOnly, "statements": list}
	if res != nil {
		params["committed"] = res.Committed
		if res.Error != nil {
			params["failed_statement"] = res.Error.Index
			err = cmpErr(err, errors.New(res.Error.Summary))
		}
	}
	h.record(r, toolConsole, SourceConsole, params, err)
}

// settings handles GET /api/v1/admin/appdb-health/settings.
//
// @Summary      Read the application database health settings
// @Description  How often each application database is sampled, how long the history is kept, how long a statement runs before its session is listed, the channels the alerts go to, and each condition's warning and critical thresholds (transaction id age, idle-in-transaction seconds, percent of max_connections, invalid indexes, dead tuple percent, drifted objects). A threshold of 0 is off.
// @Tags         Connections
// @Produce      json
// @Success      200  {object}  appdbwatch.View
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/appdb-health/settings [get]
func (h *handler) getSettings(w http.ResponseWriter, r *http.Request) {
	view, err := h.cfg.Watch.Settings(r.Context())
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "reading the health settings failed")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, view)
}

// putSettings handles PUT /api/v1/admin/appdb-health/settings.
//
// @Summary      Write the application database health settings
// @Description  Replaces the settings. A threshold of 0 is off; a critical threshold below its warning is refused.
// @Tags         Connections
// @Accept       json
// @Produce      json
// @Param        body  body  appdbwatch.Settings  true  "The settings"
// @Success      200  {object}  appdbwatch.View
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/appdb-health/settings [put]
func (h *handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var v appdbwatch.Settings
	if !decode(w, r, &v) {
		return
	}
	err := h.cfg.Watch.SaveSettings(r.Context(), v, h.cfg.Actor(r))
	channels := len(v.Channels)
	h.record(r, toolSettings, sourceAdmin, map[string]any{"channels": channels}, err)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.getSettings(w, r)
}

// decode reads a JSON body, keeping numbers exact, answering 400 when it
// cannot.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the request body is not the JSON this route takes")
		return false
	}
	return true
}

// writeFailure answers a refusal as 400 and a database that did not answer
// as 503. Not 502, whose body a CDN in front of the deployment replaces
// (#1704). Every text is a value-free summary.
func writeFailure(w http.ResponseWriter, err error) {
	var refused *appdbconsole.RefusedError
	var toolkitRefused *postgres.RefusedError
	var failed *postgres.FailedError
	switch {
	case errors.As(err, &refused), errors.As(err, &toolkitRefused), errors.Is(err, appdbops.ErrSchemaChange),
		errors.Is(err, appdbops.ErrTableRequired), errors.Is(err, appdbdrift.ErrTooLarge):
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.As(err, &failed) && failed.Code == postgres.CodeStatementFailed:
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpjson.WriteError(w, http.StatusServiceUnavailable, err.Error())
	}
}

// record audits one action. Best-effort: a failed write is warned and the
// request stands.
func (h *handler) record(r *http.Request, tool, source string, params map[string]any, opErr error) {
	if h.cfg.Audit == nil {
		return
	}
	ev := middleware.AuditEvent{
		ID: audit.NewEventID(), Timestamp: time.Now().UTC(), ToolName: tool, ToolkitKind: postgres.Kind,
		Source: source, Transport: "http", EventKind: string(audit.EventTypeAdmin),
		Authorized: true, Success: opErr == nil, Parameters: params,
	}
	if c, ok := params[keyConnection].(string); ok {
		ev.Connection = c
	}
	if who := h.cfg.Actor(r); who != "" {
		ev.UserID = who
		if strings.Contains(who, "@") {
			ev.UserEmail = who
		}
	}
	if opErr != nil {
		ev.ErrorMessage = opErr.Error()
	}
	if err := h.cfg.Audit.Log(r.Context(), ev); err != nil {
		slog.Warn("application database action audit failed", "tool", tool, "error", logsan.SanitizeForLog(err.Error()))
	}
}
