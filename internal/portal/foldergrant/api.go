package foldergrant

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// basePath is the collection route.
const basePath = "/api/v1/admin/resources/folder-grants"

// maxBodyBytes bounds a request body.
const maxBodyBytes = 8 << 10

// The tool names a change is audited under.
const (
	toolAdd    = "folder_grant_add"
	toolRemove = "folder_grant_remove"
)

// Deps is what the routes act through.
type Deps struct {
	Store *Store
	// Actor is the administrator's address; every request passed the admin
	// API's authentication.
	Actor func(*http.Request) string
	// PersonaExists reports whether a persona is defined.
	PersonaExists func(name string) bool
	// ExternalPersona reports whether a persona is external.
	ExternalPersona func(name string) bool
	// ExternalPerson reports whether the person at an address resolves to an
	// external persona now; nil reads nobody as one.
	ExternalPerson func(ctx context.Context, email string) bool
	// FolderExists reports whether a Global folder exists (stored, or holding
	// a file).
	FolderExists func(ctx context.Context, path string) (bool, error)
	// Sites names the sites serving a Global folder or one above it as media;
	// nil names none.
	Sites func(ctx context.Context, path string) ([]string, error)
	// Audit records every change; nil records none.
	Audit middleware.AuditLogger
}

// API is the admin routes.
type API struct{ d Deps }

// NewAPI returns the routes over d.
func NewAPI(d Deps) *API { return &API{d: d} }

// Register mounts the routes behind wrap, the admin API's authentication.
func (a *API) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	if a == nil || a.d.Store == nil || a.d.Actor == nil {
		return
	}
	mux.Handle("GET "+basePath, wrap(http.HandlerFunc(a.list)))
	mux.Handle("POST "+basePath, wrap(http.HandlerFunc(a.add)))
	mux.Handle("DELETE "+basePath+"/{id}", wrap(http.HandlerFunc(a.remove)))
}

// GrantList is the grants covering a Global folder.
type GrantList struct {
	Folder string  `json:"folder" example:"reports"`
	Grants []Grant `json:"grants"`
	// Sites are the sites serving the folder, or one above it, as media: a
	// grantee's write changes what they serve.
	Sites []string `json:"sites"`
}

// AddInput grants write on a Global folder to a person or a persona.
type AddInput struct {
	Folder  string `json:"folder" example:"reports"`
	Email   string `json:"email,omitempty" example:"jane@example.com"`
	Persona string `json:"persona,omitempty" example:"finance"`
}

// list handles GET /api/v1/admin/resources/folder-grants.
//
// @Summary      List the write grants on a Global folder
// @Description  Returns who can write the Global folder: the grants on it, and the grants on every folder above it marked inherited, with the sites that serve the folder as media; or, with persona, the grants that persona holds. Administrators only (#2684).
// @Tags         Resources
// @Produce      json
// @Param        folder   query  string  false  "The Global folder, such as reports/q3"
// @Param        persona  query  string  false  "In place of folder: the grants a persona holds, for its editor"
// @Success      200  {object}  GrantList
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/resources/folder-grants [get]
func (a *API) list(w http.ResponseWriter, r *http.Request) {
	if persona := strings.TrimSpace(r.URL.Query().Get("persona")); persona != "" {
		grants, err := a.d.Store.ByPersona(r.Context(), persona)
		if err != nil {
			httpjson.WriteError(w, http.StatusInternalServerError, "reading the persona's grants failed")
			return
		}
		httpjson.WriteJSON(w, http.StatusOK, GrantList{Grants: grants, Sites: []string{}})
		return
	}
	folder := strings.TrimSpace(r.URL.Query().Get("folder"))
	if err := checkFolder(folder); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	grants, err := a.d.Store.List(r.Context(), folder)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "reading the folder's grants failed")
		return
	}
	out := GrantList{Folder: folder, Grants: grants, Sites: []string{}}
	if a.d.Sites != nil {
		if sites, err := a.d.Sites(r.Context(), folder); err == nil && sites != nil {
			out.Sites = sites
		}
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// add handles POST /api/v1/admin/resources/folder-grants.
//
// @Summary      Grant write on a Global folder
// @Description  Grants a person (by address) or a persona write on a Global folder and everything beneath it: upload, new folders, new versions, rename, move inside what they can write, delete and share. The granted folder itself stays an administrator's to rename, move or delete. A person or persona that is external is refused, since an external persona never sees Global. Audited (#2684).
// @Tags         Resources
// @Accept       json
// @Produce      json
// @Param        body  body  AddInput  true  "The folder and the person or persona"
// @Success      201  {object}  Grant
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/resources/folder-grants [post]
func (a *API) add(w http.ResponseWriter, r *http.Request) {
	var in AddInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the body is not a JSON object")
		return
	}
	in.Folder, in.Email, in.Persona = strings.TrimSpace(in.Folder), strings.ToLower(strings.TrimSpace(in.Email)), strings.TrimSpace(in.Persona)
	params := map[string]any{"folder": in.Folder, "email": in.Email, "persona": in.Persona}
	if status, msg := a.refusal(r.Context(), in); status != 0 {
		a.record(r, toolAdd, params, errors.New(msg))
		httpjson.WriteError(w, status, msg)
		return
	}
	g := &Grant{Folder: in.Folder, Email: in.Email, Persona: in.Persona, CreatedBy: a.d.Actor(r)}
	err := a.d.Store.Add(r.Context(), g)
	a.record(r, toolAdd, params, err)
	switch {
	case errors.Is(err, ErrDuplicate):
		httpjson.WriteError(w, http.StatusConflict, err.Error())
	case err != nil:
		httpjson.WriteError(w, http.StatusInternalServerError, "writing the grant failed")
	default:
		httpjson.WriteJSON(w, http.StatusCreated, g)
	}
}

