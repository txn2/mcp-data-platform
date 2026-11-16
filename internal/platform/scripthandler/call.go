package scripthandler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// CallStore keeps tool-call handlers (callhandler.Store).
type CallStore interface {
	Insert(ctx context.Context, h *callhandler.Handler) error
	Update(ctx context.Context, h *callhandler.Handler) error
	Get(ctx context.Context, id string) (*callhandler.Handler, error)
	ByScript(ctx context.Context, scriptID string) ([]callhandler.Handler, error)
	SetEnabled(ctx context.Context, id string, enabled bool, by string) error
	Delete(ctx context.Context, id string) error
	Failures(ctx context.Context, handlerID string, limit int) ([]callhandler.Failure, error)
}

// VersionApprover is what a tool-call handler's version approval is read and
// recorded through (approval.Service).
type VersionApprover interface {
	// ApproveFor records an administrator's approval of version (0 is the
	// latest) of sc for its tool-call handlers, with their roles.
	ApproveFor(ctx context.Context, c Caller, sc *script.Script, version int) error
	// State is the version sc's handlers run, who approved it, and the
	// newest version waiting for approval (0 for none).
	State(ctx context.Context, sc *script.Script) (running int, approver string, waiting int)
	// Source is the source of version of sc, false when it cannot be read.
	Source(ctx context.Context, sc *script.Script, version int) (string, bool)
}

// recentFailures is how many failures handler_get shows an administrator.
const recentFailures = 10

// errCallAdmin refuses someone other than an administrator acting on a
// tool-call handler.
func errCallAdmin(kind string) error {
	return fmt.Errorf("an administrator adds, changes, turns on and off, and deletes a %s: it runs with an administrator's "+
		"authority on other people's calls: %w", kind, ErrNotAllowed)
}

// setCall adds a tool-call handler to req's script, or changes the one req
// names, and returns it as handler_get reports it.
func (s *Service) setCall(ctx context.Context, req SetRequest) (map[string]any, error) {
	if !req.Admin {
		return nil, errCallAdmin(req.Kind)
	}
	if s.Calls == nil {
		return nil, fmt.Errorf("this deployment cannot keep tool-call handlers: %w", ErrInvalid)
	}
	h, err := s.callFor(ctx, req)
	if err != nil {
		return nil, err
	}
	adding := h.ID == ""
	req.Call.Apply(h)
	h.Normalize()
	if err := h.Validate(); err != nil {
		return nil, invalid(err)
	}
	if err := s.checkCall(ctx, req, h, adding); err != nil {
		return nil, err
	}
	if h.ID == "" {
		h.CreatedBy = req.Email
		err = s.Calls.Insert(ctx, h)
	} else {
		h.UpdatedBy = req.Email
		err = s.Calls.Update(ctx, h)
	}
	if err != nil {
		return nil, storeOr(err)
	}
	s.changed()
	if req.Approve && s.Versions != nil {
		if err := s.Versions.ApproveFor(ctx, req.Caller, req.Script, 0); err != nil {
			return nil, storeOr(err)
		}
	}
	return s.callFields(ctx, req.Script, *h, req.Caller), nil
}

// checkCall refuses a tool-call handler no call would meet as it is set
// (#2649), one that applies to everyone without the administrator choosing
// that (#2650), and one whose script's version could not serve its kind
// (#2650): the refusal the save gate gives that version.
func (s *Service) checkCall(ctx context.Context, req SetRequest, h *callhandler.Handler, adding bool) error {
	if s.CallNames != nil {
		if bound, stored := s.CallNames(h.Connection); stored {
			return fmt.Errorf("connection %q is stored under that name, and its calls carry %q, the name a %s matches: "+
				"name %q: %w", h.Connection, bound, h.Kind, bound, ErrInvalid)
		}
	}
	if err := req.Call.CheckWho(h, adding); err != nil {
		return invalid(err)
	}
	version, source := s.runSource(ctx, req)
	k, ok := KindOf(h.Kind)
	if !ok || k.Check == nil {
		return nil
	}
	if err := k.Check(source); err != nil {
		return fmt.Errorf("version %d of script %s cannot be a %s: %w: %w", version, req.Script.Name, h.Kind, err, ErrInvalid)
	}
	return nil
}

