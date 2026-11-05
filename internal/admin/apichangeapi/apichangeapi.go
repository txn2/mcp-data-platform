// Package apichangeapi is the admin REST surface for keeping API catalog
// specs current (#2469): refreshing a spec now, how often its upstream is
// checked and whether a change waits for review (#2483), the upstream URL an
// uploaded or pasted spec is compared with (#2484), the change reports and the
// decision on a held one, what uses each operation (#2485), what upstreams
// have said about a connection's operations (#2486), and where reports and
// warnings are sent.
package apichangeapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/apichange"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// The routes.
const (
	specPrefix      = "/api/v1/admin/api-catalogs/{id}/specs/{spec}"
	refreshPath     = specPrefix + "/refresh"
	watchPath       = specPrefix + "/watch"
	changesPath     = specPrefix + "/changes"
	changePath      = specPrefix + "/changes/{change}"
	applyPath       = changePath + "/apply"
	discardPath     = changePath + "/discard"
	opUsedByPath    = specPrefix + "/operations/{operationId}/used-by"
	connUsedByPath  = "/api/v1/admin/connection-instances/api/{name}/used-by"
	connWarningPath = "/api/v1/admin/connection-instances/api/{name}/warnings"
	settingsPath    = "/api/v1/admin/settings/api-changes"
)

// The intervals an administrator may check a spec at.
const (
	minInterval = time.Hour
	maxInterval = 90 * 24 * time.Hour
	// changesLimit is how many changes a spec's history lists.
	changesLimit = 50
	// maxBody bounds a settings or watch body.
	maxBody = 64 << 10
	// maxURLBytes bounds an upstream URL.
	maxURLBytes = 2048
)

// Service checks and settles changes (*apichange.Refresher).
type Service interface {
	Check(ctx context.Context, k apichange.SpecKey, trigger, by string) (apichange.Result, error)
	Apply(ctx context.Context, k apichange.SpecKey, id, by string) (apichange.Result, error)
	Discard(ctx context.Context, k apichange.SpecKey, id, by string) error
}

// Store reads and writes watches and changes (*apichange.Store).
type Store interface {
	GetWatch(ctx context.Context, k apichange.SpecKey) (apichange.Watch, error)
	SaveSettings(ctx context.Context, w apichange.Watch, by string) error
	ListChanges(ctx context.Context, k apichange.SpecKey, limit int) ([]apichange.Change, error)
	GetChange(ctx context.Context, k apichange.SpecKey, id string) (apichange.Change, error)
}

// Catalogs reads specs (apicatalog.Store).
type Catalogs interface {
	GetSpec(ctx context.Context, catalogID, specName string) (*apicatalog.SpecEntry, error)
	ListSpecs(ctx context.Context, catalogID string) ([]apicatalog.SpecEntry, error)
	ReferencingConnections(ctx context.Context, catalogID string) ([]apicatalog.ConnectionRef, error)
}

// Config is what the routes read and write. A nil Store answers the
// database-backed routes 404; Mutable false registers the reads only.
type Config struct {
	Service  Service
	Store    Store
	Catalogs Catalogs
	// Callers answers which scripts, endpoints and prompts use operations.
	Callers func(ctx context.Context, connections []string, idx apichange.OperationIndex) (map[string][]apichange.Caller, error)
	// CatalogOf names the catalog an api connection reads, "" for none.
	CatalogOf func(ctx context.Context, connection string) string
	// Warnings lists a connection's upstream warnings.
	Warnings func(ctx context.Context, connection string) ([]apichange.Warning, error)
	// ReadSettings and SaveSettings are where reports and warnings go.
	ReadSettings func(ctx context.Context) (apichange.Settings, error)
	SaveSettings func(ctx context.Context, s apichange.Settings, by string) error
	// ChannelExists reports whether a notification channel is configured.
	ChannelExists func(ctx context.Context, name string) bool
	Author        func(*http.Request) string
	Mutable       bool
}

type handler struct{ cfg Config }

