// Package epcmd is the endpoint kind of script handler (#2171, #2441): setting,
// describing, listing, enabling, disabling and deleting a script's endpoint
// handlers, and form_preview. internal/platform/scripthandler carries out the
// handler_* commands and the portal's handler routes through it, after
// resolving the script by the rule every command resolves by; the input
// fields and their schema are spliced into manage_script's own.
package epcmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eppreview"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epsource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// CmdFormPreview opens a preview of a form for its owner (#2494).
const CmdFormPreview = "form_preview"

// The keys a result names the script and the endpoint under.
const (
	fieldName   = "name"
	keyEndpoint = "endpoint"
)

// failuresShown is how many recent failed requests a listing of one script's
// binding carries.
const failuresShown = 20

// ErrStore marks a command that failed because a store could not be read
// or written, which is the platform's failure rather than the caller's.
var ErrStore = errors.New("the endpoint store failed")

// storeErr is msg over err, matching ErrStore and err.
func storeErr(msg string, err error) error { return &storeError{msg: msg, err: err} }

type storeError struct {
	msg string
	err error
}

func (e *storeError) Error() string   { return e.msg + ": " + e.err.Error() }
func (e *storeError) Unwrap() []error { return []error{ErrStore, e.err} }

// Input is the part of manage_script's input an endpoint handler reads.
// handler_set kind=endpoint also reads manage_script's timeout_seconds.
type Input struct {
	Endpoint        string   `json:"endpoint,omitempty"`
	Methods         []string `json:"methods,omitempty"`
	Path            *string  `json:"path,omitempty"`
	AuthMode        string   `json:"auth_mode,omitempty"`
	Callers         []string `json:"callers,omitempty"`
	BotCheck        string   `json:"bot_check,omitempty"`
	CheckedMethods  []string `json:"checked_methods,omitempty"`
	MaxMemoryBytes  int64    `json:"max_memory_bytes,omitempty"`
	MaxBodyBytes    int      `json:"max_body_bytes,omitempty"`
	AllowedOrigins  []string `json:"allowed_origins,omitempty"`
	RateLimit       int      `json:"rate_limit,omitempty"`
	CallerRateLimit int      `json:"caller_rate_limit,omitempty"`
	// ResponseMode and PageCDNs make the endpoint a browser page (#2517).
	ResponseMode string   `json:"response_mode,omitempty"`
	PageCDNs     []string `json:"page_cdns,omitempty"`
	// PageFrameOrigins, PageConnectOrigins and PageFormOrigins are what a
	// page may frame, call and post to besides its own origin (#2554).
	PageFrameOrigins   []string `json:"page_frame_origins,omitempty"`
	PageConnectOrigins []string `json:"page_connect_origins,omitempty"`
	PageFormOrigins    []string `json:"page_form_origins,omitempty"`
	// IPRateLimit is one client address's requests a minute (#2556); nil
	// keeps it, 0 removes it.
	IPRateLimit *int `json:"ip_rate_limit,omitempty"`
	// Connection and DecryptPersonas are a form's (#2178, #2179): the Trino
	// connection its source's table is created on, and the personas a script
	// run may decrypt its sensitive fields as (nil keeps them).
	Connection      string   `json:"connection,omitempty"`
	DecryptPersonas []string `json:"decrypt_personas,omitempty"`
	// PreviewVersion is the saved version form_preview renders, 0 for the
	// latest (#2494).
	PreviewVersion int `json:"preview_version,omitempty"`
}

// Request is one operation on a script's endpoint handlers.
type Request struct {
	Input Input
	// TimeoutSeconds is manage_script's timeout_seconds.
	TimeoutSeconds int
	// Script is the script whose handlers are acted on, nil for a listing
	// that names none.
	Script *script.Script
	// Binding is the id of the binding a set replaces; empty finds it by the
	// name in Input, or makes a new one.
	Binding string
	// MaxBindings is how many endpoint handlers one script may hold, 0 for
	// no bound (#2441).
	MaxBindings int
	// Visible scopes a listing that names no script to the scripts the caller
	// may see; nil for an administrator, whose listing is every binding.
	Visible map[string]*script.Script
	Admin   bool
	Actor   string
	// Limit is how many bindings a listing returns, bounded by the store.
	Limit int
	// Opener is who opens a form_preview, with the identity its drafts run
	// as; Source is the unsaved source it renders, empty for a saved
	// version (#2494).
	Opener eppreview.Opener
	Source string
}

