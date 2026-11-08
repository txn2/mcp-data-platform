// Package apphttp serves applications on the portal (#2411, #2412): the
// listing, an application's parts, releases and what each changed, and the
// actions the manage_application tool takes, through the same service and
// under the same rules. Everyone signed in reads; an application's owner and
// administrators edit it; administrators release, roll back, export and
// import. Every write is audited as a manage_application call from the
// portal.
package apphttp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appbundle"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
	"github.com/txn2/mcp-data-platform/internal/application/apptool"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// Prefix opens every route.
const Prefix = "/api/v1/portal/applications"

// pathApp is the application path parameter, and keyApplication the audit
// parameter naming it.
const (
	pathApp        = "app"
	keyApplication = "application"
)

// sourcePortal is the audit source of a portal write.
const sourcePortal = "portal"

// maxBody bounds a request body.
const maxBody = 1 << 20

// Config is what the routes use.
type Config struct {
	Service *appsvc.Service
	// Bundles exports and imports; nil refuses both. The portal downloads a
	// bundle and uploads one, where the tool writes and reads a managed
	// resource.
	Bundles Bundles
	// Actor is who made a request, nil for none.
	Actor func(r *http.Request) *application.Actor
	Audit middleware.AuditLogger
	// Databases lists the application database connections a request's
	// caller reaches and their schemas (#2491); nil leaves the route
	// unmounted.
	Databases func(r *http.Request, actor application.Actor) []DatabaseChoice
	// ViewAsset reports whether a request's caller opens an asset, which an
	// asset part's name is told only to (#2527); nil tells it to no one but
	// administrators.
	ViewAsset appsvc.AssetViewer
}

// DatabasesPath is the route the New application dialog fills its
// connection and schema choices from. It is beside Prefix, not under it, so
// no application's name is shadowed by it.
const DatabasesPath = "/api/v1/portal/application-databases"

// DatabaseChoice is one application database connection the caller reaches,
// with the schemas it holds.
type DatabaseChoice struct {
	Connection  string         `json:"connection" example:"orders"`
	Description string         `json:"description,omitempty"`
	Schemas     []SchemaChoice `json:"schemas"`
	// Problem says the schemas could not be read, empty when they were. It
	// never repeats the database's error, which can name its address.
	Problem string `json:"problem,omitempty"`
}

// SchemaChoice is one schema of a connection, and the application holding
// it, if any: a schema is held by one application.
type SchemaChoice struct {
	Name   string `json:"name" example:"shop"`
	HeldBy string `json:"held_by,omitempty" example:"shop"`
}

// databasesResponse is the dialog's choices.
type databasesResponse struct {
	Connections []DatabaseChoice `json:"connections"`
}

// Bundles assembles a release's bundle and imports one.
type Bundles interface {
	Bundle(ctx context.Context, actor application.Actor, ref string, release int) (*appbundle.Bundle, error)
	ImportBundle(ctx context.Context, actor application.Actor, bundle *appbundle.Bundle, mapping map[string]string) (*appbundle.Imported, error)
}

type handler struct{ cfg Config }

// Register mounts the routes behind wrap, the portal's authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.Service == nil || cfg.Actor == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.Handle("GET "+Prefix, wrap(http.HandlerFunc(h.list)))
	mux.Handle("POST "+Prefix, wrap(http.HandlerFunc(h.create)))
	mux.Handle("POST "+Prefix+"/import", wrap(http.HandlerFunc(h.importBundle)))
	mux.Handle("GET "+Prefix+"/{app}", wrap(http.HandlerFunc(h.get)))
	mux.Handle("PATCH "+Prefix+"/{app}", wrap(http.HandlerFunc(h.update)))
	mux.Handle("DELETE "+Prefix+"/{app}", wrap(http.HandlerFunc(h.remove)))
	mux.Handle("POST "+Prefix+"/{app}/parts", wrap(http.HandlerFunc(h.addPart)))
	mux.Handle("GET "+Prefix+"/{app}/candidates", wrap(http.HandlerFunc(h.candidates)))
	mux.Handle("DELETE "+Prefix+"/{app}/parts/{kind}/{id}", wrap(http.HandlerFunc(h.removePart)))
	mux.Handle("POST "+Prefix+"/{app}/release", wrap(http.HandlerFunc(h.release)))
	mux.Handle("POST "+Prefix+"/{app}/rollback", wrap(http.HandlerFunc(h.rollback)))
	mux.Handle("GET "+Prefix+"/{app}/export", wrap(http.HandlerFunc(h.export)))
	mux.Handle("GET "+Prefix+"/{app}/diagram", wrap(http.HandlerFunc(h.diagram)))
	if cfg.Databases != nil {
		mux.Handle("GET "+DatabasesPath, wrap(http.HandlerFunc(h.databases)))
	}
}