// Register mounts the routes behind wrap, the admin API's authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.Service == nil || cfg.Catalogs == nil {
		return
	}
	h := &handler{cfg: cfg}
	route := func(pattern string, fn http.HandlerFunc) { mux.Handle(pattern, wrap(fn)) }
	route("GET "+opUsedByPath, h.operationUsedBy)
	route("GET "+connUsedByPath, h.connectionUsedBy)
	if cfg.Store != nil {
		route("GET "+watchPath, h.getWatch)
		route("GET "+changesPath, h.listChanges)
		route("GET "+changePath, h.getChange)
		route("GET "+connWarningPath, h.connectionWarnings)
		route("GET "+settingsPath, h.getSettings)
	}
	if !cfg.Mutable {
		return
	}
	route("POST "+refreshPath, h.refresh)
	if cfg.Store != nil {
		route("PUT "+watchPath, h.putWatch)
		route("POST "+applyPath, h.apply)
		route("POST "+discardPath, h.discard)
		route("PUT "+settingsPath, h.putSettings)
	}
}

func specKey(r *http.Request) apichange.SpecKey {
	return apichange.SpecKey{CatalogID: r.PathValue("id"), SpecName: r.PathValue("spec")}
}

func (h *handler) author(r *http.Request) string {
	if h.cfg.Author == nil {
		return ""
	}
	return h.cfg.Author(r)
}

// RefreshView is what a refresh did.
type RefreshView struct {
	SpecName       string            `json:"spec_name"`
	SourceKind     string            `json:"source_kind"`
	ETag           string            `json:"etag,omitempty"`
	LastFetchedAt  string            `json:"last_fetched_at,omitempty"`
	OperationCount int               `json:"operation_count"`
	NotModified    bool              `json:"not_modified"`
	Change         *apichange.Change `json:"change,omitempty"`
}

// refresh handles POST /api/v1/admin/api-catalogs/{id}/specs/{spec}/refresh.
//
// @Summary      Refresh catalog spec
// @Description  Reads the spec's upstream now. A URL-sourced spec is fetched (conditionally, by its ETag); when the document changed, a change report is recorded and the new document is applied, or held for review when the spec holds changes. An uploaded or pasted spec with an upstream URL is compared with that upstream and the difference reported; the spec in use is never replaced. The response names the change recorded, or the one already waiting.
// @Tags         API Catalogs
// @Produce      json
// @Param        id    path  string  true  "Catalog ID"
// @Param        spec  path  string  true  "Spec name"
// @Success      200  {object}  RefreshView
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      413  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/refresh [post]
func (h *handler) refresh(w http.ResponseWriter, r *http.Request) {
	res, err := h.cfg.Service.Check(r.Context(), specKey(r), apichange.TriggerManual, h.author(r))
	if err != nil {
		h.checkError(r.Context(), w, err)
		return
	}
	view := RefreshView{NotModified: res.NotModified, Change: res.Change}
	if s := res.Spec; s != nil {
		view.SpecName, view.SourceKind, view.ETag, view.OperationCount = s.SpecName, s.SourceKind, s.ETag, s.OperationCount
		if !s.LastFetchedAt.IsZero() {
			view.LastFetchedAt = s.LastFetchedAt.UTC().Format(time.RFC3339)
		}
	}
	httpjson.WriteJSON(w, http.StatusOK, view)
}

// checkError answers a failed check: the store's not-found as 404, a spec
// with nothing to check as 400, a refused or oversized fetch as 400 or 413,
// an upstream that failed as 503, anything else as 500 with its detail in
// the log. A fetch error's text names the spec, never the URL.
func (h *handler) checkError(ctx context.Context, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "spec not found")
	case errors.Is(err, apichange.ErrNotRefreshable), errors.Is(err, apichange.ErrNotOpenAPI),
		errors.Is(err, apicatalog.ErrSSRFBlocked), errors.Is(err, apicatalog.ErrInvalidContent):
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, apicatalog.ErrTooLarge):
		httpjson.WriteError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, apicatalog.ErrUpstream):
		httpjson.WriteError(w, http.StatusServiceUnavailable, err.Error())
	case strings.Contains(err.Error(), "is not valid"):
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	case apichange.FetchFailed(err):
		httpjson.WriteError(w, http.StatusServiceUnavailable, err.Error())
	default:
		h.readError(ctx, w, err)
	}
}

