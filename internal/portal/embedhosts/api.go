package embedhosts

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
)

const settingsPath = "/api/v1/admin/settings/embed-hosts"

// maxBody bounds a settings write.
const maxBody = 64 << 10

// Response is what the settings routes answer.
type Response struct {
	// Hosts is the list in force.
	Hosts []string `json:"hosts" example:"https://fast.wistia.net,https://cdn.example.com"`
	// Configured is the list the configuration file gives (portal.embeds.hosts,
	// else the platform's default), which a saved list replaces.
	Configured []string `json:"configured" example:"https://fast.wistia.net"`
}

// Request is a settings write.
type Request struct {
	Hosts []string `json:"hosts"`
}

// RegisterSettings mounts the admin routes behind wrap.
func RegisterSettings(mux *http.ServeMux, wrap func(http.Handler) http.Handler, r *Resolver, author func(*http.Request) string) {
	h := &handler{resolver: r, author: author}
	mux.Handle("GET "+settingsPath, wrap(http.HandlerFunc(h.get)))
	mux.Handle("PUT "+settingsPath, wrap(http.HandlerFunc(h.put)))
}

type handler struct {
	resolver *Resolver
	author   func(*http.Request) string
}

// get handles GET /api/v1/admin/settings/embed-hosts.
//
// @Summary      Get the embed hosts
// @Description  Returns the https origins an HTML asset may frame and load media from, in the portal and on share links (#2069), and the list the configuration file gives.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  embedhosts.Response
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/embed-hosts [get]
func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	httpjson.WriteJSON(w, http.StatusOK, Response{
		Hosts: h.resolver.Hosts(r.Context()), Configured: h.resolver.Configured(),
	})
}

// put handles PUT /api/v1/admin/settings/embed-hosts.
//
// @Summary      Set the embed hosts
// @Description  Replaces the list. Each entry is an https host, optionally with a port or a leading *. label; a bare host gains https. An empty list turns embeds off. Anything else is refused naming the entry.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        body  body  embedhosts.Request  true  "The hosts"
// @Success      200  {object}  embedhosts.Response
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/embed-hosts [put]
func (h *handler) put(w http.ResponseWriter, r *http.Request) {
	var in Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the body is not a JSON object with a hosts list")
		return
	}
	if in.Hosts == nil {
		in.Hosts = []string{}
	}
	hosts, err := h.resolver.Write(r.Context(), in.Hosts, h.author(r))
	if errors.Is(err, ErrInvalid) {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		slog.Error("embed hosts: saving failed", "error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "the embed hosts could not be saved")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, Response{Hosts: hosts, Configured: h.resolver.Configured()})
}