// importRequest is an uploaded bundle and the names it is mapped with.
type importRequest struct {
	Bundle *appbundle.Bundle `json:"bundle"`
	Map    map[string]string `json:"map"`
}

func (h *handler) actor(w http.ResponseWriter, r *http.Request) (application.Actor, bool) {
	a := h.cfg.Actor(r)
	if a == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, "sign in to use applications")
		return application.Actor{}, false
	}
	return *a, true
}

func decode[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var req T
	if r.Body == nil || r.ContentLength == 0 {
		return req, true
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body")
		return req, false
	}
	return req, true
}

// status is the HTTP status an error answers with.
func status(err error) int {
	var refused *appsvc.RefusedError
	switch {
	case errors.Is(err, application.ErrNotFound), errors.Is(err, application.ErrNoRelease), errors.Is(err, application.ErrNoPart):
		return http.StatusNotFound
	case errors.Is(err, application.ErrNotAllowed):
		return http.StatusForbidden
	case errors.Is(err, application.ErrNameTaken), errors.Is(err, application.ErrSchemaHeld),
		errors.Is(err, application.ErrPartTaken), errors.Is(err, application.ErrStale):
		return http.StatusConflict
	case errors.As(err, &refused):
		return http.StatusUnprocessableEntity
	}
	return http.StatusInternalServerError
}

func (*handler) answer(w http.ResponseWriter, v any, err error) {
	if err != nil {
		code := status(err)
		msg := err.Error()
		if code == http.StatusInternalServerError {
			slog.Warn("application request failed", "error", logsan.SanitizeForLog(msg))
		}
		httpjson.WriteError(w, code, msg)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, v)
}

// record audits one write as a manage_application call from the portal.
// Best-effort: a failed write is warned and the request stands.
func (h *handler) record(r *http.Request, actor application.Actor, command string, params map[string]any, opErr error) {
	if h.cfg.Audit == nil {
		return
	}
	if params == nil {
		params = map[string]any{}
	}
	params["command"] = command
	ev := middleware.AuditEvent{
		ID: audit.NewEventID(), Timestamp: time.Now().UTC(), ToolName: apptool.ToolName,
		Source: sourcePortal, Transport: "http", Authorized: true, Success: opErr == nil, Parameters: params,
		UserID: actor.Email, UserEmail: actor.Email,
	}
	if opErr != nil {
		ev.ErrorMessage = opErr.Error()
	}
	if err := h.cfg.Audit.Log(r.Context(), ev); err != nil {
		slog.Warn("application action audit failed", "command", command, "error", logsan.SanitizeForLog(err.Error()))
	}
}

// list answers every application.
//
// @Summary      List applications
// @Description  Every application on the deployment, by name, without parts. Readable by everyone signed in.
// @Success      200  {object}  listResponse
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications [get]
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	apps, err := h.cfg.Service.Store.List(r.Context())
	h.answer(w, listResponse{Applications: apps, Total: len(apps)}, err)
}

// create makes an application owned by the caller.
//
// @Summary      Create an application
// @Description  Creates an application owned by the caller. connection and schema name the application database schema whose migrations a release applies; both or neither.
// @Accept       json
// @Param        body  body  createRequest  true  "The application"
// @Success      200  {object}  application.Application
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications [post]
func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	req, ok2 := decode[createRequest](w, r)
	if !ok || !ok2 {
		return
	}
	a, err := h.cfg.Service.Create(r.Context(), actor, application.Application{
		Name: req.Name, Description: req.Description, Connection: req.Connection, Schema: req.Schema,
	})
	h.record(r, actor, apptool.CmdCreate, map[string]any{"name": req.Name}, err)
	h.answer(w, a, err)
}