// WatchView is how a spec is kept current.
type WatchView struct {
	// RefreshInterval is a Go duration ("168h"), "" when the spec is
	// never checked on a schedule.
	RefreshInterval string `json:"refresh_interval"`
	HoldChanges     bool   `json:"hold_changes"`
	UpstreamURL     string `json:"upstream_url"`
	NextCheckAt     string `json:"next_check_at,omitempty"`
	LastCheckedAt   string `json:"last_checked_at,omitempty"`
	LastError       string `json:"last_error,omitempty"`
	// PendingChange is the id of the change waiting for review, if any.
	PendingChange string `json:"pending_change,omitempty"`
	UpdatedBy     string `json:"updated_by,omitempty"`
}

// getWatch handles GET /api/v1/admin/api-catalogs/{id}/specs/{spec}/watch.
//
// @Summary      Get how a catalog spec is kept current
// @Description  Returns how often the spec's upstream is checked, whether a changed spec waits for review, the upstream URL an uploaded or pasted spec is compared with, when it was last and is next checked, the last check's failure, and the change waiting for review.
// @Tags         API Catalogs
// @Produce      json
// @Param        id    path  string  true  "Catalog ID"
// @Param        spec  path  string  true  "Spec name"
// @Success      200  {object}  WatchView
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/watch [get]
func (h *handler) getWatch(w http.ResponseWriter, r *http.Request) {
	k := specKey(r)
	if _, err := h.cfg.Catalogs.GetSpec(r.Context(), k.CatalogID, k.SpecName); err != nil {
		h.readError(r.Context(), w, err)
		return
	}
	h.writeWatch(w, r, k)
}

func (h *handler) writeWatch(w http.ResponseWriter, r *http.Request, k apichange.SpecKey) {
	watch, err := h.cfg.Store.GetWatch(r.Context(), k)
	if err != nil {
		h.readError(r.Context(), w, err)
		return
	}
	view := WatchView{
		HoldChanges: watch.HoldChanges, UpstreamURL: watch.UpstreamURL,
		LastError: watch.LastError, UpdatedBy: watch.UpdatedBy,
	}
	if watch.RefreshInterval > 0 {
		view.RefreshInterval = watch.RefreshInterval.String()
	}
	view.NextCheckAt, view.LastCheckedAt = stamp(watch.NextCheckAt), stamp(watch.LastCheckedAt)
	changes, err := h.cfg.Store.ListChanges(r.Context(), k, changesLimit)
	if err != nil {
		h.readError(r.Context(), w, err)
		return
	}
	for _, c := range changes {
		if c.Status == apichange.StatusPending {
			view.PendingChange = c.ID
			break
		}
	}
	httpjson.WriteJSON(w, http.StatusOK, view)
}

