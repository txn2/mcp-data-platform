// Package botcheckapi is the admin REST surface for the named bot checks a
// bot_check endpoint picks from (#2173, #2259): each check's provider, its
// site key and the stored secret its key is in, and the proof of work's
// difficulty and how a flood raises it.
package botcheckapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/botcheck"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// The routes.
const (
	listPath = "/api/v1/admin/bot-checks"
	onePath  = listPath + "/{name}"
)

// maxBodyBytes bounds a check write.
const maxBodyBytes = 16 << 10

// Config carries the checks and what verifies them.
type Config struct {
	Checks botcheck.Store
	// KeyAvailable is whether the deployment can sign a proof-of-work
	// challenge (auth.browser_session.signing_key).
	KeyAvailable bool
	// Secret reads the stored secret a provider's key is in, refusing one
	// the bot check may not use (botcheck.SecretUse).
	Secret func(ctx context.Context, name string) (string, error)
	// Author resolves the acting admin.
	Author func(*http.Request) string
}

// View is one check as the settings page reads it.
type View struct {
	botcheck.Check
	// UsedBy is the endpoints that name the check; a check any endpoint names
	// cannot be deleted.
	UsedBy []string `json:"used_by"`
	// Usable is false for a proof of work on a deployment that cannot sign
	// one, which no endpoint can be bound to.
	Usable bool `json:"usable"`
}

// List is the list route's answer.
type List struct {
	Checks []View `json:"checks"`
	// KeyAvailable is whether a proof-of-work challenge can be signed: a
	// deployment with no browser-session signing key cannot.
	KeyAvailable bool `json:"key_available"`
}

type handler struct{ cfg Config }

// Register mounts the routes, each behind wrap, the admin API's
// authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.Checks == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.Handle("GET "+listPath, wrap(http.HandlerFunc(h.list)))
	mux.Handle("GET "+onePath, wrap(http.HandlerFunc(h.get)))
	mux.Handle("PUT "+onePath, wrap(http.HandlerFunc(h.put)))
	mux.Handle("DELETE "+onePath, wrap(http.HandlerFunc(h.delete)))
}

// list handles GET /api/v1/admin/bot-checks.
//
// @Summary      List the bot checks
// @Description  Returns every named bot check, by name. A bot_check endpoint names the check it requires. Each check has a provider (pow, the platform's own proof-of-work challenge, which makes each request cost work any program can pay; turnstile or hcaptcha, which need a browser), the provider's site key and the stored secret its secret key is in, the proof of work's difficulty in leading zero bits while an endpoint is quiet, the most a flood raises it to and the answers a minute past which it rises, and how long a challenge may be answered. used_by lists the endpoints that name the check, and usable is false for a proof of work on a deployment that cannot sign one. key_available reports whether this deployment can sign a proof-of-work challenge, which needs the browser-session signing key.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  List
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/bot-checks [get]
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	checks, err := h.cfg.Checks.List(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	used, err := h.cfg.Checks.UsedBy(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	out := List{Checks: make([]View, 0, len(checks)), KeyAvailable: h.cfg.KeyAvailable}
	for _, c := range checks {
		out.Checks = append(out.Checks, h.view(c, used[c.Name]))
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// get handles GET /api/v1/admin/bot-checks/{name}.
//
// @Summary      Get a bot check
// @Description  Returns one named bot check with the endpoints that name it, as the list does.
// @Tags         Settings
// @Produce      json
// @Param        name  path  string  true  "Bot check name"
// @Success      200  {object}  View
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/bot-checks/{name} [get]
func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	c, err := h.cfg.Checks.Get(r.Context(), r.PathValue("name"))
	if !h.found(w, r, err) {
		return
	}
	h.writeOne(w, r, c)
}

// put handles PUT /api/v1/admin/bot-checks/{name}.
//
// @Summary      Create or update a bot check
// @Description  Creates the named check, or updates it. A field left out keeps its value, or on a new check takes the proof of work's default. The name is 1 to 63 lowercase letters, digits, hyphens and underscores. Turnstile and hCaptcha need a site_key and secret_name, a stored secret (Admin > Secrets) holding the provider's secret key, whose allow_connections names bot_check and which lists no personas; the key is read on the server when a request is verified and never appears in a response. An endpoint that wants no check uses the public access mode rather than a check.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        name  path  string          true  "Bot check name"
// @Param        body  body  botcheck.Input  true  "Bot check"
// @Success      200  {object}  View
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/bot-checks/{name} [put]
func (h *handler) put(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := botcheck.ValidateName(name); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	var in botcheck.Input
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	stored, err := h.cfg.Checks.Get(r.Context(), name)
	switch {
	case errors.Is(err, botcheck.ErrNotFound):
		stored = botcheck.New(name)
	case err != nil:
		fail(w, r, err)
		return
	}
	next, err := in.Apply(stored)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if next.NeedsBrowser() {
		if _, err := h.cfg.Secret(r.Context(), next.SecretName); err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	saved, err := h.cfg.Checks.Put(r.Context(), next, h.cfg.Author(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	h.writeOne(w, r, saved)
}

// delete handles DELETE /api/v1/admin/bot-checks/{name}.
//
// @Summary      Delete a bot check
// @Description  Deletes the named check. A check an endpoint names is not deleted: the answer is 409 and names those endpoints, which are bound to another check, or another access mode, first.
// @Tags         Settings
// @Param        name  path  string  true  "Bot check name"
// @Success      204
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/bot-checks/{name} [delete]
func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	err := h.cfg.Checks.Delete(r.Context(), r.PathValue("name"))
	if inUse, ok := errors.AsType[*botcheck.InUseError](err); ok {
		httpjson.WriteError(w, http.StatusConflict, inUse.Error())
		return
	}
	if !h.found(w, r, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// found writes the answer to a read that failed, and reports whether it
// succeeded.
func (*handler) found(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case errors.Is(err, botcheck.ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "no bot check has this name")
		return false
	case err != nil:
		fail(w, r, err)
		return false
	}
	return true
}

func (h *handler) writeOne(w http.ResponseWriter, r *http.Request, c botcheck.Check) {
	used, err := h.cfg.Checks.UsedBy(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, h.view(c, used[c.Name]))
}

func (h *handler) view(c botcheck.Check, used []string) View {
	if used == nil {
		used = []string{}
	}
	return View{Check: c, UsedBy: used, Usable: c.NeedsBrowser() || h.cfg.KeyAvailable}
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "bot checks: the store failed", "error", logsan.SanitizeForLog(err.Error()))
	httpjson.WriteError(w, http.StatusInternalServerError, "the bot checks could not be read or written")
}
