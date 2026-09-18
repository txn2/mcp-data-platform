// Package tablehttp serves the register / unregister actions that make an
// uploaded file readable as a query-engine table (#1327).
//
// One handler serves both kinds. A managed resource and a portal asset reach
// the registrar through different records -- a resource is a person's upload,
// an asset is what the platform wrote for them -- but the action, the request
// body, and the response are the same, so the parts that differ are a Subject
// resolver per kind and nothing else.
//
// It sits beside the resource and portal handlers rather than inside them: the
// routes it registers are more specific than the prefix mounts those handlers
// occupy, so ServeMux gives them precedence, and neither public package gains
// a dependency on the registrar.
package tablehttp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// Subject resolves the record a route's {id} names into what the registrar
// needs, and decides whether this caller may act on it. Returning ok=false
// means the caller may not act on the record at all, which is answered as a
// not-found so the surface never reveals a record the caller could not reach.
//
// It is the registrar's own resolver type rather than one of this package's,
// so the rule for who may register a file is written once and serves the tool
// surface as well as these routes.
type Subject = tableregister.Subject

// ConnectionChoice is one connection a person can register onto.
type ConnectionChoice struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Catalog     string `json:"catalog"`
	Schema      string `json:"schema"`
}

// ConnectionEnumerator lists the connections this caller may register onto:
// granted to their persona and carrying a scratch target. It is the picker's
// only source, so a connection the form offers is one the registrar accepts.
type ConnectionEnumerator func(ctx context.Context, user *portal.User) []ConnectionChoice

// Deps is what the handler needs.
type Deps struct {
	Registrar *tableregister.Registrar
	// Resources and Assets resolve the two kinds. Either may be nil, which
	// leaves that kind's routes unregistered rather than answering an error.
	Resources Subject
	Assets    Subject
	// Connections fills the picker. Nil serves an empty list, which a form
	// renders as "no connection here can hold a table".
	Connections ConnectionEnumerator
	// Visible reports the connections a caller may SEE registrations on, which
	// is a wider set than Connections: the picker narrows to connections that
	// can hold a NEW table, and a listing has to keep showing the tables
	// already on one that no longer can.
	Visible Visibility
	// Sources resolves the records a cross-source listing names. Nil leaves
	// every row without a source name and without the unregister action,
	// which is the listing a deployment that cannot resolve its sources can
	// honestly serve.
	Sources tableregister.Sources
	// Caller builds the registrar's view of the authenticated user: their
	// persona and whether they are an administrator.
	Caller func(*portal.User) tableregister.Caller
}

// Handler serves the table routes.
type Handler struct {
	deps Deps
}

// New builds a Handler, or nil when nothing can be registered -- no registrar,
// or neither kind resolvable. Nil is meaningful: Routes on a nil handler
// registers nothing, so a deployment with no Trino toolkit or no database
// never advertises an action that would always refuse.
func New(deps Deps) *Handler {
	if !deps.Registrar.Available() || (deps.Resources == nil && deps.Assets == nil) {
		return nil
	}
	return &Handler{deps: deps}
}

// Routes registers the table routes on a mux, each wrapped by the portal
// authentication middleware the surrounding surface uses.
func (h *Handler) Routes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	if h == nil {
		return
	}
	register := func(pattern string, fn http.HandlerFunc) {
		mux.Handle(pattern, wrap(fn))
	}

	register("GET /api/v1/table-connections", h.listConnections)
	register("GET /api/v1/tables", h.listAll)
	register("GET /api/v1/tables/{regID}", h.getOne)

	// Each kind's three routes are registered with literal patterns rather
	// than assembled from the kind's prefix: the route-parity gate reads
	// registration patterns statically, and a pattern built from a
	// parameter is a route it cannot see (see TestAdminCatalogRouteParity).
	//
	// They are also registered through a named method per route rather than
	// through the shared implementation directly: swaggo documents one
	// operation per annotated function, and the two kinds differ in what
	// {id} names and in who may act on it, which is a sentence each rather
	// than one generic sentence twice.
	if h.deps.Resources != nil {
		register("GET /api/v1/resources/{id}/tables", h.listResourceTables)
		register("POST /api/v1/resources/{id}/tables", h.registerResourceTable)
		register("DELETE /api/v1/resources/{id}/tables/{regID}", h.unregisterResourceTable)
	}
	if h.deps.Assets != nil {
		register("GET /api/v1/portal/assets/{id}/tables", h.listAssetTables)
		register("POST /api/v1/portal/assets/{id}/tables", h.registerAssetTable)
		register("DELETE /api/v1/portal/assets/{id}/tables/{regID}", h.unregisterAssetTable)
	}
}