func stamp(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// WatchInput is what an administrator sets.
type WatchInput struct {
	// RefreshInterval is a Go duration from 1h to 2160h (90 days), or ""
	// to check only on request.
	RefreshInterval string `json:"refresh_interval"`
	HoldChanges     bool   `json:"hold_changes"`
	// UpstreamURL is for an uploaded or pasted spec; "" for none.
	UpstreamURL string `json:"upstream_url"`
}

// putWatch handles PUT /api/v1/admin/api-catalogs/{id}/specs/{spec}/watch.
//
// @Summary      Set how a catalog spec is kept current
// @Description  Sets how often the spec's upstream is checked (a duration from 1h to 2160h, or empty for on request only), whether a changed URL-sourced spec waits for review instead of replacing the one in use, and for an uploaded or pasted spec the https upstream URL it is compared with. A schedule needs something to check: a URL-sourced spec, or an upstream URL. Changing the upstream URL forgets the copy read from the old one.
// @Tags         API Catalogs
// @Accept       json
// @Produce      json
// @Param        id    path  string      true  "Catalog ID"
// @Param        spec  path  string      true  "Spec name"
// @Param        body  body  WatchInput  true  "Settings"
// @Success      200  {object}  WatchView
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/watch [put]
func (h *handler) putWatch(w http.ResponseWriter, r *http.Request) {
	k := specKey(r)
	var in WatchInput
	if err := decode(w, r, &in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	spec, err := h.cfg.Catalogs.GetSpec(r.Context(), k.CatalogID, k.SpecName)
	if err != nil {
		h.readError(r.Context(), w, err)
		return
	}
	watch, err := validateWatch(in, spec)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	watch.SpecKey = k
	if err := h.cfg.Store.SaveSettings(r.Context(), watch, h.author(r)); err != nil {
		h.readError(r.Context(), w, err)
		return
	}
	h.writeWatch(w, r, k)
}

// validateWatch checks what an administrator set against the spec.
func validateWatch(in WatchInput, spec *apicatalog.SpecEntry) (apichange.Watch, error) {
	var watch apichange.Watch
	if in.RefreshInterval != "" {
		d, err := time.ParseDuration(in.RefreshInterval)
		if err != nil || d < minInterval || d > maxInterval {
			return watch, errors.New("refresh_interval must be a duration from 1h to 2160h, or empty")
		}
		watch.RefreshInterval = d
	}
	in.UpstreamURL = strings.TrimSpace(in.UpstreamURL)
	var err error
	switch spec.SourceKind {
	case apicatalog.SourceURL:
		if in.UpstreamURL != "" {
			err = errors.New("upstream_url is for an uploaded or pasted spec; a URL-sourced spec is checked at its source_url")
		}
	case apicatalog.SourceEmbedded:
		err = errors.New("an embedded spec is the platform's own and is kept current by each release")
	default:
		err = validateUpstream(in, spec, watch.RefreshInterval > 0)
	}
	if err != nil {
		return watch, err
	}
	watch.HoldChanges, watch.UpstreamURL = in.HoldChanges, in.UpstreamURL
	return watch, nil
}

// validateUpstream checks what an uploaded or pasted spec's watch may hold:
// no hold, an upstream URL it can be compared with, and one for a schedule.
func validateUpstream(in WatchInput, spec *apicatalog.SpecEntry, scheduled bool) error {
	switch {
	case in.HoldChanges:
		return errors.New("hold_changes applies to a URL-sourced spec; an uploaded or pasted spec is never replaced by its upstream")
	case in.UpstreamURL == "" && scheduled:
		return errors.New("a schedule needs an upstream_url on an uploaded or pasted spec")
	case in.UpstreamURL == "":
		return nil
	case spec.Format() == apicatalog.FormatGraphQL:
		return apichange.ErrNotOpenAPI
	}
	return checkURL(in.UpstreamURL)
}

// checkURL refuses an upstream URL the fetch would refuse on its face: not
// http(s), or without a host. Where it points is checked when it is fetched.
func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("upstream_url must be an http or https URL with a host")
	}
	if len(raw) > maxURLBytes {
		return fmt.Errorf("upstream_url is longer than %d characters", maxURLBytes)
	}
	return nil
}

// ChangesView is a spec's changes, newest first.
type ChangesView struct {
	Changes []apichange.Change `json:"changes"`
}