// Handler carries the commands out.
type Handler struct {
	Store epbinding.Store
	// CheckUsable reports why a binding cannot name a bot check: none has
	// the name, or it cannot be issued here (internal/botcheck).
	CheckUsable func(ctx context.Context, name string) error
	// BaseURL is the deployment's public address, empty when it has none.
	BaseURL string
	// ScriptName names a script a listing found by id; nil leaves names out.
	ScriptName func(ctx context.Context, scriptID string) string
	// Approvals says which version of a form is approved to be served to
	// callers with no session (#2494); nil refuses making a form public.
	Approvals Approvals
	// Previews opens a form's preview (#2494); nil refuses form_preview.
	Previews *eppreview.Previews
	// PageAllowed reports why a script's endpoint cannot be a browser page
	// (#2517); nil refuses every page.
	PageAllowed func(ctx context.Context, scriptID string) error
	// PageAddress is where a script's page answers, "" when it has none.
	PageAddress func(ctx context.Context, scriptID string) string
	// NoTrustedProxies is true when the deployment trusts no proxy for a
	// client's address (server.trusted_proxies), which a binding setting
	// ip_rate_limit is warned of (#2556).
	NoTrustedProxies bool

	// forms makes a form's webhook source (SetForms); nil makes none.
	forms atomic.Pointer[FormSources]
}

// NoTrustedProxiesWarning is what a binding with an ip_rate_limit is told on
// a deployment that trusts no proxy for a client's address (#2556).
const NoTrustedProxiesWarning = "This deployment trusts no proxy for a visitor's address (server.trusted_proxies is empty). " +
	"Behind a load balancer or an ingress, every visitor then shares one address and one per-address limit. " +
	"An administrator sets server.trusted_proxies to the ingress's address range."

// Approvals is the part of the Approvals queue (internal/approval) a binding
// reads: the version approved for form_public, and an administrator's
// approval of the version they serve.
type Approvals interface {
	Approved(ctx context.Context, scriptID, purpose string) int
	RecordSelfApproval(ctx context.Context, actor approval.Actor, sc *script.Script, version int) error
}

// FormSources makes the webhook source a form stores its submissions in
// (epcollect.Maker).
type FormSources interface {
	Ensure(ctx context.Context, req epsource.SourceRequest) (epsource.SourceResult, error)
	Find(ctx context.Context, scriptID string) (epsource.SourceResult, bool, error)
	ConnectionChoices() []string
}

// SetForms is what makes a form's source from here on: the webhook
// sources, which are assembled after the endpoint commands.
func (h *Handler) SetForms(f FormSources) { h.forms.Store(&f) }

func (h *Handler) formSources() FormSources {
	if f := h.forms.Load(); f != nil {
		return *f
	}
	return nil
}

// MayWrite reports why the caller of req may not set, enable, disable or
// delete an endpoint handler of its script, nil when they may.
func MayWrite(req Request) error {
	if req.Admin {
		return nil
	}
	return ownerMayWrite(req)
}

// Set creates or changes one of the script's endpoint handlers and returns
// it as manage_script reports it. The handler changed is the one named by
// req.Binding, else the script's handler with the name in req.Input, else
// the script's only one when no name is sent; any other name makes a new one.
func (h *Handler) Set(ctx context.Context, req Request) (map[string]any, error) {
	if err := MayWrite(req); err != nil {
		return nil, err
	}
	sc := req.Script
	if err := epbinding.Callable(sc.Source); err != nil {
		return nil, fmt.Errorf("script %s cannot be bound: %w", sc.Name, err)
	}
	prev, err := h.previous(ctx, req)
	if err != nil {
		return nil, err
	}
	in := req.Input
	b, err := epbinding.Build(sc, prev, epbinding.Request{
		Name: in.Endpoint, Methods: in.Methods, Path: in.Path, AuthMode: in.AuthMode, Callers: in.Callers,
		BotCheck: in.BotCheck, CheckedMethods: in.CheckedMethods,
		TimeoutSeconds: req.TimeoutSeconds, MaxMemoryBytes: in.MaxMemoryBytes, MaxBodyBytes: in.MaxBodyBytes,
		AllowedOrigins: in.AllowedOrigins, RateLimit: in.RateLimit, CallerRateLimit: in.CallerRateLimit,
		Actor: req.Actor, ResponseMode: in.ResponseMode, PageCDNs: in.PageCDNs,
		PageFrameOrigins: in.PageFrameOrigins, PageConnectOrigins: in.PageConnectOrigins, PageFormOrigins: in.PageFormOrigins,
		IPRateLimit: in.IPRateLimit,
	}, time.Now())
	if err != nil {
		return nil, err //nolint:wrapcheck // the binding's own refusal is what the caller reads
	}
	if err := h.checkBinding(ctx, req, b); err != nil {
		return nil, err
	}
	if err := h.Store.Set(ctx, b); err != nil {
		return nil, h.setError(ctx, b, err)
	}
	out := h.setOut(sc.Name, b)
	if err := h.formSource(ctx, req, b, out); err != nil {
		return nil, err
	}
	return out, nil
}