// registerRequest is the body of a register call.
type registerRequest struct {
	Connection string `json:"connection"`
	// TableName is optional; empty derives one from the filename.
	TableName string `json:"table_name,omitempty"`
	// Repair asks for a corrected version of the file to be saved and
	// registered when it cannot be read as a table the way it is stored. It is
	// the second submission of the form: the first is refused with what is
	// wrong, and the refusal is what offers this (#1441).
	Repair bool `json:"repair,omitempty"`
	// Follow, when false, pins the table to the version of the file it is
	// registered over (#1536). Absent or true, the table is moved onto each
	// new revision or version of the file as it is written.
	Follow *bool `json:"follow,omitempty"`
}

// followRequested resolves the optional follow flag: on unless the body said
// false.
func followRequested(follow *bool) bool {
	return follow == nil || *follow
}

// registrationView is one registration as a surface renders it. It carries the
// sample SQL and the stale flag the record itself does not hold, because both
// are what a reader needs and neither is worth storing.
type registrationView struct {
	tableregister.Registration
	QueryTable string `json:"query_table"`
	SampleSQL  string `json:"sample_sql,omitempty"`
	Stale      bool   `json:"stale"`
	// Repaired says what a correction of the file changed before it could be
	// registered, and is absent when none was needed. It is only ever set on
	// the registration that made the correction: it describes what happened
	// just now, not a property of the record.
	Repaired string `json:"repaired,omitempty"`
}

func viewOf(reg tableregister.Registration, src tableregister.Source) registrationView {
	return registrationView{
		Registration: reg,
		QueryTable:   reg.QualifiedName(),
		SampleSQL:    tableregister.SampleJoinSQL(reg),
		Stale:        reg.IsStale(src.Bucket, src.HeadKey),
	}
}

// listTables answers the registrations over one source, for either kind.
func (h *Handler) listTables(w http.ResponseWriter, r *http.Request, kind string, subject Subject) {
	_, src, ok := h.resolve(w, r, subject)
	if !ok {
		return
	}
	regs, err := h.deps.Registrar.BySource(r.Context(), kind, src.ID)
	if err != nil {
		problem(w, http.StatusInternalServerError, "could not read the registrations of this file")
		slog.Warn("table registrations: list failed", "error", logsan.SanitizeForLog(err.Error()))
		return
	}
	views := make([]registrationView, 0, len(regs))
	for _, reg := range regs {
		views = append(views, viewOf(reg, src))
	}
	writeJSON(w, http.StatusOK, map[string]any{"table_registrations": views})
}

// registerTable makes one source readable as a table, for either kind.
func (h *Handler) registerTable(w http.ResponseWriter, r *http.Request, subject Subject) {
	caller, src, ok := h.resolve(w, r, subject)
	if !ok {
		return
	}
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, http.StatusBadRequest, "the request body is not valid JSON")
		return
	}
	if strings.TrimSpace(req.Connection) == "" {
		problem(w, http.StatusBadRequest, "name the connection to register the table on")
		return
	}

	res, err := h.deps.Registrar.Register(r.Context(), caller, src, tableregister.Request{
		Connection: req.Connection,
		TableName:  req.TableName,
		Source:     "portal",
		Repair:     req.Repair,
		Follow:     followRequested(req.Follow),
	})
	if err != nil {
		refuse(w, "registering a table", err)
		return
	}
	// The source the registration was built over, not the one that was
	// resolved: a file corrected on the way in is registered over the
	// version the correction wrote, and staleness measured against the
	// version it replaced would mark a fresh registration stale.
	view := viewOf(res.Registration, res.Source)
	view.Repaired = res.Correction.Summary()
	writeJSON(w, http.StatusCreated, view)
}