// refusal is why a grant cannot be added: status 0 when it can.
func (a *API) refusal(ctx context.Context, in AddInput) (status int, msg string) {
	if err := checkFolder(in.Folder); err != nil {
		return http.StatusBadRequest, err.Error()
	}
	if msg := a.granteeRefusal(ctx, in); msg != "" {
		return http.StatusBadRequest, msg
	}
	return a.folderRefusal(ctx, in.Folder)
}

// granteeRefusal is why the person or persona named cannot hold a grant, ""
// when they can.
func (a *API) granteeRefusal(ctx context.Context, in AddInput) string {
	switch {
	case (in.Email == "") == (in.Persona == ""):
		return "name the person (email) or the persona the grant is for, one of them"
	case in.Email != "":
		return a.personRefusal(ctx, in.Email)
	}
	return a.personaRefusal(in.Persona)
}

// personRefusal is why a person cannot hold a grant, "" when they can.
func (a *API) personRefusal(ctx context.Context, email string) string {
	switch {
	case !strings.Contains(email, "@"):
		return "email is not an address"
	case a.d.ExternalPerson != nil && a.d.ExternalPerson(ctx, email):
		return email + " resolves to an external persona, which never sees Global"
	}
	return ""
}

// personaRefusal is why a persona cannot hold a grant, "" when it can.
func (a *API) personaRefusal(name string) string {
	switch {
	case a.d.PersonaExists != nil && !a.d.PersonaExists(name):
		return "no persona is named " + name
	case a.d.ExternalPersona != nil && a.d.ExternalPersona(name):
		return "the " + name + " persona is external, and an external persona never sees Global"
	}
	return ""
}

// folderRefusal is why the folder cannot take a grant: it does not exist.
func (a *API) folderRefusal(ctx context.Context, folder string) (status int, msg string) {
	if a.d.FolderExists == nil {
		return 0, ""
	}
	ok, err := a.d.FolderExists(ctx, folder)
	if err != nil {
		return http.StatusInternalServerError, "reading the folder failed"
	}
	if !ok {
		return http.StatusNotFound, "Global has no folder " + folder
	}
	return 0, ""
}

// remove handles DELETE /api/v1/admin/resources/folder-grants/{id}.
//
// @Summary      Remove a write grant on a Global folder
// @Description  Ends the grant: the person or persona's next write under the folder is refused, on every replica. Audited (#2684).
// @Tags         Resources
// @Produce      json
// @Param        id  path  string  true  "The grant"
// @Success      200  {object}  Grant
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/resources/folder-grants/{id} [delete]
func (a *API) remove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g, err := a.d.Store.Remove(r.Context(), id)
	params := map[string]any{"id": id}
	if g != nil {
		params["folder"], params["email"], params["persona"] = g.Folder, g.Email, g.Persona
	}
	a.record(r, toolRemove, params, err)
	switch {
	case errors.Is(err, ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, err.Error())
	case err != nil:
		httpjson.WriteError(w, http.StatusInternalServerError, "removing the grant failed")
	default:
		httpjson.WriteJSON(w, http.StatusOK, g)
	}
}

// checkFolder refuses a folder a grant cannot name: Global's root, which only
// administrators write, and a path the resource rules refuse.
func checkFolder(folder string) error {
	if folder == "" {
		return errors.New("folder is required: a grant is on one Global folder, never the whole library")
	}
	if err := resource.ValidatePath(folder); err != nil {
		return err //nolint:wrapcheck // the path rule's own sentence
	}
	return nil
}

// record audits one change. Best-effort: a failed write is warned and the
// change stands.
func (a *API) record(r *http.Request, tool string, params map[string]any, opErr error) {
	if a.d.Audit == nil {
		return
	}
	who := a.d.Actor(r)
	ev := middleware.AuditEvent{
		ID: audit.NewEventID(), Timestamp: time.Now().UTC(), ToolName: tool, ToolkitKind: "resources",
		Source: "admin", Transport: "http", EventKind: string(audit.EventTypeAdmin),
		Authorized: true, Success: opErr == nil, Parameters: params, UserID: who, UserEmail: who,
	}
	if opErr != nil {
		ev.ErrorMessage = opErr.Error()
	}
	if err := a.d.Audit.Log(r.Context(), ev); err != nil {
		slog.Warn("folder grant audit failed", "tool", tool, "error", logsan.SanitizeForLog(err.Error()))
	}
}
