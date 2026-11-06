// Package epcmd is manage_script's endpoint binding commands (#2171):
// endpoint_set, endpoint_list, endpoint_enable and endpoint_disable, beside
// the schedule commands. internal/platform/scriptlayer resolves the script a
// command names, as it does for every command, and hands it here with who is
// asking; the input fields and their schema are spliced into manage_script's
// own.
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

// The commands.
const (
	CmdSet     = "endpoint_set"
	CmdList    = "endpoint_list"
	CmdEnable  = "endpoint_enable"
	CmdDisable = "endpoint_disable"
	// CmdFormPreview opens a preview of a form for its owner (#2494).
	CmdFormPreview = "form_preview"
)

// Commands is every endpoint command, in the order the schema lists them.
var Commands = []string{CmdSet, CmdList, CmdEnable, CmdDisable, CmdFormPreview}

// fieldName is the script's name in a result.
const fieldName = "name"

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

// Writes reports whether cmd changes a binding, which a script run may not
// do: a run that could bind a script would be able to publish a route.
func Writes(cmd string) bool { return cmd == CmdSet || cmd == CmdEnable || cmd == CmdDisable }

// Input is the part of manage_script's input the endpoint commands read.
// endpoint_set also reads manage_script's timeout_seconds.
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
	// Connection and DecryptPersonas are a form's (#2178, #2179): the Trino
	// connection its source's table is created on, and the personas a script
	// run may decrypt its sensitive fields as (nil keeps them).
	Connection      string   `json:"connection,omitempty"`
	DecryptPersonas []string `json:"decrypt_personas,omitempty"`
	// PreviewVersion is the saved version form_preview renders, 0 for the
	// latest (#2494).
	PreviewVersion int `json:"preview_version,omitempty"`
}

// Request is one endpoint command.
type Request struct {
	Command string
	Input   Input
	// TimeoutSeconds is manage_script's timeout_seconds.
	TimeoutSeconds int
	// Script is the script the command names, nil for a listing that names
	// none.
	Script *script.Script
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

	// forms makes a form's webhook source (SetForms); nil makes none.
	forms atomic.Pointer[FormSources]
}

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

// Run carries out req and returns the result manage_script answers with. An
// error is the caller's to read.
func (h *Handler) Run(ctx context.Context, req Request) (map[string]any, error) {
	if Writes(req.Command) && !req.Admin {
		if err := ownerMayWrite(req); err != nil {
			return nil, err
		}
	}
	switch req.Command {
	case CmdSet:
		return h.set(ctx, req)
	case CmdEnable, CmdDisable:
		return h.setEnabled(ctx, req)
	case CmdList:
		if req.Script != nil {
			return h.one(ctx, req.Script)
		}
		return h.list(ctx, req)
	case CmdFormPreview:
		return h.preview(ctx, req)
	}
	return nil, fmt.Errorf("unknown endpoint command %q", req.Command)
}

func (h *Handler) set(ctx context.Context, req Request) (map[string]any, error) {
	sc := req.Script
	if err := epbinding.Callable(sc.Source); err != nil {
		return nil, fmt.Errorf("script %s cannot be bound: %w", sc.Name, err)
	}
	prev, err := h.Store.Get(ctx, sc.ID)
	switch {
	case errors.Is(err, epbinding.ErrNotFound):
		prev = nil
	case err != nil:
		return nil, storeErr("failed to read the current binding", err)
	}
	in := req.Input
	b, err := epbinding.Build(sc, prev, epbinding.Request{
		Name: in.Endpoint, Methods: in.Methods, Path: in.Path, AuthMode: in.AuthMode, Callers: in.Callers,
		BotCheck: in.BotCheck, CheckedMethods: in.CheckedMethods,
		TimeoutSeconds: req.TimeoutSeconds, MaxMemoryBytes: in.MaxMemoryBytes, MaxBodyBytes: in.MaxBodyBytes,
		AllowedOrigins: in.AllowedOrigins, RateLimit: in.RateLimit, CallerRateLimit: in.CallerRateLimit,
		Actor: req.Actor,
	}, time.Now())
	if err != nil {
		return nil, err //nolint:wrapcheck // the binding's own refusal is what the caller reads
	}
	if b.AuthMode == epbinding.AuthBotCheck && h.CheckUsable != nil {
		if err := h.CheckUsable(ctx, b.BotCheck); err != nil {
			return nil, err
		}
	}
	if err := h.publicApproved(ctx, req, b); err != nil {
		return nil, err
	}
	if err := h.Store.Set(ctx, b); err != nil {
		switch {
		case errors.Is(err, epbinding.ErrNameTaken):
			return nil, fmt.Errorf("endpoint name %q is taken by another script's endpoint; choose another", b.Name)
		case errors.Is(err, epbinding.ErrNoSuchCheck):
			return nil, err //nolint:wrapcheck // names the check the binding asked for
		}
		return nil, storeErr("failed to set the binding", err)
	}
	out := h.fields(sc.Name, b)
	out["message"] = fmt.Sprintf("%s now answers %s at %s.", sc.Name, strings.Join(b.Methods, ", "), out["url"])
	if err := h.formSource(ctx, req, b, out); err != nil {
		return nil, err
	}
	return out, nil
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
			"call endpoint_set again once that is fixed", req.Script.Name, out["url"], err)
	}
	out["form"] = res
	out["message"] = fmt.Sprintf("%s Each accepted submission is stored as a row of %s on connection %s.",
		out["message"], res.Table, res.Connection)
	return nil
}

