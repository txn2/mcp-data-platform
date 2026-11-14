package scripthandler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents/sourcerule"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The ways a handler operation is refused, which each surface reads by
// identity: an MCP error, or the HTTP status a portal route answers.
var (
	// ErrNotFound is a handler, or a script, that does not exist.
	ErrNotFound = errors.New("not found")
	// ErrNotAllowed is a caller who may not do what they asked.
	ErrNotAllowed = errors.New("not allowed")
	// ErrInvalid is a request that names something wrongly.
	ErrInvalid = errors.New("invalid request")
	// ErrStore is a store that could not be read or written.
	ErrStore = errors.New("the handler store failed")
)

// Refused reports whether err is a caller not allowed to do what they asked,
// by any kind's rule.
func Refused(err error) bool {
	return errors.Is(err, ErrNotAllowed) || errors.Is(err, epbinding.ErrNotAllowed) || errors.Is(err, sourcerule.ErrSourceRefused)
}

// Missing reports whether err is a handler or script that does not exist.
func Missing(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, epbinding.ErrNotFound) ||
		errors.Is(err, script.ErrScheduleNotFound) || errors.Is(err, scriptevents.ErrHandlerNotFound)
}

// Failed reports whether err is the platform's failure rather than the
// caller's.
func Failed(err error) bool { return errors.Is(err, ErrStore) || errors.Is(err, epcmd.ErrStore) }

// kindError is err carrying one of the sentinels above: a surface reads the
// sentinel by identity, and the caller reads err's own text, or, for a store
// failure, only that the store failed.
type kindError struct {
	err  error
	kind error
}

func (e *kindError) Error() string   { return e.err.Error() }
func (e *kindError) Unwrap() []error { return []error{e.err, e.kind} }

// storeErr is err as the store's failure.
func storeErr(err error) error { return &kindError{err: err, kind: ErrStore} }

// invalid is err as a request that names something wrongly.
func invalid(err error) error { return &kindError{err: err, kind: ErrInvalid} }

// The keys a handler's id, its kind and its script's name are reported under.
const (
	fieldHandler = "handler"
	fieldKind    = "kind"
	fieldName    = "name"
)

// Caller is who asks.
type Caller struct {
	Email string
	Admin bool
}

// Service carries out every handler operation, for every surface.
type Service struct {
	View    Reader
	Scripts Scripts
	// Endpoints is the endpoint kind; nil on a deployment that cannot bind
	// a script to an endpoint.
	Endpoints *epcmd.Handler
	Schedules ScheduleStore
	Webhooks  Webhooks
	// Sources decides which webhook source a caller may name; nil refuses
	// every source. SourceList lists them for a picker.
	Sources    SourceRule
	SourceList SourceLister
	// Now is the clock a new schedule's first fire is computed from.
	Now func() time.Time
}

// New is the service over the platform's database, nil without one: there
// is nowhere to keep a handler.
func New(db *sql.DB, endpoints *epcmd.Handler, scripts Scripts) *Service {
	if db == nil {
		return nil
	}
	s := &Service{
		View: View{DB: db}, Scripts: scripts, Endpoints: endpoints,
		Schedules: scriptstore.New(db), Webhooks: scriptevents.NewStore(db), Now: time.Now,
	}
	if rule := sourcerule.NewRule(db, scripts.GetByID); rule != nil {
		s.Sources, s.SourceList = rule, whsource.NewStore(db, nil)
	}
	return s
}

// SetRequest is one handler_set: the script, the kind, the handler it
// changes ("" adds one, or for an endpoint finds it by name) and the kind's
// settings.
type SetRequest struct {
	Caller
	Script  *script.Script
	Kind    string
	Handler string
	// Endpoint and TimeoutSeconds are an endpoint's settings.
	Endpoint       epcmd.Input
	TimeoutSeconds int
	// Cron, Timezone and Args are a schedule's.
	Cron     string
	Timezone string
	Args     map[string]any
	// EventSource is a webhook handler's.
	EventSource string
}

// mayAct refuses a caller who is neither the script's owner nor an
// administrator: a script's handlers are its owner's.
func mayAct(sc *script.Script, c Caller) error {
	if c.Admin || sc.OwnedBy(c.Email) {
		return nil
	}
	return fmt.Errorf("the script %s is not yours: its owner or an administrator acts on its handlers: %w", sc.Name, ErrNotAllowed)
}

// Set creates or changes one of a script's handlers and returns it as
// handler_get reports it.
func (s *Service) Set(ctx context.Context, req SetRequest) (map[string]any, error) {
	k, err := s.checkSet(ctx, req)
	if err != nil {
		return nil, err
	}
	switch k.Name {
	case KindEndpoint:
		return s.setEndpoint(ctx, req)
	case KindSchedule:
		return s.setSchedule(ctx, req)
	}
	return s.setWebhook(ctx, req)
}