// checkBinding refuses a binding its bot check, its approval or its page
// settings do not allow.
func (h *Handler) checkBinding(ctx context.Context, req Request, b *epbinding.Binding) error {
	if b.AuthMode == epbinding.AuthBotCheck && h.CheckUsable != nil {
		if err := h.CheckUsable(ctx, b.BotCheck); err != nil {
			return err
		}
	}
	if err := h.publicApproved(ctx, req, b); err != nil {
		return err
	}
	return h.pageAllowed(ctx, b)
}

// setOut is a binding just set as handler_set reports it.
func (h *Handler) setOut(scriptName string, b *epbinding.Binding) map[string]any {
	out := h.fields(scriptName, b)
	out["message"] = fmt.Sprintf("%s now answers %s at %s.", scriptName, strings.Join(b.Methods, ", "), out["url"])
	if b.IPRateLimit > 0 && h.NoTrustedProxies {
		out["warning"] = NoTrustedProxiesWarning
	}
	return out
}

// previous is the binding a set replaces, nil for a new one.
func (h *Handler) previous(ctx context.Context, req Request) (*epbinding.Binding, error) {
	sc := req.Script
	held, err := h.Store.ByScript(ctx, sc.ID)
	if err != nil {
		return nil, storeErr("failed to read the script's endpoint handlers", err)
	}
	switch name := req.Input.Endpoint; {
	case req.Binding != "":
		if b := findBinding(held, func(b *epbinding.Binding) bool { return b.ID == req.Binding }); b != nil {
			return b, nil
		}
		return nil, fmt.Errorf("script %s has no endpoint handler %s: %w", sc.Name, req.Binding, epbinding.ErrNotFound)
	case name != "":
		if b := findBinding(held, func(b *epbinding.Binding) bool { return b.Name == name }); b != nil {
			return b, nil
		}
	case len(held) == 1:
		return &held[0], nil
	case len(held) > 1:
		return nil, fmt.Errorf("script %s has %d endpoint handlers (%s): name the one to change with endpoint, or handler with its id",
			sc.Name, len(held), strings.Join(bindingNames(held), ", "))
	}
	if req.MaxBindings > 0 && len(held) >= req.MaxBindings {
		return nil, fmt.Errorf("script %s already has %d endpoint handlers, the most a script may hold: %w",
			sc.Name, len(held), epbinding.ErrNotAllowed)
	}
	return nil, nil //nolint:nilnil // no binding to replace: a new one
}

// findBinding is the first binding in held match accepts, nil for none.
func findBinding(held []epbinding.Binding, match func(*epbinding.Binding) bool) *epbinding.Binding {
	for i := range held {
		if match(&held[i]) {
			return &held[i]
		}
	}
	return nil
}

// bindingNames is the endpoint names held answers at.
func bindingNames(held []epbinding.Binding) []string {
	names := make([]string, 0, len(held))
	for i := range held {
		names = append(names, held[i].Name)
	}
	return names
}