// runSource is the version of req's script its tool-call handlers will run
// once req is set, and its source: the latest when req approves it or none is
// approved, else the one approved.
func (s *Service) runSource(ctx context.Context, req SetRequest) (version int, source string) {
	sc := req.Script
	if req.Approve || s.Versions == nil {
		return sc.Version, sc.Source
	}
	running, _, _ := s.Versions.State(ctx, sc)
	if running == 0 || running == sc.Version {
		return sc.Version, sc.Source
	}
	if src, ok := s.Versions.Source(ctx, sc, running); ok {
		return running, src
	}
	return sc.Version, sc.Source
}

// callFor is the handler req changes, or a new one of its kind for its
// script, refusing a script already holding the most of a kind.
func (s *Service) callFor(ctx context.Context, req SetRequest) (*callhandler.Handler, error) {
	if req.Handler != "" {
		return s.existingCall(ctx, req)
	}
	held, err := s.Calls.ByScript(ctx, req.Script.ID)
	if err != nil {
		return nil, storeErr(err)
	}
	n := 0
	for _, h := range held {
		if h.Kind == req.Kind {
			n++
		}
	}
	if n >= MaxPerKind {
		return nil, fmt.Errorf("script %s already holds %d %s handlers, the most a script may: %w", req.Script.Name, n, req.Kind, ErrInvalid)
	}
	return &callhandler.Handler{ScriptID: req.Script.ID, Kind: req.Kind, Enabled: true}, nil
}

// failuresListed is how many of a handler's recent failures its page lists.
const failuresListed = 100

// existingCall is the handler req names, when it is one of req's script's
// handlers of req's kind.
func (s *Service) existingCall(ctx context.Context, req SetRequest) (*callhandler.Handler, error) {
	h, err := s.Calls.Get(ctx, req.Handler)
	if errors.Is(err, callhandler.ErrNotFound) || (err == nil && (h.ScriptID != req.Script.ID || h.Kind != req.Kind)) {
		return nil, fmt.Errorf("script %s has no %s handler %s: %w", req.Script.Name, req.Kind, req.Handler, ErrNotFound)
	}
	if err != nil {
		return nil, storeErr(err)
	}
	return h, nil
}

// enableCall turns a tool-call handler on or off: an administrator's.
func (s *Service) enableCall(ctx context.Context, h Handler, enabled bool, c Caller) error {
	if !c.Admin {
		return errCallAdmin(h.Kind)
	}
	if err := s.Calls.SetEnabled(ctx, h.ID, enabled, c.Email); err != nil {
		return storeOr(callErr(err))
	}
	s.changed()
	return nil
}

// deleteCall removes a tool-call handler: an administrator's.
func (s *Service) deleteCall(ctx context.Context, h Handler, c Caller) error {
	if !c.Admin {
		return errCallAdmin(h.Kind)
	}
	if err := callErr(s.Calls.Delete(ctx, h.ID)); err != nil {
		return err
	}
	s.changed()
	return nil
}

// changed tells this replica's index a tool-call handler changed, before
// the database's notice reaches it.
func (s *Service) changed() {
	if s.OnCallChange != nil {
		s.OnCallChange()
	}
}

// callErr is a store's not-found as the service's.
func callErr(err error) error {
	if errors.Is(err, callhandler.ErrNotFound) {
		return fmt.Errorf("handler %w: %w", ErrNotFound, err)
	}
	return err
}

// describeCall is one tool-call handler as handler_get reports it.
func (s *Service) describeCall(ctx context.Context, h Handler, sc *script.Script, c Caller) (map[string]any, error) {
	ch, err := s.Calls.Get(ctx, h.ID)
	if err != nil {
		return nil, storeOr(callErr(err))
	}
	return s.callFields(ctx, sc, *ch, c), nil
}

