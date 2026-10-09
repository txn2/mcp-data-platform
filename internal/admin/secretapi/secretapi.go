// Package secretapi is the admin REST surface for stored secrets (#2051):
// list, read, create or change, and delete, and an authenticator seed's
// current code (#2065). The value is write-only: a PUT carries it, and no
// response ever does.
package secretapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/secretstore"
)

// secretsPath is the collection route.
const secretsPath = "/api/v1/admin/secrets" // #nosec G101 -- a route, not a credential

// maxBodyBytes bounds a request body: the largest value a secret may hold,
// with room for its scope and description.
const maxBodyBytes = secretstore.MaxValueBytes + 16<<10

// Store is what the routes act through. *secretstore.Store satisfies it.
type Store interface {
	List(ctx context.Context) ([]secretstore.Secret, error)
	Get(ctx context.Context, name string) (secretstore.Secret, error)
	Put(ctx context.Context, w secretstore.Write) (secretstore.Secret, bool, error)
	Delete(ctx context.Context, name string) error
	Code(ctx context.Context, name string) (secretstore.CurrentCode, error)
}

// Config carries the store and the parent-owned helpers.
type Config struct {
	// Store holds the secrets. nil mounts nothing.
	Store Store
	// Author resolves the acting admin.
	Author func(*http.Request) string
}

// SecretList is the list response.
type SecretList struct {
	Secrets []secretstore.Secret `json:"secrets"`
}

// SecretInput is a create or a change. Value is omitted on a change that
// keeps the stored value.
type SecretInput struct {
	Description string `json:"description"`
	// Kind is "value" (the default on a create) or "totp", an authenticator
	// seed given as its otpauth://totp URI or bare base32 seed. Omitted on a
	// change, the stored kind is kept.
	Kind             string   `json:"kind,omitempty" enums:"value,totp"`
	Value            *string  `json:"value,omitempty"`
	AllowConnections []string `json:"allow_connections"`
	AllowPersonas    []string `json:"allow_personas"`
}

type handler struct{ cfg Config }

// Register mounts the secret routes, each behind wrap, the admin API's
// authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.Store == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.Handle("GET "+secretsPath, wrap(http.HandlerFunc(h.list)))
	mux.Handle("GET "+secretsPath+"/{name}", wrap(http.HandlerFunc(h.get)))
	mux.Handle("PUT "+secretsPath+"/{name}", wrap(http.HandlerFunc(h.put)))
	mux.Handle("DELETE "+secretsPath+"/{name}", wrap(http.HandlerFunc(h.remove)))
	mux.Handle("GET "+secretsPath+"/{name}/code", wrap(http.HandlerFunc(h.code)))
}

// list handles GET /api/v1/admin/secrets.
//
// @Summary      List stored secrets
// @Description  Returns every stored secret: its name, description, the connections it may be sent through, the personas that may use it, and who wrote it when. A value is never returned.
// @Tags         Secrets
// @Produce      json
// @Success      200  {object}  SecretList
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/secrets [get]
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.cfg.Store.List(r.Context())
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "reading secrets failed")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, SecretList{Secrets: list})
}

// get handles GET /api/v1/admin/secrets/{name}.
//
// @Summary      Get a stored secret
// @Description  Returns one secret's name, description and scope. The value is never returned.
// @Tags         Secrets
// @Produce      json
// @Param        name  path  string  true  "Secret name"
// @Success      200  {object}  secretstore.Secret
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/secrets/{name} [get]
func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	sec, err := h.cfg.Store.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, sec)
}

// put handles PUT /api/v1/admin/secrets/{name}.
//
// @Summary      Create or change a stored secret
// @Description  Creates the secret, or changes it. A value secret (kind value, the default) is referenced as {{secret:<name>}} in an api_invoke_endpoint or api_export request's body, query_params, path_params or headers, and in an api, graphql or mcp connection's credential fields; the platform fills in the value as it sends the request and redacts it from the response. An authenticator seed (kind totp) is given as value in the otpauth://totp URI form a provider's QR code encodes, or as the bare base32 seed, and a request references its current one-time code as {{totp:<name>}}; the seed is never sent. allow_connections is required and names the connections it may be used by; allow_personas, when not empty, names the personas that may use it besides the administrator persona. value is required to create and may be omitted to change the rest and keep the stored value; a value must be at least 6 characters. It is encrypted at rest and never returned.
// @Tags         Secrets
// @Accept       json
// @Produce      json
// @Param        name  path  string       true  "Secret name"
// @Param        body  body  SecretInput  true  "The secret"
// @Success      200  {object}  secretstore.Secret  "Changed"
// @Success      201  {object}  secretstore.Secret  "Created"
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/secrets/{name} [put]
func (h *handler) put(w http.ResponseWriter, r *http.Request) {
	var in SecretInput
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, fmt.Sprintf("the body is not a valid secret: %v", err))
		return
	}
	if dec.More() {
		httpjson.WriteError(w, http.StatusBadRequest, "the body holds more than one JSON value")
		return
	}
	sec, created, err := h.cfg.Store.Put(r.Context(), secretstore.Write{
		Name: r.PathValue("name"), Description: in.Description, Kind: in.Kind, Value: in.Value,
		AllowConnections: in.AllowConnections, AllowPersonas: in.AllowPersonas,
		Actor: h.cfg.Author(r),
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpjson.WriteJSON(w, status, sec)
}

// remove handles DELETE /api/v1/admin/secrets/{name}.
//
// @Summary      Delete a stored secret
// @Description  Deletes the secret. A request that still references it is refused by name and sends nothing.
// @Tags         Secrets
// @Param        name  path  string  true  "Secret name"
// @Success      204
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/secrets/{name} [delete]
func (h *handler) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.cfg.Store.Delete(r.Context(), r.PathValue("name")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// code handles GET /api/v1/admin/secrets/{name}/code.
//
// @Summary      Get an authenticator seed's current code
// @Description  Returns a totp secret's one-time code for this moment, the seconds left in its period, and its parameters, for an administrator to compare with the authenticator app before an automation depends on it. Showing it does not count as issuing a code: the next request that fills {{totp:<name>}} may send the same one. A value secret has no code and is answered 409.
// @Tags         Secrets
// @Produce      json
// @Param        name  path  string  true  "Secret name"
// @Success      200  {object}  secretstore.CurrentCode
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/secrets/{name}/code [get]
func (h *handler) code(w http.ResponseWriter, r *http.Request) {
	current, err := h.cfg.Store.Code(r.Context(), r.PathValue("name"))
	if errors.Is(err, secretstore.ErrNotTOTP) {
		httpjson.WriteError(w, http.StatusConflict, "this secret holds a value, not an authenticator seed, so it has no code")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, current)
}

// writeStoreError maps the store's refusals to their status. A refusal's
// text names what to change and is returned as written; any other failure is
// not.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, secretstore.ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "secret not found")
	case errors.Is(err, secretstore.ErrInvalid):
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpjson.WriteError(w, http.StatusInternalServerError, "the secret could not be saved; see the server log")
	}
}