// checkSet is req's kind once the caller may set it: the script is named,
// theirs, not a library, and holds the handler req changes.
func (s *Service) checkSet(ctx context.Context, req SetRequest) (Kind, error) {
	k, err := checkKind(req.Kind)
	if err != nil {
		return Kind{}, err
	}
	if req.Script == nil {
		return Kind{}, fmt.Errorf("a handler names its script: %w", ErrInvalid)
	}
	if err := mayAct(req.Script, req.Caller); err != nil {
		return Kind{}, err
	}
	if req.Script.Library {
		return Kind{}, fmt.Errorf("script %s is a library, which other scripts load and which never runs, so nothing starts it: %w", req.Script.Name, ErrInvalid)
	}
	return k, s.holds(ctx, req, k)
}

// holds refuses a handler req names that is not one of its script's
// handlers of its kind.
func (s *Service) holds(ctx context.Context, req SetRequest, k Kind) error {
	if req.Handler == "" {
		return nil
	}
	h, err := s.View.Get(ctx, req.Handler)
	if err != nil {
		return storeOr(err)
	}
	if h.ScriptID != req.Script.ID || h.Kind != k.Name {
		return fmt.Errorf("script %s has no %s handler %s: %w", req.Script.Name, k.Name, req.Handler, ErrNotFound)
	}
	return nil
}

// Resolve is the handler with the id and its script, once the caller may act
// on the script's handlers.
func (s *Service) Resolve(ctx context.Context, id string, c Caller) (Handler, *script.Script, error) {
	if id == "" {
		return Handler{}, nil, fmt.Errorf("handler is required: the id handler_list and handler_set report: %w", ErrInvalid)
	}
	h, err := s.View.Get(ctx, id)
	if err != nil {
		return Handler{}, nil, storeOr(err)
	}
	sc, err := s.Scripts.GetByID(ctx, h.ScriptID)
	if err != nil {
		return Handler{}, nil, storeErr(fmt.Errorf("reading handler %s's script: %w", id, err))
	}
	if sc == nil {
		return Handler{}, nil, fmt.Errorf("no handler %s: %w", id, ErrNotFound)
	}
	if err := mayAct(sc, c); err != nil {
		return Handler{}, nil, err
	}
	return h, sc, nil
}

// Get is one handler, as its kind describes it.
func (s *Service) Get(ctx context.Context, id string, c Caller) (map[string]any, error) {
	h, sc, err := s.Resolve(ctx, id, c)
	if err != nil {
		return nil, err
	}
	return s.describe(ctx, h, sc)
}

// Enable turns one handler on or off and returns it as Get does.
func (s *Service) Enable(ctx context.Context, id string, enabled bool, c Caller) (map[string]any, error) {
	h, sc, err := s.Resolve(ctx, id, c)
	if err != nil {
		return nil, err
	}
	switch h.Kind {
	case KindEndpoint:
		if err := s.enableEndpoint(ctx, h, sc, enabled, c); err != nil {
			return nil, err
		}
	case KindSchedule:
		if err := s.Schedules.EnableSchedule(ctx, h.ID, enabled, c.Email); err != nil {
			return nil, storeOr(err)
		}
	default:
		if err := s.mayRunFor(ctx, h, enabled, c); err != nil {
			return nil, err
		}
		if err := s.Webhooks.EnableHandler(ctx, h.ID, enabled, c.Email); err != nil {
			return nil, storeOr(err)
		}
	}
	return s.describe(ctx, h, sc)
}

// mayRunFor holds turning a webhook handler on to the rule adding one is held
// to: someone other than an administrator turns on only a handler for the
// source of a form they own, so a handler an administrator added for any
// other source, then turned off, stays off. Turning one off is anyone's who
// may act on the script.
func (s *Service) mayRunFor(ctx context.Context, h Handler, enabled bool, c Caller) error {
	if !enabled || c.Admin {
		return nil
	}
	if s.Sources == nil {
		return errNoSources
	}
	return s.checkSource(ctx, h.Target, c)
}

// Delete removes one handler. A schedule that produced runs is refused: it
// is disabled instead, because its runs name it.
func (s *Service) Delete(ctx context.Context, id string, c Caller) (map[string]any, error) {
	h, sc, err := s.Resolve(ctx, id, c)
	if err != nil {
		return nil, err
	}
	switch h.Kind {
	case KindEndpoint:
		err = s.deleteEndpoint(ctx, h, sc, c)
	case KindSchedule:
		err = s.Schedules.DeleteSchedule(ctx, h.ID)
		if errors.Is(err, scriptstore.ErrScheduleHasRuns) {
			return nil, fmt.Errorf("schedule handler %s of %s produced runs, which name it, so it is kept: "+
				"disable it with handler_disable: %w", h.ID, sc.Name, ErrNotAllowed)
		}
	default:
		err = s.Webhooks.DeleteHandler(ctx, h.ID)
	}
	if err != nil {
		return nil, storeOr(err)
	}
	return map[string]any{fieldHandler: h.ID, fieldKind: h.Kind, fieldName: sc.Name, "deleted": true}, nil
}

