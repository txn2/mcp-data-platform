// Package scripthandler is what starts a managed script (#2441, #2434,
// #2435): its handlers. A handler is one of a list of kinds, each keeping its
// own table: an endpoint (script_endpoints), a schedule (script_schedules)
// and a webhook source (script_webhook_handlers). A script may hold several
// handlers of each kind, and the read-only view script_handlers lists them
// all. The manage_script handler_* commands and the portal's handler routes
// carry out the same Service, so who may do what to which kind is decided in
// one place.
//
// Form is not a kind: an endpoint whose script defines form_spec(req) and
// form_submit(req, values) is a form, and the view reports its type as form.
package scripthandler

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
)

// The kinds of handler.
const (
	KindEndpoint = "endpoint"
	KindSchedule = "schedule"
	KindWebhook  = "webhook"
)

// The types a handler list filters on: every kind, form (an endpoint on a
// form script), and none (a script with no handler).
const (
	TypeForm = "form"
	TypeNone = "none"
)

// MaxPerKind is how many handlers of one kind a script may hold. Every
// handler can start runs, so the bound is what keeps one script from
// multiplying its own load: fifty schedules are fifty runs a minute.
const MaxPerKind = 20

// Kind is one kind of handler as the platform registers it.
type Kind struct {
	// Name is the kind's name: the kind argument and the view's kind column.
	Name string
	// Contract is the functions a script defines to hold a handler of this
	// kind, as a refusal names them.
	Contract string
	// Check refuses a source a handler of this kind could not run, nil when
	// it could. The save gate asks it of every handler the script holds.
	Check func(source string) error
	// Settings are the manage_script arguments the kind reads.
	Settings []string
	// Creator is who may create a handler of this kind.
	Creator string
}

// kinds is every kind the platform registers, in the order lists show them.
// A structure test holds it to the kinds the script_handlers view names.
var kinds = []Kind{
	{
		Name:     KindEndpoint,
		Contract: "handle(req), or form_spec(req) and form_submit(req, values) on a form",
		Check:    epbinding.Callable,
		Settings: []string{
			"endpoint", "methods", "path", "auth_mode", "callers", "bot_check", "checked_methods",
			"timeout_seconds", "max_memory_bytes", "max_body_bytes", "allowed_origins", "rate_limit",
			"caller_rate_limit", "ip_rate_limit", "response_mode", "page_cdns", "page_frame_origins",
			"page_connect_origins", "page_form_origins", "connection", "decrypt_personas",
		},
		Creator: "an administrator; a form's owner on their own form",
	},
	{
		Name:     KindSchedule,
		Contract: "main()",
		Settings: []string{"cron", "timezone", "args"},
		Creator:  "the script's owner or an administrator",
	},
	{
		Name:     KindWebhook,
		Contract: "main()",
		Settings: []string{"event_source"},
		Creator:  "the script's owner or an administrator; anyone but an administrator only for the source of a form they own",
	},
}

// Kinds is the name of every registered kind, in list order.
func Kinds() []string {
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, k.Name)
	}
	return out
}

// Types is every type a handler list filters on, in list order.
func Types() []string {
	return []string{KindEndpoint, TypeForm, KindSchedule, KindWebhook, TypeNone}
}

// KindOf is the registered kind with the name.
func KindOf(name string) (Kind, bool) {
	for _, k := range kinds {
		if k.Name == name {
			return k, true
		}
	}
	return Kind{}, false
}

// ErrUnknownKind is a kind the platform does not register.
var ErrUnknownKind = errors.New("unknown handler kind")

// checkKind refuses a kind the platform does not register, naming the ones
// it does.
func checkKind(name string) (Kind, error) {
	if name == "" {
		return Kind{}, fmt.Errorf("kind is required: one of %s: %w", strings.Join(Kinds(), ", "), ErrInvalid)
	}
	k, ok := KindOf(name)
	if !ok {
		return Kind{}, &kindError{err: fmt.Errorf("unknown handler kind %q: one of %s", name, strings.Join(Kinds(), ", ")), kind: ErrUnknownKind}
	}
	return k, nil
}

// CheckTypes refuses a type a list cannot filter on.
func CheckTypes(types []string) error {
	for _, t := range types {
		if !slices.Contains(Types(), t) {
			return fmt.Errorf("unknown handler type %q: one of %s: %w", t, strings.Join(Types(), ", "), ErrInvalid)
		}
	}
	return nil
}

// isForm reports whether a source defines a form.
func isForm(source string) bool { return epform.DefinesSource(source) }