// unregisterTable drops one registration, for either kind.
func (h *Handler) unregisterTable(w http.ResponseWriter, r *http.Request, subject Subject) {
	caller, _, ok := h.resolve(w, r, subject)
	if !ok {
		return
	}
	if err := h.deps.Registrar.Unregister(r.Context(), caller, r.PathValue("regID"), "portal"); err != nil {
		refuse(w, "dropping a registered table", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listResourceTables handles GET /api/v1/resources/{id}/tables.
//
// @Summary      List the tables registered over a managed resource
// @Description  Returns the registrations built over one managed resource under a `table_registrations` key, each with its fully qualified query name, a sample SELECT, and whether the table has fallen behind the file. Requires authority to change the resource -- the uploader, a platform administrator, or an administrator of the scope the resource lives in; a resource outside that authority is answered as not found.
// @Tags         Tables
// @Produce      json
// @Param        id  path  string  true  "Managed resource ID"
// @Success      200  {object}  map[string][]registrationView
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/tables [get]
func (h *Handler) listResourceTables(w http.ResponseWriter, r *http.Request) {
	h.listTables(w, r, tableregister.KindResource, h.deps.Resources)
}

// registerResourceTable handles POST /api/v1/resources/{id}/tables.
//
// @Summary      Register a managed resource as a query-engine table
// @Description  Creates an external table over the resource's directory on the named connection, so the CSV stored there can be queried. Requires authority to change the resource -- the uploader, a platform administrator, or an administrator of the scope the resource lives in -- because registering publishes the file's contents into a schema everyone granted the connection can read.
// @Description  A file that cannot be read as a table the way it is stored is refused with the problem type `urn:mcp-data-platform:problem:csv-needs-repair`; resubmitting with `repair: true` saves a corrected version of the file first and registers over that.
// @Tags         Tables
// @Accept       json
// @Produce      json
// @Param        id    path  string           true  "Managed resource ID"
// @Param        body  body  registerRequest  true  "Connection to register on, and the optional table name, repair and follow choices"
// @Success      201  {object}  registrationView
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/tables [post]
func (h *Handler) registerResourceTable(w http.ResponseWriter, r *http.Request) {
	h.registerTable(w, r, h.deps.Resources)
}

// unregisterResourceTable handles DELETE /api/v1/resources/{id}/tables/{regID}.
//
// @Summary      Drop a table registered over a managed resource
// @Description  Drops the external table and forgets the registration. The resource itself is untouched: dropping a Hive external table removes the metastore entry and leaves the stored objects. Only the person who registered the table or an administrator may remove it, and the record goes even when the DROP fails, so a table nobody can remove through the platform is never left behind.
// @Tags         Tables
// @Param        id     path  string  true  "Managed resource ID"
// @Param        regID  path  string  true  "Table registration ID"
// @Success      204
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/tables/{regID} [delete]
func (h *Handler) unregisterResourceTable(w http.ResponseWriter, r *http.Request) {
	h.unregisterTable(w, r, h.deps.Resources)
}

// listAssetTables handles GET /api/v1/portal/assets/{id}/tables.
//
// @Summary      List the tables registered over a portal asset
// @Description  Returns the registrations built over one portal asset under a `table_registrations` key, each with its fully qualified query name, a sample SELECT, and whether the table has fallen behind the file. Restricted to the asset's owner and to administrators -- an editor share does not carry it -- and a soft-deleted asset is answered as not found.
// @Tags         Tables
// @Produce      json
// @Param        id  path  string  true  "Portal asset ID"
// @Success      200  {object}  map[string][]registrationView
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/tables [get]
func (h *Handler) listAssetTables(w http.ResponseWriter, r *http.Request) {
	h.listTables(w, r, tableregister.KindAsset, h.deps.Assets)
}

// registerAssetTable handles POST /api/v1/portal/assets/{id}/tables.
//
// @Summary      Register a portal asset as a query-engine table
// @Description  Creates an external table over the asset's directory on the named connection, so the CSV stored there can be queried. Restricted to the asset's owner and to administrators -- an editor share does not carry it -- because registering publishes the file's contents into a schema everyone granted the connection can read.
// @Description  A file that cannot be read as a table the way it is stored is refused with the problem type `urn:mcp-data-platform:problem:csv-needs-repair`; resubmitting with `repair: true` saves a corrected version of the file first and registers over that.
// @Tags         Tables
// @Accept       json
// @Produce      json
// @Param        id    path  string           true  "Portal asset ID"
// @Param        body  body  registerRequest  true  "Connection to register on, and the optional table name, repair and follow choices"
// @Success      201  {object}  registrationView
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/tables [post]
func (h *Handler) registerAssetTable(w http.ResponseWriter, r *http.Request) {
	h.registerTable(w, r, h.deps.Assets)
}

// unregisterAssetTable handles DELETE /api/v1/portal/assets/{id}/tables/{regID}.
//
// @Summary      Drop a table registered over a portal asset
// @Description  Drops the external table and forgets the registration. The asset itself is untouched: dropping a Hive external table removes the metastore entry and leaves the stored objects. Only the person who registered the table or an administrator may remove it, and the record goes even when the DROP fails, so a table nobody can remove through the platform is never left behind.
// @Tags         Tables
// @Param        id     path  string  true  "Portal asset ID"
// @Param        regID  path  string  true  "Table registration ID"
// @Success      204
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/tables/{regID} [delete]
func (h *Handler) unregisterAssetTable(w http.ResponseWriter, r *http.Request) {
	h.unregisterTable(w, r, h.deps.Assets)
}

// listConnections handles GET /api/v1/table-connections.
//
// @Summary      List the connections a table can be registered on
// @Description  Returns, under a `connections` key, the connections this caller may register a new table onto: granted to their persona, carrying a scratch catalog and schema, and accepting writes. It is the register form's only source, so every connection offered here is one the register routes accept.
// @Description  It is deliberately narrower than the set a listing shows: a connection turned read-only after a registration still appears in `GET /tables` and no longer appears here. A deployment that cannot enumerate its connections answers an empty list rather than an error.
// @Tags         Tables
// @Produce      json
// @Success      200  {object}  map[string][]ConnectionChoice
// @Failure      401  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /table-connections [get]
func (h *Handler) listConnections(w http.ResponseWriter, r *http.Request) {
	user := portal.GetUser(r.Context())
	if user == nil {
		problem(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var choices []ConnectionChoice
	if h.deps.Connections != nil {
		choices = h.deps.Connections(r.Context(), user)
	}
	if choices == nil {
		choices = []ConnectionChoice{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": choices})
}

// resolve authenticates the caller and reads the record the route names.
//
// The caller is built before the record is resolved because the resolver
// decides authority from it, which is what lets the same resolver serve a tool
// call, where there is no portal user at all.
func (h *Handler) resolve(
	w http.ResponseWriter, r *http.Request, subject Subject,
) (caller tableregister.Caller, src tableregister.Source, ok bool) {
	user := portal.GetUser(r.Context())
	if user == nil {
		problem(w, http.StatusUnauthorized, "authentication required")
		return tableregister.Caller{}, tableregister.Source{}, false
	}
	caller = h.callerOf(user)
	src, ok = subject(r.Context(), r.PathValue("id"), caller)
	if !ok {
		problem(w, http.StatusNotFound, "no such file")
		return tableregister.Caller{}, tableregister.Source{}, false
	}
	return caller, src, true
}

// callerOf builds the registrar's view of the authenticated user.
func (h *Handler) callerOf(user *portal.User) tableregister.Caller {
	if h.deps.Caller != nil {
		return h.deps.Caller(user)
	}
	return tableregister.Caller{UserID: user.UserID, Email: user.Email, Roles: user.Roles}
}

// refuse answers a registrar error: the status that describes it, the detail a
// caller can act on, and -- for the two answers a surface does something about
// -- a problem type naming it. Either status can carry one: a file that needs
// correcting first is a 4xx, and a file that was corrected before the
// registration failed is a 4xx or a 5xx depending on what failed. action names
// what was being attempted, for the log line a platform failure leaves behind.
func refuse(w http.ResponseWriter, action string, err error) {
	status := statusFor(err)
	if status == http.StatusInternalServerError {
		slog.Warn(action+" failed", "error", logsan.SanitizeForLog(err.Error()))
	}
	if code := codeFor(err); code != "" {
		httpjson.WriteErrorCode(w, status, code, detailFor(err, status))
		return
	}
	problem(w, status, detailFor(err, status))
}

// codeFor names the problem for the two answers a surface does something
// specific about, or is empty for the refusals whose prose is the whole answer.
//
// The two are exclusive: a file that needs a correction has not had one, and a
// file that has had one is past the check that asks for it.
func codeFor(err error) string {
	switch {
	case errors.Is(err, tableregister.ErrNeedsRepair):
		return codeNeedsRepair
	case tableregister.RepairOf(err) != nil:
		return codeFileCorrected
	default:
		return ""
	}
}

// codeNeedsRepair is what the form matches on to offer the correction. The
// detail carries the sentence a person reads; a surface cannot key a control
// off prose, so the machine-readable half is here.
const codeNeedsRepair = "csv-needs-repair"

// codeFileCorrected says the file changed even though the registration did
// not: the correction was written and something after it failed. It is the
// signal a surface showing the file needs, because the version trail and the
// file's own record are now behind what is stored, and a client cannot be
// asked to find that out by reading the detail prose.
const codeFileCorrected = "file-corrected"

// statusFor maps a registrar refusal onto the status that describes it.
//
// A refusal the caller can act on -- a name already taken, a sibling object in
// the way, a file that is not a CSV -- is a 4xx: the request was understood and
// declined, and the message says what to do next. Anything the registrar did
// not mark as a refusal is a failure of the platform (a store outage, an
// unreachable coordinator) and is a 500, so a database that stopped answering
// is not reported to the caller as a conflict they could resolve.
func statusFor(err error) int {
	switch {
	case errors.Is(err, tableregister.ErrConnectionDenied):
		return http.StatusForbidden
	case errors.Is(err, tableregister.ErrNoIdentity):
		return http.StatusUnauthorized
	case errors.Is(err, tableregister.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, tableregister.ErrUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, tableregister.ErrNoScratchTarget),
		errors.Is(err, tableregister.ErrConnectionReadOnly),
		errors.Is(err, tableregister.ErrNotCSV),
		errors.Is(err, tableregister.ErrEmptyHeader):
		return http.StatusBadRequest
	case errors.Is(err, tableregister.ErrNameTaken), errors.Is(err, tableregister.ErrRefused):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// detailFor is what the caller is told. A refusal names what to do next and is
// passed through as written; a platform failure is not, because its text is a
// wrapped store or driver error that says nothing a caller can act on and may
// carry topology they should not see.
//
// What a platform failure does say is the stage it stopped at. "The
// registration could not be completed" on its own named neither a field, a
// reason nor a step, which left a person with nothing to retry differently and
// support with nothing to look for; the stage is the registrar's own word for
// where it was, and reading the file is a different thing to chase than
// running the statement (#1775). The whole error is in the log line and on the
// audit event either way.
//
// The other thing a platform failure carries through is a correction that
// preceded it: the file changed before the failure and stays changed after it,
// so a message about the table alone would leave its owner not knowing that.
func detailFor(err error, status int) string {
	if status != http.StatusInternalServerError {
		return err.Error()
	}
	stage := tableregister.StageOf(err)
	if repair := tableregister.RepairOf(err); repair != nil {
		if stage == "" {
			return repair.Summary() + " The table was not created; register it again."
		}
		// The colon introduces the reason, as it does on the same sentence the
		// registrar builds for the callers that are not this one.
		return repair.Summary() + " The table was not created: the registration stopped while " +
			stage + ". Register it again."
	}
	if stage == "" {
		return "the registration could not be completed"
	}
	return "the registration could not be completed while " + stage
}

// problem writes an RFC 9457 Problem Details response, the form every other
// seam in this decomposition answers with.
func problem(w http.ResponseWriter, status int, detail string) {
	httpjson.WriteError(w, status, detail)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	httpjson.WriteJSON(w, status, body)
}