// setError is what a store's refusal of a set tells the caller: a name held
// elsewhere names the handler holding it.
func (h *Handler) setError(ctx context.Context, b *epbinding.Binding, err error) error {
	switch {
	case errors.Is(err, epbinding.ErrNameTaken):
		holder, gerr := h.Store.GetByName(ctx, b.Name)
		if gerr != nil {
			return fmt.Errorf("endpoint name %q is taken by another endpoint handler; choose another", b.Name)
		}
		owner := holder.ScriptID
		if h.ScriptName != nil {
			if name := h.ScriptName(ctx, holder.ScriptID); name != "" {
				owner = name
			}
		}
		return fmt.Errorf("endpoint name %q is taken by endpoint handler %s of script %s; choose another", b.Name, holder.ID, owner)
	case errors.Is(err, epbinding.ErrNoSuchCheck), errors.Is(err, epbinding.ErrNotFound):
		return err //nolint:wrapcheck // names the check, or the handler, the request named
	}
	return storeErr("failed to set the endpoint handler", err)
}

// addPageState reports where a script's page answers and, when it could not be
// a page, why, for the portal's endpoint card (#2559).
func (h *Handler) addPageState(ctx context.Context, out map[string]any, scriptID string) {
	if h.PageAddress != nil {
		if address := h.PageAddress(ctx, scriptID); address != "" {
			out["page_address"] = address
		}
	}
	if h.PageAllowed == nil {
		out["page_problem"] = "This deployment has no pages domain."
		return
	}
	if err := h.PageAllowed(ctx, scriptID); err != nil {
		out["page_problem"] = err.Error()
	}
}

// pageAllowed refuses making an endpoint a page where no page origin serves
// it: a deployment with no pages domain, or a script in no application.
func (h *Handler) pageAllowed(ctx context.Context, b *epbinding.Binding) error {
	if b.ResponseMode != epbinding.ResponsePage {
		return nil
	}
	if h.PageAllowed == nil {
		return errors.New("this deployment serves no browser pages: it has no sites.pages_domain")
	}
	return h.PageAllowed(ctx, b.ScriptID)
}

// formSource makes or updates the webhook source a form stores into, once
// its binding is set, and reports it in out (#2178). A script that is not a
// form, or a deployment with no webhook sources, has none.
func (h *Handler) formSource(ctx context.Context, req Request, b *epbinding.Binding, out map[string]any) error {
	forms := h.formSources()
	if forms == nil || !epform.DefinesSource(req.Script.Source) {
		return nil
	}
	res, err := forms.Ensure(ctx, epsource.SourceRequest{
		ScriptID: req.Script.ID, Endpoint: b.Name, Connection: req.Input.Connection,
		DecryptPersonas: req.Input.DecryptPersonas, Actor: req.Actor,
	})
	if err != nil {
		return fmt.Errorf("%s is bound at %s, but its submissions have nowhere to be stored: %w; "+
			"call handler_set again once that is fixed", req.Script.Name, out["url"], err)
	}
	out["form"] = res
	out["message"] = fmt.Sprintf("%s Each accepted submission is stored as a row of %s on connection %s.",
		out["message"], res.Table, res.Connection)
	return nil
}

// Enable turns one of the script's endpoint handlers on or off. Turning a
// public form on by someone other than an administrator needs an approved
// version, as making it public does.
func (h *Handler) Enable(ctx context.Context, req Request, b *epbinding.Binding, enabled bool) (map[string]any, error) {
	if err := MayWrite(req); err != nil {
		return nil, err
	}
	if enabled && !req.Admin {
		if err := h.publicApproved(ctx, req, b); err != nil {
			return nil, err
		}
	}
	err := h.Store.Enable(ctx, b.ID, enabled, req.Actor)
	if errors.Is(err, epbinding.ErrNotFound) {
		return nil, fmt.Errorf("script %s has no endpoint handler %s", req.Script.Name, b.ID)
	}
	if err != nil {
		return nil, storeErr("failed to change the endpoint handler", err)
	}
	b.Enabled = enabled
	return h.fields(req.Script.Name, b), nil
}

// Delete removes one of the script's endpoint handlers: /endpoints/<name>
// answers nothing from then on, and its failed and refused requests go with
// it. A form's webhook source, and the submissions stored in it, stay.
func (h *Handler) Delete(ctx context.Context, req Request, b *epbinding.Binding) error {
	if err := MayWrite(req); err != nil {
		return err
	}
	if err := h.Store.Delete(ctx, b.ID); err != nil && !errors.Is(err, epbinding.ErrNotFound) {
		return storeErr("failed to delete the endpoint handler", err)
	}
	return nil
}

