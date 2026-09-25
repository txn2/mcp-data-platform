// Package webhookapi is the admin REST surface for inbound webhook sources
// (#1870): list, create, read with status, change, and delete. Secrets are
// write-only: a view says whether one is set and until when the previous one
// is still accepted, and never carries either.
package webhookapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/webhook/whadmin"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
)

// sourcesPath is the collection route.
const sourcesPath = "/api/v1/admin/webhooks/sources"

// maxBodyBytes bounds a request body. A source is a few hundred bytes of
// settings.
const maxBodyBytes = 64 << 10

// Service is what the routes act through. whadmin.Service satisfies it.
type Service interface {
	List(ctx context.Context) ([]whsource.Source, error)
	Get(ctx context.Context, name string) (whsource.Source, whstore.Status, error)
	Create(ctx context.Context, src whsource.Source) (whsource.Source, error)
	Update(ctx context.Context, name string, u whadmin.Update) (whsource.Source, error)
	Delete(ctx context.Context, name string) error
}

// Config carries the service and the parent-owned helpers.
type Config struct {
	// Service manages sources. nil mounts nothing.
	Service Service
	// Author resolves the acting admin.
	Author func(*http.Request) string
}

type handler struct{ cfg Config }

// Register mounts the webhook source routes, each behind wrap, the admin
// API's authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.Service == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.Handle("GET "+sourcesPath, wrap(http.HandlerFunc(h.list)))
	mux.Handle("POST "+sourcesPath, wrap(http.HandlerFunc(h.create)))
	mux.Handle("GET "+sourcesPath+"/{name}", wrap(http.HandlerFunc(h.get)))
	mux.Handle("PUT "+sourcesPath+"/{name}", wrap(http.HandlerFunc(h.update)))
	mux.Handle("DELETE "+sourcesPath+"/{name}", wrap(http.HandlerFunc(h.remove)))
}

// decode reads a JSON body, refusing unknown fields and trailing data.
func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("the body is not a valid source: %w", err)
	}
	if dec.More() {
		return errors.New("the body holds more than one JSON value")
	}
	return nil
}

// list handles GET /api/v1/admin/webhooks/sources.
//
// @Summary      List webhook sources
// @Description  Returns every inbound webhook source. Secrets are never returned.
// @Tags         Webhooks
// @Produce      json
// @Success      200  {object}  SourceList
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/webhooks/sources [get]
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.cfg.Service.List(r.Context())
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "reading webhook sources failed")
		return
	}
	out := SourceList{Sources: make([]SourceView, 0, len(list))}
	for _, s := range list {
		out.Sources = append(out.Sources, viewOf(s))
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// get handles GET /api/v1/admin/webhooks/sources/{name}.
//
// @Summary      Get a webhook source
// @Description  Returns one source and its status: request counts by outcome for the last hour and day, when it last received an event, the last compacted hour, hours owed a compaction, the oldest hour held, and the last 50 rejected requests (never their bodies).
// @Tags         Webhooks
// @Produce      json
// @Param        name  path  string  true  "Source name"
// @Success      200  {object}  SourceDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/webhooks/sources/{name} [get]
func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	src, st, err := h.cfg.Service.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, SourceDetail{Source: viewOf(src), Status: st})
}

// create handles POST /api/v1/admin/webhooks/sources.
//
// @Summary      Create a webhook source
// @Description  Creates a source, its tables and its view on the named connection. The connection's scratch catalog must read the managed-resources bucket and allow register_partition; the source is refused, with the reason, when it does not.
// @Tags         Webhooks
// @Accept       json
// @Produce      json
// @Param        body  body  SourceInput  true  "The source"
// @Success      201  {object}  SourceView
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/webhooks/sources [post]
func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	var in SourceInput
	if err := decode(w, r, &in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	enabled := in.Enabled == nil || *in.Enabled
	src, err := h.cfg.Service.Create(r.Context(), whsource.Source{
		Name: in.Name, Enabled: enabled, Connection: in.Connection,
		Auth: authOf(in.Auth), Config: in.Config, CreatedBy: h.cfg.Author(r),
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusCreated, viewOf(src))
}

// update handles PUT /api/v1/admin/webhooks/sources/{name}.
//
// @Summary      Change a webhook source
// @Description  Replaces a source's auth and config. An empty auth.secret keeps the stored secret; a new one rotates it, keeping the previous one valid for rotation_overlap_seconds. The name and connection cannot change.
// @Tags         Webhooks
// @Accept       json
// @Produce      json
// @Param        name  path  string       true  "Source name"
// @Param        body  body  SourceInput  true  "The source"
// @Success      200  {object}  SourceView
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/webhooks/sources/{name} [put]
func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	var in SourceInput
	if err := decode(w, r, &in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	src, err := h.cfg.Service.Update(r.Context(), r.PathValue("name"), whadmin.Update{
		Enabled: in.Enabled, Auth: authOf(in.Auth), Config: in.Config,
		RotationOverlap: time.Duration(in.RotationOverlapSeconds) * time.Second,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, viewOf(src))
}

// remove handles DELETE /api/v1/admin/webhooks/sources/{name}.
//
// @Summary      Delete a webhook source
// @Description  Deletes a source and everything it landed: its tables and view, every compacted window's resource, every raw segment, and its request history.
// @Tags         Webhooks
// @Param        name  path  string  true  "Source name"
// @Success      204
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/webhooks/sources/{name} [delete]
func (h *handler) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.cfg.Service.Delete(r.Context(), r.PathValue("name")); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeServiceError maps the service's refusals to their status. A refusal's
// text names what to change and is returned as written; any other failure is
// not.
func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, whadmin.ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "webhook source not found")
	case errors.Is(err, whadmin.ErrExists), errors.Is(err, whadmin.ErrNameTaken):
		httpjson.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, whsource.ErrInvalid), errors.Is(err, whadmin.ErrUnusable),
		errors.Is(err, whtable.ErrNoScratchTarget), errors.Is(err, whtable.ErrReadOnly):
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpjson.WriteError(w, http.StatusInternalServerError, "the webhook source could not be saved; see the server log")
	}
}

// authOf reads the auth input.
func authOf(a AuthInput) whsource.Auth {
	return whsource.Auth{
		Mode: a.Mode, Secret: a.Secret, Algorithm: a.Algorithm, SignatureHeader: a.SignatureHeader,
		Encoding: a.Encoding, Prefix: a.Prefix, TimestampHeader: a.TimestampHeader,
		ToleranceSeconds: a.ToleranceSeconds, Signed: a.Signed, Header: a.Header, Username: a.Username,
	}
}

// viewOf renders a source without its secrets.
func viewOf(s whsource.Source) SourceView {
	a := s.Auth
	view := AuthView{
		Mode: a.Mode, SecretSet: a.Secret != "", Algorithm: a.Algorithm, SignatureHeader: a.SignatureHeader,
		Encoding: a.Encoding, Prefix: a.Prefix, TimestampHeader: a.TimestampHeader,
		ToleranceSeconds: a.ToleranceSeconds, Signed: a.Signed, Header: a.Header, Username: a.Username,
	}
	if a.PreviousSecret != "" && !a.PreviousUntil.IsZero() {
		until := a.PreviousUntil.UTC()
		view.PreviousUntil = &until
	}
	return SourceView{
		Name: s.Name, Enabled: s.Enabled, Connection: s.Connection, Path: "/hooks/" + s.Name,
		Table: s.TableName(), Auth: view, Config: s.Config, CreatedBy: s.CreatedBy,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}