// List is one page of the handlers filter matches: every handler for an
// administrator, and the handlers of the caller's own scripts for anyone
// else.
func (s *Service) List(ctx context.Context, f Filter, c Caller) ([]Handler, int, error) {
	if err := CheckTypes(f.Types); err != nil {
		return nil, 0, err
	}
	if !c.Admin {
		f.OwnerEmail = c.Email
		if f.OwnerEmail == "" {
			return []Handler{}, 0, nil
		}
	}
	out, total, err := s.View.List(ctx, f)
	if err != nil {
		return nil, 0, storeErr(err)
	}
	return out, total, nil
}

// TypesOf is each script's handler types; a script with none is absent.
func (s *Service) TypesOf(ctx context.Context, scriptIDs []string) (map[string][]string, error) {
	out, err := s.View.TypesOf(ctx, scriptIDs)
	if err != nil {
		return nil, storeErr(err)
	}
	return out, nil
}

// Check is the save gate's question of every handler the script holds
// (#2441): why a version with source could not serve one of them, "" when it
// could serve them all. It names the handler refused.
func (s *Service) Check(ctx context.Context, sc *script.Script, source string) string {
	if s == nil || sc == nil || sc.ID == "" {
		return ""
	}
	held, _, err := s.View.List(ctx, Filter{ScriptIDs: []string{sc.ID}})
	if err != nil {
		// The save is not refused over a read that failed: the handlers'
		// own checks still refuse to run a version that cannot serve them.
		slog.WarnContext(ctx, "handlers: reading a script's handlers for its save failed", "error", logsan.SanitizeForLog(err.Error()))
		return ""
	}
	for _, h := range held {
		if refusal := checkOne(h, source); refusal != "" {
			return refusal
		}
	}
	return ""
}

// checkOne is why source could not serve h, "" when it could.
func checkOne(h Handler, source string) string {
	if h.Type == TypeForm && !isForm(source) {
		return fmt.Sprintf("this script is the form %q (endpoint handler %s), and this version defines no form_spec(req) and "+
			"form_submit(req, values), so it was not saved; delete the handler first to make the script something else", h.Target, h.ID)
	}
	k, ok := KindOf(h.Kind)
	if !ok || k.Check == nil {
		return ""
	}
	if err := k.Check(source); err != nil {
		if h.Kind == KindEndpoint {
			return fmt.Sprintf("this script answers the endpoint %q (endpoint handler %s): %s, so it was not saved", h.Target, h.ID, err)
		}
		return fmt.Sprintf("this script holds %s handler %s: %s, so it was not saved", h.Kind, h.ID, err)
	}
	return ""
}

// describe is one handler as its kind reports it.
func (s *Service) describe(ctx context.Context, h Handler, sc *script.Script) (map[string]any, error) {
	switch h.Kind {
	case KindEndpoint:
		return s.describeEndpoint(ctx, h, sc)
	case KindSchedule:
		sched, err := s.Schedules.ScheduleByID(ctx, h.ID)
		if err != nil {
			return nil, storeOr(err)
		}
		return scheduleFields(sc, sched), nil
	}
	w, err := s.Webhooks.HandlerByID(ctx, h.ID)
	if err != nil {
		return nil, storeOr(err)
	}
	return webhookFields(sc, w), nil
}

// storeOr is err as a caller reads it: a missing handler, a refusal and an
// invalid request as they are, and anything else the store's failure.
func storeOr(err error) error {
	if Missing(err) || Refused(err) || errors.Is(err, ErrInvalid) {
		return err
	}
	return storeErr(err)
}

// Offer is one kind as adding a handler to a script offers it: whether the
// caller may add one, why not when they may not, and what the script must
// define.
type Offer struct {
	Kind     string `json:"kind"`
	Allowed  bool   `json:"allowed"`
	Reason   string `json:"reason,omitempty"`
	Contract string `json:"contract"`
}

// Offers is every kind as adding a handler to sc offers it to the caller.
func (s *Service) Offers(sc *script.Script, c Caller) []Offer {
	out := make([]Offer, 0, len(kinds))
	for _, k := range kinds {
		reason := s.offerRefusal(sc, c, k.Name)
		out = append(out, Offer{Kind: k.Name, Allowed: reason == "", Reason: reason, Contract: k.Contract})
	}
	return out
}

// offerRefusal is why the caller may not add a handler of kind to sc, ""
// when they may.
func (s *Service) offerRefusal(sc *script.Script, c Caller, kind string) string {
	switch {
	case mayAct(sc, c) != nil:
		return "Only the script's owner or an administrator adds its handlers."
	case sc.Library:
		return "A library is loaded by other scripts and never runs."
	case kind == KindEndpoint && s.Endpoints == nil:
		return "This deployment cannot bind scripts to endpoints."
	case kind == KindEndpoint && !c.Admin && !sc.IsForm:
		return "An administrator adds an endpoint to a script that is not a form."
	case kind == KindWebhook && s.Sources == nil:
		return "This deployment has no webhook sources."
	}
	return ""
}