// listChanges handles GET /api/v1/admin/api-catalogs/{id}/specs/{spec}/changes.
//
// @Summary      List a catalog spec's changes
// @Description  Returns the spec's most recent change reports, newest first (at most 50): each check that found the upstream different, its status (applied, pending, discarded, reported, superseded), its trigger, its one-line summary and the report: operations added, removed, changed (with the parts that changed) and newly deprecated, each with what uses it.
// @Tags         API Catalogs
// @Produce      json
// @Param        id    path  string  true  "Catalog ID"
// @Param        spec  path  string  true  "Spec name"
// @Success      200  {object}  ChangesView
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/changes [get]
func (h *handler) listChanges(w http.ResponseWriter, r *http.Request) {
	changes, err := h.cfg.Store.ListChanges(r.Context(), specKey(r), changesLimit)
	if err != nil {
		h.readError(r.Context(), w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, ChangesView{Changes: changes})
}

// getChange handles GET /api/v1/admin/api-catalogs/{id}/specs/{spec}/changes/{change}.
//
// @Summary      Get a catalog spec change
// @Description  Returns one change report of the spec.
// @Tags         API Catalogs
// @Produce      json
// @Param        id      path  string  true  "Catalog ID"
// @Param        spec    path  string  true  "Spec name"
// @Param        change  path  string  true  "Change ID"
// @Success      200  {object}  apichange.Change
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/changes/{change} [get]
func (h *handler) getChange(w http.ResponseWriter, r *http.Request) {
	c, err := h.cfg.Store.GetChange(r.Context(), specKey(r), r.PathValue("change"))
	if err != nil {
		h.readError(r.Context(), w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, c)
}

// apply handles POST /api/v1/admin/api-catalogs/{id}/specs/{spec}/changes/{change}/apply.
//
// @Summary      Apply a held catalog spec change
// @Description  Puts the document a held change fetched in use: the spec is saved, its operations re-embedded and the connections that read the catalog reloaded on every replica. Only a change waiting for review can be applied.
// @Tags         API Catalogs
// @Produce      json
// @Param        id      path  string  true  "Catalog ID"
// @Param        spec    path  string  true  "Spec name"
// @Param        change  path  string  true  "Change ID"
// @Success      200  {object}  RefreshView
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/changes/{change}/apply [post]
func (h *handler) apply(w http.ResponseWriter, r *http.Request) {
	res, err := h.cfg.Service.Apply(r.Context(), specKey(r), r.PathValue("change"), h.author(r))
	if err != nil {
		h.decideError(r.Context(), w, err)
		return
	}
	view := RefreshView{Change: res.Change}
	if s := res.Spec; s != nil {
		view.SpecName, view.SourceKind, view.ETag, view.OperationCount = s.SpecName, s.SourceKind, s.ETag, s.OperationCount
	}
	httpjson.WriteJSON(w, http.StatusOK, view)
}

// discard handles POST /api/v1/admin/api-catalogs/{id}/specs/{spec}/changes/{change}/discard.
//
// @Summary      Discard a held catalog spec change
// @Description  Drops the document a held change fetched; the spec in use is unchanged. Only a change waiting for review can be discarded.
// @Tags         API Catalogs
// @Produce      json
// @Param        id      path  string  true  "Catalog ID"
// @Param        spec    path  string  true  "Spec name"
// @Param        change  path  string  true  "Change ID"
// @Success      200  {object}  httpjson.StatusResponse
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/changes/{change}/discard [post]
func (h *handler) discard(w http.ResponseWriter, r *http.Request) {
	if err := h.cfg.Service.Discard(r.Context(), specKey(r), r.PathValue("change"), h.author(r)); err != nil {
		h.decideError(r.Context(), w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, httpjson.StatusResponse{Status: "discarded"})
}

func (h *handler) decideError(ctx context.Context, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, apichange.ErrNotPending):
		httpjson.WriteError(w, http.StatusConflict, "the change is not waiting for review")
	case strings.Contains(err.Error(), "is not valid"):
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		h.readError(ctx, w, err)
	}
}

// readError answers a failed read: a missing spec or change as 404, the rest
// as 500 with the detail in the log.
func (*handler) readError(ctx context.Context, w http.ResponseWriter, err error) {
	if errors.Is(err, apicatalog.ErrNotFound) || errors.Is(err, apichange.ErrNoChange) {
		httpjson.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	slog.ErrorContext(ctx, "api changes: a request failed", "error", logsan.SanitizeForLog(err.Error()))
	httpjson.WriteError(w, http.StatusInternalServerError, "the request failed; see the server log")
}

func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid body: %w", err)
	}
	return nil
}