// Describe is one endpoint handler as handler_get reports it: its settings,
// where its page answers, its recent failed requests and, for a form, where
// its submissions are stored.
func (h *Handler) Describe(ctx context.Context, sc *script.Script, b *epbinding.Binding) (map[string]any, error) {
	failures, err := h.Store.Failures(ctx, b.ID, failuresShown)
	if err != nil {
		return nil, storeErr("failed to read the endpoint handler's failed requests", err)
	}
	out := h.fields(sc.Name, b)
	h.addPageState(ctx, out, b.ScriptID)
	out["failures"] = failures
	if err := h.describeForm(ctx, sc, out); err != nil {
		return nil, err
	}
	if isForm, _ := out["is_form"].(bool); isForm {
		// The hosted page a site frames with the embed snippet (#2331).
		out["page_url"] = h.BaseURL + epform.PagePrefix + b.Name
	}
	return out, nil
}

// Choices is what adding an endpoint handler to sc offers: where its page
// would answer, and, for a form, the connections its submissions could be
// stored on, or the source they are stored in.
func (h *Handler) Choices(ctx context.Context, sc *script.Script) (map[string]any, error) {
	out := map[string]any{fieldName: sc.Name}
	h.addPageState(ctx, out, sc.ID)
	return out, h.describeForm(ctx, sc, out)
}

// describeForm says, of a script that is a form, where its submissions are
// stored (form) or, before its source is made, which connections it could be
// made on (form_connections) (#2178). A script that is not a form, or a
// deployment with no webhook sources, says nothing.
func (h *Handler) describeForm(ctx context.Context, sc *script.Script, out map[string]any) error {
	forms := h.formSources()
	if forms == nil || !epform.DefinesSource(sc.Source) {
		return nil
	}
	out["is_form"] = true
	res, found, err := forms.Find(ctx, sc.ID)
	if err != nil {
		return storeErr("failed to read the form's webhook source", err)
	}
	if found {
		out["form"] = res
		return nil
	}
	out["form_connections"] = forms.ConnectionChoices()
	return nil
}

// List is one page of endpoint handlers: the caller's scripts' (req.Visible),
// or every one for an administrator.
func (h *Handler) List(ctx context.Context, req Request) (map[string]any, error) {
	filter := epbinding.Filter{Limit: req.Limit}
	if req.Visible != nil {
		filter.ScriptIDs = make([]string, 0, len(req.Visible))
		for id := range req.Visible {
			filter.ScriptIDs = append(filter.ScriptIDs, id)
		}
	}
	bindings, total, err := h.Store.List(ctx, filter)
	if err != nil {
		return nil, storeErr("failed to list endpoints", err)
	}
	items := make([]map[string]any, 0, len(bindings))
	for i := range bindings {
		name := ""
		if sc, ok := req.Visible[bindings[i].ScriptID]; ok {
			name = sc.Name
		} else if h.ScriptName != nil {
			name = h.ScriptName(ctx, bindings[i].ScriptID)
		}
		items = append(items, h.fields(name, &bindings[i]))
	}
	out := map[string]any{"endpoints": items, "count": len(items), "total": total}
	if total > len(items) {
		// A listing is one page; the caller is told the rest exist (#2251).
		out["note"] = fmt.Sprintf("%d of %d endpoints listed; Admin > Automations > Handlers pages through every one", len(items), total)
	}
	return out, nil
}

// fields is a binding as manage_script reports it.
func (h *Handler) fields(scriptName string, b *epbinding.Binding) map[string]any {
	return map[string]any{
		"handler": b.ID, "kind": keyEndpoint, fieldName: scriptName, keyEndpoint: b.Name, "url": h.BaseURL + b.URLPath(), "methods": b.Methods,
		"path": b.Path, "auth_mode": b.AuthMode, "callers": b.Callers, "bot_check": b.BotCheck,
		"checked_methods": b.CheckedMethods, "enabled": b.Enabled,
		"timeout_seconds": b.TimeoutSeconds, "max_memory_bytes": b.MaxMemoryBytes,
		"max_body_bytes": b.MaxBodyBytes, "allowed_origins": b.AllowedOrigins,
		"rate_limit": b.RateLimit, "caller_rate_limit": b.CallerRateLimit,
		"response_mode": b.ResponseMode, "page_cdns": b.PageCDNs,
		"page_frame_origins": b.PageFrameOrigins, "page_connect_origins": b.PageConnectOrigins,
		"page_form_origins": b.PageFormOrigins, "ip_rate_limit": b.IPRateLimit,
		"updated_by": b.UpdatedBy, "updated_at": b.UpdatedAt,
	}
}