// callFields is a tool-call handler's fields: its match and settings, the
// version it runs and the one waiting, and, for an administrator, its recent
// failures. What a failure holds (another person's call, what the script
// printed for it) is an administrator's only.
func (s *Service) callFields(ctx context.Context, sc *script.Script, h callhandler.Handler, c Caller) map[string]any {
	out := map[string]any{
		fieldHandler: h.ID, fieldKind: h.Kind, fieldName: sc.Name, "script_id": sc.ID,
		fieldConnection: h.Connection, "operations": h.Operations, "personas": h.Personas, "users": h.Users,
		"everyone":   h.Everyone(),
		"conditions": nonNilConds(h.Conditions), settingArgs: h.Args, "timeout_ms": h.TimeoutMS, "on_failure": h.OnFailure,
		"items_path": h.ItemsPath, "writes": h.Writes, "protected": h.Protected, "order": h.Order,
		fieldEnabled: h.Enabled, "contract": callhandler.Contract(h.Kind), fieldCreatedBy: h.CreatedBy,
	}
	if s.Versions != nil {
		running, approver, waiting := s.Versions.State(ctx, sc)
		out["running_version"], out["approved_by"], out["waiting_version"] = running, approver, waiting
	}
	if c.Admin {
		failures, err := s.Calls.Failures(ctx, h.ID, recentFailures)
		if err == nil {
			out["recent_failures"] = failures
		}
	}
	return out
}

// Failures is a tool-call handler's newest failures: an administrator's
// only, ErrNotFound for anyone else, as if there were none to name.
func (s *Service) Failures(ctx context.Context, id string, c Caller) ([]callhandler.Failure, error) {
	if !c.Admin {
		return nil, fmt.Errorf("no handler %s: %w", id, ErrNotFound)
	}
	h, _, err := s.Resolve(ctx, id, c)
	if err != nil {
		return nil, err
	}
	if !callhandler.IsKind(h.Kind) {
		return nil, fmt.Errorf("handler %s is a %s, which keeps no failures here: %w", id, h.Kind, ErrInvalid)
	}
	out, err := s.Calls.Failures(ctx, id, failuresListed)
	if err != nil {
		return nil, storeErr(err)
	}
	return out, nil
}

func nonNilConds(c []callhandler.Condition) []callhandler.Condition {
	if c == nil {
		return []callhandler.Condition{}
	}
	return c
}

// ApprovalVersions is the VersionApprover over the Approvals queue: a
// tool-call handler runs the highest version approved for call_handler, with
// the roles of the administrator who approved it.
type ApprovalVersions struct {
	Approvals *approval.Service
}

// ApproveFor records an administrator's approval of a version for handlers.
func (a ApprovalVersions) ApproveFor(ctx context.Context, c Caller, sc *script.Script, version int) error {
	return a.Approvals.ApproveForHandlers(ctx, actorOf(c), sc, version) //nolint:wrapcheck // read by identity
}

// State is the version sc's handlers run, who approved it, and the version
// waiting.
func (a ApprovalVersions) State(ctx context.Context, sc *script.Script) (running int, approver string, waiting int) {
	if run, ok := a.Approvals.HandlerRunAs(ctx, sc.ID); ok {
		running, approver = run.Version, run.Approver
	}
	return running, approver, a.Approvals.PendingHandlerVersion(ctx, sc.ID)
}

// Source is the source of version of sc, false when it cannot be read.
func (a ApprovalVersions) Source(ctx context.Context, sc *script.Script, version int) (string, bool) {
	if a.Approvals == nil || a.Approvals.Scripts == nil {
		return "", false
	}
	v, err := a.Approvals.Scripts.GetVersion(ctx, sc.ID, version)
	if err != nil || v == nil {
		return "", false
	}
	return v.Source, true
}

func actorOf(c Caller) approval.Actor {
	return approval.Actor{Email: c.Email, Persona: c.Persona, Admin: c.Admin, Roles: c.Roles}
}

