package eppreview

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// relayedHeaders are the headers of the form runner's answer a preview passes
// on: its content type, and when to send a submission again.
var relayedHeaders = []string{"Content-Type", "Retry-After"}

// maxBody bounds a preview's submission, the default a form's endpoint takes.
const maxBody = 10 << 20

// Scripts reads the script a preview renders.
type Scripts interface {
	GetByID(ctx context.Context, id string) (*script.Script, error)
	GetVersion(ctx context.Context, scriptID string, version int) (*script.Version, error)
}

// Drafts runs a draft (internal/platform/scriptdraft).
type Drafts interface {
	Run(ctx context.Context, req scriptdraft.Request) (*scriptdraft.Outcome, error)
}

// Handler answers a preview's spec and submissions at APIPrefix+{id}.
type Handler struct {
	Store   *Store
	Scripts Scripts
	Runner  *eprun.Runner
	Drafts  Drafts
	// ViewerKey opens the viewer token the preview's page signed for the
	// person who opened it (epform.ViewerKey).
	ViewerKey []byte
}

// Mount registers the routes on mux.
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET "+APIPrefix+"{id}", h.serve)
	mux.HandleFunc("POST "+APIPrefix+"{id}", h.serve)
}

// serve answers one request to a preview, for the person who opened it only.
func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, refusal := h.resolve(r)
	if refusal != nil {
		writeError(w, refusal.status, refusal.msg)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "the submission is larger than a form takes")
		return
	}
	caller := &eprun.Caller{UserID: p.rec.UserID, Email: p.rec.CreatedBy, AuthType: p.rec.AuthType}
	resp, failure := h.Runner.ServePreview(ctx, eprun.Preview{
		ID: p.rec.ID, Script: p.sc, Version: p.rec.Version, Source: p.source, Exec: h.exec(p.rec, p.sc, p.source),
	}, eprun.Request{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Header: r.Header.Clone(), Body: body, Caller: caller,
	})
	if failure != nil {
		slog.InfoContext(ctx, "form preview: the form failed", "script_id", p.sc.ID, "error", logsan.SanitizeForLog(failure.Error()))
	}
	// A preview's answer is the form runner's own JSON; only the headers it
	// means for the browser are passed on.
	for _, k := range relayedHeaders {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)
}

// resolved is the preview a request names: its record, script and source.
type resolved struct {
	rec    *Record
	sc     *script.Script
	source string
}

// refusal is the answer a request that may not reach a preview gets.
type refusal struct {
	status int
	msg    string
}

// resolve reads the preview a request names, refusing anyone but the person
// who opened it.
func (h *Handler) resolve(r *http.Request) (*resolved, *refusal) {
	ctx := r.Context()
	id := r.PathValue("id")
	rec, err := h.Store.Get(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			slog.ErrorContext(ctx, "form preview: reading the preview failed", "error", logsan.SanitizeForLog(err.Error()))
		}
		return nil, &refusal{http.StatusNotFound, "this preview does not exist or has expired; open a new one from the script's page"}
	}
	viewer, err := epform.OpenViewer(h.ViewerKey, eprun.PreviewPrefix+id, r.Header.Get(epform.ViewerHeader), time.Now())
	if err != nil || viewer.Email == "" || viewer.Email != rec.CreatedBy {
		return nil, &refusal{http.StatusForbidden, "this preview belongs to the person who opened it"}
	}
	sc, err := h.Scripts.GetByID(ctx, rec.ScriptID)
	if err != nil || sc == nil {
		return nil, &refusal{http.StatusNotFound, "the preview's script no longer exists"}
	}
	source, ok := h.source(ctx, rec, sc)
	if !ok {
		return nil, &refusal{http.StatusNotFound, "the preview's version no longer exists"}
	}
	return &resolved{rec: rec, sc: sc, source: source}, nil
}

// source is what a preview renders: its unsaved source, or its saved version.
func (h *Handler) source(ctx context.Context, rec *Record, sc *script.Script) (string, bool) {
	if rec.Source != "" {
		return rec.Source, true
	}
	v, err := h.Scripts.GetVersion(ctx, sc.ID, rec.Version)
	if err != nil || v == nil {
		return "", false
	}
	return v.Source, true
}

// exec runs one of the preview's functions as a draft under the identity the
// preview was opened with.
func (h *Handler) exec(rec *Record, sc *script.Script, source string) eprun.Executor {
	return func(ctx context.Context, name string, args starlark.Tuple) (starlark.Value, error) {
		var returned starlark.Value = starlark.None
		out, err := h.Drafts.Run(ctx, scriptdraft.Request{
			Source: source, Name: sc.Name, Script: sc,
			Identity: scriptdraft.Identity{UserID: rec.UserID, Email: rec.CreatedBy, Roles: rec.Roles, AuthType: rec.AuthType},
			Handler: &scriptdialect.Handler{Name: name, Args: args, Returned: func(v starlark.Value) error {
				returned = v
				return nil
			}},
		})
		if err != nil {
			return nil, err //nolint:wrapcheck // the draft's own refusal (busy, no identity) is what the author reads
		}
		if out.Err != nil {
			return nil, out.Err
		}
		return returned, nil
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = wirejson.Encode(w, map[string]string{"error": msg})
}

// PortalPath is the route the script page opens a preview through.
const PortalPath = "/api/v1/portal/scripts/{id}/form-preview"

// Portal opens previews from the portal's script page.
type Portal struct {
	Previews *Previews
	Scripts  interface {
		GetByID(ctx context.Context, id string) (*script.Script, error)
	}
	// Opener is who made a request, nil for none.
	Opener func(r *http.Request) *Opener
}

// openRequest is the documented body: the saved version, or the unsaved
// source, to preview.
type openRequest struct {
	Version int    `json:"version,omitempty" example:"2"`
	Source  string `json:"source,omitempty"`
}

// Register mounts the route behind wrap, the portal's authentication.
func (p *Portal) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	if p == nil || p.Previews == nil || p.Opener == nil {
		return
	}
	mux.Handle("POST "+PortalPath, wrap(http.HandlerFunc(p.open)))
}

// open opens a preview of a script's form.
//
// @Summary      Open a preview of a script's form
// @Description  Opens a preview of the script's form as /forms/<endpoint> renders it, from a saved version (the latest when omitted) or the unsaved source sent, with no endpoint bound. The address opens for the caller only and expires within the hour; a submission there runs form_submit as a draft and stores nothing. Restricted to the script's owner and administrators.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id    path  string       true  "Script ID"
// @Param        body  body  openRequest  true  "What to preview"
// @Success      200  {object}  Opened
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/form-preview [post]
func (p *Portal) open(w http.ResponseWriter, r *http.Request) {
	o := p.Opener(r)
	if o == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, "sign in to preview a form")
		return
	}
	var in openRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the request body is not the JSON this route takes")
		return
	}
	sc, err := p.Scripts.GetByID(r.Context(), r.PathValue("id"))
	if err != nil || sc == nil {
		httpjson.WriteError(w, http.StatusNotFound, "script not found")
		return
	}
	opened, err := p.Previews.Open(r.Context(), *o, sc, in.Version, in.Source)
	var refused *RefusedError
	switch {
	case errors.Is(err, ErrNotAllowed):
		httpjson.WriteError(w, http.StatusForbidden, err.Error())
	case errors.As(err, &refused):
		httpjson.WriteError(w, http.StatusUnprocessableEntity, refused.Msg)
	case err != nil:
		slog.ErrorContext(r.Context(), "form preview: opening the preview failed", "error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "the preview could not be opened")
	default:
		httpjson.WriteJSON(w, http.StatusOK, opened)
	}
}