// databases answers the connection and schema choices of a new application.
//
// @Summary      List the databases an application may hold a schema of
// @Description  The application database connections (kind appdb) the caller's persona reaches, each with its schemas and the application holding each, if any. The New application dialog chooses from it; a schema another application holds cannot be chosen.
// @Success      200  {object}  databasesResponse
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/application-databases [get]
func (h *handler) databases(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	choices := h.cfg.Databases(r, actor)
	if choices == nil {
		choices = []DatabaseChoice{}
	}
	httpjson.WriteJSON(w, http.StatusOK, databasesResponse{Connections: choices})
}

// listResponse is every application.
type listResponse struct {
	Applications []application.Application `json:"applications"`
	Total        int                       `json:"total"`
}

// createRequest, updateRequest, partRequest, releaseRequest and
// rollbackRequest are the documented forms of request.
type (
	createRequest struct {
		Name        string `json:"name" example:"shop"`
		Description string `json:"description" example:"The storefront, its API and its orders"`
		Connection  string `json:"connection" example:"orders"`
		Schema      string `json:"schema" example:"shop"`
	}
	updateRequest struct {
		Description string `json:"description"`
		OwnerEmail  string `json:"owner_email"`
	}
	partRequest struct {
		Kind string `json:"kind" example:"script"`
		ID   string `json:"id" example:"5b0c2f3e-0000-0000-0000-000000000000"`
	}
	releaseRequest struct {
		Note string `json:"note"`
	}
	rollbackRequest struct {
		ToRelease int `json:"to_release" example:"2"`
	}
)

// detail is an application's page: where it stands, each part as the
// caller reads it, and its releases.
type detail struct {
	*appsvc.Status
	// Parts is each part with its name and the facts its row describes,
	// in the order application.parts lists them (#2527).
	Parts    []appsvc.PartView     `json:"parts"`
	Releases []application.Release `json:"releases"`
}

// get answers an application's page.
//
// @Summary      Read an application
// @Description  An application by name or id: its parts (parts names each one, the facts its row is described with, its newest version and its state: ok, deleted, or not_visible for an asset the caller cannot open, whose name is not told), its current release, what a release now would change (pending), its schema part (schema: its migration version, the version the current release recorded, the pending migrations, and the role its scripts log in as), the parts a release would refuse (problems), and its releases newest first with what each changed.
// @Param        app  path  string  true  "Application name or id"
// @Success      200  {object}  detail
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app} [get]
func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	st, err := h.cfg.Service.StatusOf(r.Context(), r.PathValue(pathApp))
	if err != nil {
		h.answer(w, nil, err)
		return
	}
	parts := h.cfg.Service.Describe(r.Context(), actor, st.Application.Parts, h.cfg.ViewAsset)
	rels, err := h.cfg.Service.Store.Releases(r.Context(), st.Application.ID)
	h.answer(w, detail{Status: st, Parts: parts, Releases: rels}, err)
}

// candidates answers the parts the caller may add to an application.
//
// @Summary      Search parts to add to an application
// @Description  The scripts and assets the caller owns (every one for an administrator) and, for an administrator, the webhook sources, whose name contains q, at most 50 of each kind. A part already in this application is left out; one in another application carries that application, and adding it is refused. kind (script, asset, source) narrows the list; counts are for every kind. For the application's owner and administrators.
// @Param        app   path   string  true   "Application name or id"
// @Param        q     query  string  false  "Text the name contains"
// @Param        kind  query  string  false  "script, asset or source"
// @Success      200  {object}  appsvc.Candidates
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app}/candidates [get]
func (h *handler) candidates(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind != "" && !application.ValidKind(kind) {
		h.answer(w, nil, &appsvc.RefusedError{Msg: "kind: one of script, asset, source"})
		return
	}
	out, err := h.cfg.Service.FindCandidates(r.Context(), actor, r.PathValue(pathApp), r.URL.Query().Get("q"), kind)
	h.answer(w, out, err)
}

