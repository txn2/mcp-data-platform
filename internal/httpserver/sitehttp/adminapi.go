package sitehttp

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/namesort"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitewire"
)

const (
	adminDomains  = "/api/v1/admin/sites/domains"
	adminSettings = "/api/v1/admin/settings/sites"
)

type adminAPI struct {
	rt     *sitewire.Runtime
	author func(*http.Request) string
}

func (a *adminAPI) mount(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET "+adminDomains, wrap(http.HandlerFunc(a.domains)))
	mux.Handle("POST "+adminDomains, wrap(http.HandlerFunc(a.addDomain)))
	mux.Handle("DELETE "+adminDomains+"/{hostname}", wrap(http.HandlerFunc(a.removeDomain)))
	mux.Handle("GET "+adminSettings, wrap(http.HandlerFunc(a.settings)))
	mux.Handle("PUT "+adminSettings, wrap(http.HandlerFunc(a.saveSettings)))
}

func (a *adminAPI) actor(r *http.Request) sitesvc.Actor {
	return sitesvc.Actor{Email: a.author(r), Admin: true}
}

// AddressKind says what an address on the deployment is.
const (
	addressSite   = "site_address"
	addressDomain = "domain"
	addressApp    = "application"
)

// DeploymentAddress is one hostname the deployment answers on for a site or an
// application. Each needs an ingress rule, added by a system administrator.
type DeploymentAddress struct {
	Hostname    string `json:"hostname" example:"marketing.pages.example.net"`
	Kind        string `json:"kind" example:"site_address"`
	Site        string `json:"site,omitempty" example:"marketing"`
	Environment string `json:"environment,omitempty" example:"default"`
	Application string `json:"application,omitempty"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
}

// AddressList is every site address and domain on the deployment.
type AddressList struct {
	Addresses []DeploymentAddress `json:"addresses"`
}

// domains handles GET /api/v1/admin/sites/domains.
//
// @Summary      Every site address and domain
// @Description  Each hostname the deployment answers on for a site or an application: a site's own address and every domain added. A system administrator adds an ingress rule for each.
// @Tags         Sites
// @Produce      json
// @Success      200  {object}  AddressList
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/sites/domains [get]
func (a *adminAPI) domains(w http.ResponseWriter, r *http.Request) {
	domains, err := a.rt.Store.Domains(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	sites, err := a.rt.Store.List(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	out := make([]DeploymentAddress, 0, len(domains))
	for i := range domains {
		out = append(out, addressOf(&domains[i]))
	}
	if pages := a.rt.Live.Get().PagesDomain; pages != "" {
		for i := range sites {
			out = append(out, pagesAddresses(&sites[i], pages)...)
		}
	}
	slices.SortFunc(out, func(x, y DeploymentAddress) int { return namesort.Compare(x.Hostname, y.Hostname) })
	writeJSON(w, http.StatusOK, AddressList{Addresses: out})
}

const timeLayout = "2006-01-02T15:04:05Z"

func addressOf(d *site.Domain) DeploymentAddress {
	kind := addressDomain
	if d.Application != "" {
		kind = addressApp
	}
	return DeploymentAddress{
		Hostname: d.Hostname, Kind: kind, Site: d.Site, Environment: d.Environment, Application: d.Application,
		CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt.UTC().Format(timeLayout),
	}
}

// AdminDomainBody maps a hostname to a site environment or an application.
type AdminDomainBody struct {
	Hostname         string `json:"hostname" example:"shop.example.com"`
	Site             string `json:"site,omitempty"`
	Environment      string `json:"environment,omitempty"`
	RequiresApproval bool   `json:"requires_approval,omitempty"`
	Application      string `json:"application,omitempty"`
}

// addDomain handles POST /api/v1/admin/sites/domains.
//
// @Summary      Map a hostname
// @Description  Maps a hostname to a site environment or to an application. It routes as soon as it is added.
// @Tags         Sites
// @Accept       json
// @Produce      json
// @Param        body  body      AdminDomainBody  true  "The domain"
// @Success      201   {object}  site.Domain
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Failure      409   {object}  httpjson.ProblemDetail
// @Failure      422   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/sites/domains [post]
func (a *adminAPI) addDomain(w http.ResponseWriter, r *http.Request) {
	var in AdminDomainBody
	if !decode(w, r, &in) {
		return
	}
	d, err := a.rt.Service.AddDomain(r.Context(), a.actor(r), sitesvc.DomainRequest{
		Hostname: in.Hostname, Site: in.Site, Environment: in.Environment, RequiresApproval: in.RequiresApproval, Application: in.Application,
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

// removeDomain handles DELETE /api/v1/admin/sites/domains/{hostname}.
//
// @Summary      Remove a hostname
// @Description  Stops routing a hostname.
// @Tags         Sites
// @Param        hostname  path  string  true  "Hostname"
// @Success      204
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/sites/domains/{hostname} [delete]
func (a *adminAPI) removeDomain(w http.ResponseWriter, r *http.Request) {
	if err := a.rt.Service.RemoveDomain(r.Context(), a.actor(r), r.PathValue("hostname")); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SiteSettings is the Sites section of the settings page.
type SiteSettings struct {
	siteconfig.Settings
	UpdatedBy string `json:"updated_by,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// settings handles GET /api/v1/admin/settings/sites.
//
// @Summary      The sites settings
// @Description  The pages domain and the limits a release is held to, as saved, or the configuration file's where none are.
// @Tags         Sites
// @Produce      json
// @Success      200  {object}  SiteSettings
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/sites [get]
func (a *adminAPI) settings(w http.ResponseWriter, r *http.Request) {
	out := SiteSettings{Settings: a.rt.Config.Settings()}
	stored, err := a.rt.Settings.Get(r.Context())
	switch {
	case errors.Is(err, settingsjson.ErrNotFound):
	case err != nil:
		slog.ErrorContext(r.Context(), "sites: reading the settings failed", "error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "the settings could not be read")
		return
	default:
		out = SiteSettings{Settings: stored.Value, UpdatedBy: stored.UpdatedBy, UpdatedAt: stored.UpdatedAt.UTC().Format(timeLayout)}
	}
	writeJSON(w, http.StatusOK, out)
}

// saveSettings handles PUT /api/v1/admin/settings/sites.
//
// @Summary      Save the sites settings
// @Description  Applies on every replica without a restart. A pages domain holding the portal's own host, or under its session cookie's domain, is refused.
// @Tags         Sites
// @Accept       json
// @Produce      json
// @Param        body  body      siteconfig.Settings  true  "The settings"
// @Success      200   {object}  SiteSettings
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      422   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/sites [put]
func (a *adminAPI) saveSettings(w http.ResponseWriter, r *http.Request) {
	var in siteconfig.Settings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the request body is not valid JSON")
		return
	}
	in.PagesDomain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(in.PagesDomain)), ".")
	if err := a.rt.Save(r.Context(), in, a.author(r)); err != nil {
		fail(w, r, err)
		return
	}
	a.settings(w, r)
}

// pagesAddresses is a site's addresses on the pages domain: the main address
// and every environment's <site>--<env>.
func pagesAddresses(st *site.Site, pages string) []DeploymentAddress {
	at := st.CreatedAt.UTC().Format(timeLayout)
	out := make([]DeploymentAddress, 0, len(st.Environments))
	if st.MainEnvironment != "" {
		out = append(out, DeploymentAddress{
			Hostname: st.Name + "." + pages, Kind: addressSite, Site: st.Name, Environment: st.MainEnvironment, CreatedBy: st.CreatedBy, CreatedAt: at,
		})
	}
	for _, e := range st.Environments {
		out = append(out, DeploymentAddress{
			Hostname: site.EnvLabel(st.Name, e.Name) + "." + pages, Kind: addressSite, Site: st.Name, Environment: e.Name, CreatedBy: st.CreatedBy, CreatedAt: at,
		})
	}
	return out
}