// VersionSaved is what a landed save of sc does to its tool-call handlers:
// with none it does nothing; otherwise an administrator's save approves the
// version and anyone else's files a request an administrator decides, the
// approved version running meanwhile. It is the save gate's HandlersSaved.
func (s *Service) VersionSaved(ctx context.Context, sc *script.Script, caller scriptsave.Caller) {
	if s == nil || s.Calls == nil || s.Approvals == nil || sc == nil {
		return
	}
	held, err := s.Calls.ByScript(ctx, sc.ID)
	if err != nil || len(held) == 0 {
		return
	}
	actor := approval.Actor{Email: caller.Email, Persona: caller.Persona, Admin: caller.Admin, Roles: caller.Roles}
	if err := s.Approvals.HandlerVersionSaved(ctx, actor, sc, sc.Version); err != nil {
		slog.WarnContext(ctx, "handlers: recording a handler version's approval failed", "script", sc.Name,
			"error", logsan.SanitizeForLog(err.Error()))
	}
}

// handlerScriptsListed is the most scripts HandlerScripts names.
const handlerScriptsListed = 50

// HandlerScriptChoice is one script a tool-call handler of a kind can run.
type HandlerScriptChoice struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	OwnerEmail string `json:"owner_email"`
	// Version is the latest version, which defines the kind's entry.
	Version int `json:"version"`
	// Running is the version approved for handlers, 0 for none, and
	// RunningDefines whether it defines the entry too.
	Running        int  `json:"running_version"`
	RunningDefines bool `json:"running_defines"`
}

// HandlerScripts is what the Add handler dialog lists for a kind (#2650).
type HandlerScripts struct {
	Kind    string                `json:"kind"`
	Entry   string                `json:"entry"`
	Scripts []HandlerScriptChoice `json:"scripts"`
	// Looked is how many scripts were read to find them.
	Looked int `json:"looked"`
}

// HandlerScripts is the scripts a policy, transform or watch can be bound to:
// those that run (not libraries) whose latest version defines the kind's
// entry, matching search, at most 50, with the version each would run as
// handlers. An administrator's.
func (s *Service) HandlerScripts(ctx context.Context, kind, search string, c Caller) (HandlerScripts, error) {
	if !c.Admin {
		return HandlerScripts{}, errCallAdmin(kind)
	}
	k, ok := KindOf(kind)
	if !ok || k.Check == nil || !callhandler.IsKind(kind) {
		return HandlerScripts{}, fmt.Errorf("kind %q is not a tool-call handler: one of policy, transform, watch: %w", kind, ErrInvalid)
	}
	lister, ok := s.Scripts.(interface {
		List(ctx context.Context, f script.ListFilter) ([]script.Script, error)
	})
	out := HandlerScripts{Kind: kind, Entry: entryNames[kind], Scripts: []HandlerScriptChoice{}}
	if !ok {
		return out, nil
	}
	notLibrary := false
	all, err := lister.List(ctx, script.ListFilter{Search: search, Library: &notLibrary, Limit: maxScriptsLooked})
	if err != nil {
		return HandlerScripts{}, storeErr(err)
	}
	out.Looked = len(all)
	for i := range all {
		if k.Check(all[i].Source) != nil {
			continue
		}
		out.Scripts = append(out.Scripts, s.choiceOf(ctx, &all[i], k))
		if len(out.Scripts) == handlerScriptsListed {
			break
		}
	}
	return out, nil
}

// choiceOf is sc as the dialog offers it for k: its latest version, the one
// approved for handlers, and whether that one defines k's entry too.
func (s *Service) choiceOf(ctx context.Context, sc *script.Script, k Kind) HandlerScriptChoice {
	choice := HandlerScriptChoice{ID: sc.ID, Name: sc.Name, OwnerEmail: sc.OwnerEmail, Version: sc.Version}
	if s.Versions == nil {
		return choice
	}
	choice.Running, _, _ = s.Versions.State(ctx, sc)
	choice.RunningDefines = choice.Running == sc.Version
	if choice.Running > 0 && choice.Running != sc.Version {
		src, ok := s.Versions.Source(ctx, sc, choice.Running)
		choice.RunningDefines = ok && k.Check(src) == nil
	}
	return choice
}

// mainEntry is what a watch calls.
const mainEntry = "main()"

// entryNames are what each tool-call kind calls, as the dialog names it.
var entryNames = map[string]string{
	KindPolicy: "decide(call)", KindTransform: "transform(call, result)", KindWatch: mainEntry,
}

// maxScriptsLooked bounds the scripts read to find the ones a kind can run.
const maxScriptsLooked = 1000