// ownerMayWrite is the rule a write by someone other than an administrator
// passes (#2494): the script is theirs, and it is a form. A form's handlers
// run under its author's persona, so an internal form can do nothing its
// owner cannot already do by running the script; any other endpoint stays an
// administrator's to publish.
func ownerMayWrite(req Request) error {
	sc := req.Script
	switch {
	case sc == nil:
		return epbinding.ErrNotAllowed
	case !sc.OwnedBy(req.Actor):
		return fmt.Errorf("the script %s is not yours: its owner or an administrator binds its endpoint: %w", sc.Name, epbinding.ErrNotAllowed)
	case !epform.DefinesSource(sc.Source):
		return fmt.Errorf("the script %s is not a form: an administrator binds an endpoint that is not a form "+
			"(a form defines form_spec(req) and form_submit(req, values)): %w", sc.Name, epbinding.ErrNotAllowed)
	}
	return nil
}

// publicApproved refuses serving a form to callers with no session (public or
// bot_check) before a version of it is approved (#2494). An administrator's
// binding approves the version it serves, recorded as self-approved; anyone
// else is told to request approval. A version saved after the approval is
// served to signed-in people and not publicly until it is approved too, which
// the endpoint runner reads; this only decides whether the binding may be
// made at all.
func (h *Handler) publicApproved(ctx context.Context, req Request, b *epbinding.Binding) error {
	if b.AuthMode == epbinding.AuthAuthenticated || !epform.DefinesSource(req.Script.Source) {
		return nil
	}
	if h.Approvals == nil {
		if req.Admin {
			return nil
		}
		return fmt.Errorf("this deployment cannot approve a form for callers with no session: %w", epbinding.ErrNotAllowed)
	}
	if h.Approvals.Approved(ctx, req.Script.ID, approval.PurposeFormPublic) > 0 {
		return nil
	}
	if req.Admin {
		err := h.Approvals.RecordSelfApproval(ctx, approval.Actor{Email: req.Actor, Admin: true}, req.Script, req.Script.RunVersion())
		if err != nil {
			return storeErr("failed to record the approval of the version served", err)
		}
		return nil
	}
	return fmt.Errorf("%s has no version approved to be served without signing in: "+
		"request approval of a version (manage_approval action=submit script=%s version=%d, or Request public access "+
		"on the script's page), and set auth_mode %s once an administrator approves it: %w",
		req.Script.Name, req.Script.ID, req.Script.Version, b.AuthMode, epbinding.ErrNotAllowed)
}

// Preview opens a preview of the script's form for the caller (#2494): its
// owner or an administrator, of a saved version or the unsaved source sent.
// The link renders only for the person who opened it and expires within the
// hour.
func (h *Handler) Preview(ctx context.Context, req Request) (map[string]any, error) {
	if h.Previews == nil {
		return nil, errors.New("this deployment cannot preview a form")
	}
	if req.Script == nil {
		return nil, errors.New("form_preview names the script whose form it previews")
	}
	o := req.Opener
	o.Admin = req.Admin
	if o.Email == "" {
		o.Email = req.Actor
	}
	opened, err := h.Previews.Open(ctx, o, req.Script, req.Input.PreviewVersion, req.Source)
	if err != nil {
		return nil, err //nolint:wrapcheck // the refusal names what to change
	}
	what := "the unsaved source"
	if req.Source == "" {
		v := req.Input.PreviewVersion
		if v <= 0 {
			v = req.Script.Version
		}
		what = fmt.Sprintf("version %d", v)
	}
	return map[string]any{
		fieldName: req.Script.Name, "preview_url": opened.URL, "expires_at": opened.ExpiresAt,
		"message": fmt.Sprintf("Open %s to see %s of %s's form as /forms/<endpoint> renders it. It opens for you only, "+
			"until %s; a submission there runs form_submit as a draft and stores nothing.",
			opened.URL, what, req.Script.Name, opened.ExpiresAt.Format("15:04 MST")),
	}, nil
}