func (h *Handler) setEnabled(ctx context.Context, req Request) (map[string]any, error) {
	enabled := req.Command == CmdEnable
	if enabled && !req.Admin {
		if b, err := h.Store.Get(ctx, req.Script.ID); err == nil {
			if err := h.publicApproved(ctx, req, b); err != nil {
				return nil, err
			}
		}
	}
	err := h.Store.SetEnabled(ctx, req.Script.ID, enabled, req.Actor)
	if errors.Is(err, epbinding.ErrNotFound) {
		return nil, fmt.Errorf("script %s is not bound to an endpoint; bind it with endpoint_set", req.Script.Name)
	}
	if err != nil {
		return nil, storeErr("failed to change the binding", err)
	}
	return map[string]any{fieldName: req.Script.Name, "enabled": enabled}, nil
}

func (h *Handler) one(ctx context.Context, sc *script.Script) (map[string]any, error) {
	b, err := h.Store.Get(ctx, sc.ID)
	if errors.Is(err, epbinding.ErrNotFound) {
		out := map[string]any{fieldName: sc.Name, "bound": false}
		return out, h.describeForm(ctx, sc, out)
	}
	if err != nil {
		return nil, storeErr("failed to read the binding", err)
	}
	failures, err := h.Store.Failures(ctx, b.ID, failuresShown)
	if err != nil {
		return nil, storeErr("failed to read the binding's failed requests", err)
	}
	out := h.fields(sc.Name, b)
	out["bound"] = true
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

func (h *Handler) list(ctx context.Context, req Request) (map[string]any, error) {
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
		out["note"] = fmt.Sprintf("%d of %d endpoints listed; Admin > Automations > Endpoints pages through every one", len(items), total)
	}
	return out, nil
}

// Bound names the endpoint a script is bound to, "" when it is bound to
// none or the binding cannot be read: the save gate's question (#2171).
func (h *Handler) Bound(ctx context.Context, scriptID string) string {
	b, err := h.Store.Get(ctx, scriptID)
	if err != nil {
		return ""
	}
	return b.Name
}

// fields is a binding as manage_script reports it.
func (h *Handler) fields(scriptName string, b *epbinding.Binding) map[string]any {
	return map[string]any{
		fieldName: scriptName, "endpoint": b.Name, "url": h.BaseURL + b.URLPath(), "methods": b.Methods,
		"path": b.Path, "auth_mode": b.AuthMode, "callers": b.Callers, "bot_check": b.BotCheck,
		"checked_methods": b.CheckedMethods, "enabled": b.Enabled,
		"timeout_seconds": b.TimeoutSeconds, "max_memory_bytes": b.MaxMemoryBytes,
		"max_body_bytes": b.MaxBodyBytes, "allowed_origins": b.AllowedOrigins,
		"rate_limit": b.RateLimit, "caller_rate_limit": b.CallerRateLimit,
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

// preview opens a preview of the script's form for the caller (#2494): its
// owner or an administrator, of a saved version or the unsaved source sent.
// The link renders only for the person who opened it and expires within the
// hour.
func (h *Handler) preview(ctx context.Context, req Request) (map[string]any, error) {
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