// update writes the description and, for an administrator, the owner.
//
// @Summary      Update an application
// @Description  Writes the description. owner_email moves the application to another owner and is for administrators.
// @Accept       json
// @Param        app   path  string         true  "Application name or id"
// @Param        body  body  updateRequest  true  "What to change"
// @Success      200  {object}  application.Application
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app} [patch]
func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	req, ok2 := decode[updateRequest](w, r)
	if !ok || !ok2 {
		return
	}
	a, err := h.cfg.Service.Update(r.Context(), actor, r.PathValue(pathApp), req.Description, req.OwnerEmail)
	h.record(r, actor, apptool.CmdUpdate, map[string]any{keyApplication: r.PathValue(pathApp)}, err)
	h.answer(w, a, err)
}

// remove deletes an application.
//
// @Summary      Delete an application
// @Description  Deletes the application and its releases. Its parts stay, and run and serve their newest versions again. For the owner and administrators.
// @Param        app  path  string  true  "Application name or id"
// @Success      200  {object}  map[string]string
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app} [delete]
func (h *handler) remove(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	err := h.cfg.Service.Delete(r.Context(), actor, r.PathValue(pathApp))
	h.record(r, actor, apptool.CmdDelete, map[string]any{keyApplication: r.PathValue(pathApp)}, err)
	h.answer(w, map[string]any{"deleted": r.PathValue(pathApp)}, err)
}

// addPart adds a part.
//
// @Summary      Add a part to an application
// @Description  Adds a script (by id), an asset (by id), a webhook source (by name), or the application database schema (kind schema, by "<connection>/<schema>", or the schema alone when the caller reaches one application database connection). A part belongs to at most one application; the caller must own the part (a webhook source is added by an administrator). A schema is on an existing application database connection the caller's persona reaches; an application holds one schema, and adding it creates the database role the application's scripts log in as, which needs CREATEROLE on the connection's user. A script that reads or writes a schema another application holds is refused.
// @Accept       json
// @Param        app   path  string       true  "Application name or id"
// @Param        body  body  partRequest  true  "The part"
// @Success      200  {object}  application.Application
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app}/parts [post]
func (h *handler) addPart(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	req, ok2 := decode[partRequest](w, r)
	if !ok || !ok2 {
		return
	}
	a, err := h.cfg.Service.AddPart(r.Context(), actor, r.PathValue(pathApp), req.Kind, req.ID)
	h.record(r, actor, apptool.CmdAddPart, map[string]any{keyApplication: r.PathValue(pathApp), "kind": req.Kind, "part_id": req.ID}, err)
	h.answer(w, a, err)
}

// removePart removes a part.
//
// @Summary      Remove a part from an application
// @Description  Removes the part; it runs or serves its newest version again. Removing the schema drops the database role the application's scripts log in as and leaves the schema and its data; on a released application it is for administrators.
// @Param        app   path  string  true  "Application name or id"
// @Param        kind  path  string  true  "script, asset, source or schema"
// @Param        id    path  string  true  "The part's id, a webhook source's name, or the schema (\"<connection>/<schema>\" escaped, or the schema alone)"
// @Success      200  {object}  application.Application
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app}/parts/{kind}/{id} [delete]
func (h *handler) removePart(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	kind, id := r.PathValue("kind"), r.PathValue("id")
	a, err := h.cfg.Service.RemovePart(r.Context(), actor, r.PathValue(pathApp), kind, id)
	h.record(r, actor, apptool.CmdRemovePart, map[string]any{keyApplication: r.PathValue(pathApp), "kind": kind, "part_id": id}, err)
	h.answer(w, a, err)
}

