// Package botcheckapi is the admin REST surface for the bot check public
// endpoints require (#2173): which provider checks, its site key and the
// stored secret its key is in, the proof of work's difficulty and how a flood
// raises it, and the override that lets a public endpoint run with no check.
package botcheckapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/botcheck"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// path is the section's route.
const path = "/api/v1/admin/settings/bot-check"

// maxBodyBytes bounds a settings write.
const maxBodyBytes = 16 << 10

// Config carries the settings and what checks them.
type Config struct {
	Settings botcheck.SettingsStore
	// KeyAvailable is whether the deployment can sign a proof-of-work
	// challenge (auth.browser_session.signing_key).
	KeyAvailable bool
	// Secret reads the stored secret a provider's key is in, refusing one
	// the bot check may not use (botcheck.SecretUse).
	Secret func(ctx context.Context, name string) (string, error)
	// Author resolves the acting admin.
	Author func(*http.Request) string
}

// View is the settings as the page reads them.
type View struct {
	botcheck.View
	// KeyAvailable is whether a proof-of-work challenge can be signed: a
	// deployment with no browser-session signing key cannot.
	KeyAvailable bool `json:"key_available"`
	// PublicAllowed is whether a public endpoint can be bound and answered
	// under these settings.
	PublicAllowed bool `json:"public_allowed"`
}

type handler struct{ cfg Config }

// Register mounts the routes, each behind wrap, the admin API's
// authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.Settings == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.Handle("GET "+path, wrap(http.HandlerFunc(h.get)))
	mux.Handle("PUT "+path, wrap(http.HandlerFunc(h.put)))
}

// get handles GET /api/v1/admin/settings/bot-check.
//
// @Summary      Get the bot check settings
// @Description  Returns the bot check a public endpoint requires: the provider (pow, the platform's own proof-of-work challenge and the default; turnstile; hcaptcha; or none), the provider's site key and the stored secret its secret key is in, the proof of work's difficulty in leading zero bits while an endpoint is quiet, the most a flood raises it to and the answers a minute past which it rises, how long a challenge may be answered, and whether a public endpoint may run with no check. key_available reports whether this deployment can sign a proof-of-work challenge, which needs the browser-session signing key, and public_allowed whether a public endpoint can be bound under these settings.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  View
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/bot-check [get]
func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	h.writeView(w, r)
}

// put handles PUT /api/v1/admin/settings/bot-check.
//
// @Summary      Update the bot check settings
// @Description  Sets the bot check. A field left out keeps its value. Turnstile and hCaptcha need a site_key and secret_name, a stored secret (Admin > Secrets) holding the provider's secret key, whose allow_connections names bot_check and which lists no personas; the key is read on the server when a request is verified and never appears in a response. Choosing none refuses every public endpoint until allow_public_without_check is set.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        body  body  botcheck.Input  true  "Bot check settings"
// @Success      200  {object}  View
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/bot-check [put]
func (h *handler) put(w http.ResponseWriter, r *http.Request) {
	var in botcheck.Input
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	stored, err := botcheck.Read(r.Context(), h.cfg.Settings)
	if err != nil {
		fail(w, r, err)
		return
	}
	next, err := in.Apply(stored.Settings)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if next.Provider == botcheck.ProviderTurnstile || next.Provider == botcheck.ProviderHCaptcha {
		if _, err := h.cfg.Secret(r.Context(), next.SecretName); err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := h.cfg.Settings.Set(r.Context(), next, h.cfg.Author(r)); err != nil {
		fail(w, r, err)
		return
	}
	h.writeView(w, r)
}

func (h *handler) writeView(w http.ResponseWriter, r *http.Request) {
	v, err := botcheck.Read(r.Context(), h.cfg.Settings)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, View{View: v, KeyAvailable: h.cfg.KeyAvailable, PublicAllowed: v.PublicAllowedWhere(h.cfg.KeyAvailable)})
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "bot check settings: the store failed", "error", logsan.SanitizeForLog(err.Error()))
	httpjson.WriteError(w, http.StatusInternalServerError, "the bot check settings could not be read or written")
}