// release releases an application.
//
// @Summary      Release an application
// @Description  Applies the pending migrations and pins each script's and asset's newest version; from then on endpoints, schedules, events and runs execute the pinned script version and the portal serves the pinned asset version. A part that cannot be pinned or a migration that fails refuses the release and changes nothing. For administrators.
// @Accept       json
// @Param        app   path  string          true   "Application name or id"
// @Param        body  body  releaseRequest  false  "A note on the release"
// @Success      200  {object}  application.Release
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app}/release [post]
func (h *handler) release(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	req, ok2 := decode[releaseRequest](w, r)
	if !ok || !ok2 {
		return
	}
	rel, err := h.cfg.Service.Release(r.Context(), actor, r.PathValue(pathApp), req.Note)
	h.record(r, actor, apptool.CmdRelease, map[string]any{keyApplication: r.PathValue(pathApp)}, err)
	h.answer(w, rel, err)
}

// rollback restores an earlier release's pins.
//
// @Summary      Roll an application back
// @Description  Restores the pins of to_release, or of the release before the current one, and reverts the migrations applied since when each has a down migration; the rollback records which it reverted or why it reverted none. For administrators.
// @Accept       json
// @Param        app   path  string           true   "Application name or id"
// @Param        body  body  rollbackRequest  false  "The release to restore"
// @Success      200  {object}  application.Release
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app}/rollback [post]
func (h *handler) rollback(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	req, ok2 := decode[rollbackRequest](w, r)
	if !ok || !ok2 {
		return
	}
	rel, err := h.cfg.Service.Rollback(r.Context(), actor, r.PathValue(pathApp), req.ToRelease)
	h.record(r, actor, apptool.CmdRollback, map[string]any{keyApplication: r.PathValue(pathApp), "to_release": req.ToRelease}, err)
	h.answer(w, rel, err)
}

var errNoBundles = &appsvc.RefusedError{Msg: "this deployment cannot export or import applications: it has no managed resources"}

// export answers a release's bundle as a download (?release=, the current
// release when absent).
//
// @Summary      Export an application release
// @Description  Answers one release as a bundle to download: its migrations, script versions with their endpoint bindings and schedules, and asset versions, with the connections, secrets, webhook sources and bot checks they use named. A bundle carries no secret value, credential, data row, script state or run history. For administrators.
// @Param        app      path   string   true   "Application name or id"
// @Param        release  query  integer  false  "The release; the current one when absent"
// @Success      200  {object}  appbundle.Bundle
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app}/export [get]
func (h *handler) export(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	if h.cfg.Bundles == nil {
		h.answer(w, nil, errNoBundles)
		return
	}
	release, _ := strconv.Atoi(r.URL.Query().Get("release"))
	bundle, err := h.cfg.Bundles.Bundle(r.Context(), actor, r.PathValue(pathApp), release)
	h.record(r, actor, apptool.CmdExport, map[string]any{keyApplication: r.PathValue(pathApp), "release": release}, err)
	if err != nil {
		h.answer(w, nil, err)
		return
	}
	name := bundle.Application.Name + "-release-" + strconv.Itoa(bundle.Release) + ".json"
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	httpjson.WriteJSON(w, http.StatusOK, bundle)
}

// importBundle imports an uploaded bundle. The audit row names the
// application and the names it was mapped with, never the bundle's content.
//
// @Summary      Import an application bundle
// @Description  Imports an exported bundle: every connection, secret, webhook source and bot check it names must be mapped to one on this deployment (map), or nothing is written. Creates the application, or finds the one with its name, writes each part owned by the caller, and releases it. For administrators.
// @Accept       json
// @Param        body  body  importRequest  true  "The bundle and the names it is mapped with"
// @Success      200  {object}  appbundle.Imported
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/import [post]
func (h *handler) importBundle(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var req importRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, appbundle.MaxBytes)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body: send {\"bundle\": <the exported bundle>, \"map\": {...}}")
		return
	}
	if h.cfg.Bundles == nil {
		h.answer(w, nil, errNoBundles)
		return
	}
	out, err := h.cfg.Bundles.ImportBundle(r.Context(), actor, req.Bundle, req.Map)
	params := map[string]any{"map": req.Map}
	if req.Bundle != nil {
		params["application"] = req.Bundle.Application.Name
		params["release"] = req.Bundle.Release
	}
	h.record(r, actor, apptool.CmdImport, params, err)
	h.answer(w, out, err)
}
