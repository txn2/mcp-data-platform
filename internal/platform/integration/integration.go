// Package integration hands operations only a deployment's environment knows
// how to do to a script an administrator chooses (#2659): routing a hostname
// through the ingress, and later operations of the same shape. Each operation
// is a contract: the function the script defines, the items it is handed, and
// the status it returns for each. With no script chosen the platform does
// nothing more than it did before.
//
// The mechanism is one copy for every operation. An operation's items are
// computed, never described by the call sites that change them: a pass
// computes the set now, compares it with the set the script was last handed,
// and the difference is the change. A pass runs when a change is announced
// (the tables an operation reads notify integration_changed), when an
// integration's run ends, and on a timer, on one replica at a time. One run
// of an operation is in flight at most; a change that arrives meanwhile is in
// the next run's difference.
package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/internal/untrusted"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The operations the platform registers.
const (
	// OpSiteRouting is every hostname the platform serves for sites and
	// applications, handed to a script that routes them (#2659).
	OpSiteRouting = "site_routing"
)

// Spec is one operation's contract.
type Spec struct {
	// Name is the operation's name in the API and the view's target.
	Name string `json:"operation"`
	// Label is how the portal names it.
	Label string `json:"label"`
	// Function is the top-level function the script defines, taking req.
	Function string `json:"function"`
	// ItemsField is the request field holding every item now, and the name
	// an item is counted by.
	ItemsField string `json:"items_field"`
	// KeyField is the entry field an item is keyed by.
	KeyField string `json:"key_field"`
	// FailedMessage is what someone other than an administrator reads on an
	// item when the script itself failed.
	FailedMessage string `json:"-"`
}

// specs is every operation, in the order the settings page lists them.
//
//nolint:gochecknoglobals // a read-only registry.
var specs = []Spec{
	{
		Name: OpSiteRouting, Label: "Site routing", Function: scriptdialect.RouteName, ItemsField: "hosts", KeyField: "hostname",
		FailedMessage: "The site routing script failed",
	},
}

// Specs is every registered operation.
func Specs() []Spec { return append([]Spec(nil), specs...) }

// SpecOf is the operation with the name.
func SpecOf(name string) (Spec, bool) {
	for _, s := range specs {
		if s.Name == name {
			return s, true
		}
	}
	return Spec{}, false
}

// Contract is the function a script defines to serve the operation, as a
// refusal names it.
func (s Spec) Contract() string { return s.Function + "(req)" }

// Contracts is every operation's function, as the handler kind names it.
func Contracts() string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Contract())
	}
	return strings.Join(out, " or ")
}

// Check refuses a source that could serve no operation: it defines none of
// the operations' functions. It is the handler kind's check, which the save
// gate asks of every script an operation is bound to.
func Check(source string) error {
	file, err := scriptdialect.Options.Parse("script", source, 0)
	if err != nil {
		return fmt.Errorf("the source does not parse: %w", err)
	}
	for _, s := range specs {
		if scriptdialect.DefOf(file, s.Function, 1) != nil {
			return nil
		}
	}
	return fmt.Errorf("an integration calls %s, and the source defines no top-level function of that name taking 1 parameter", Contracts())
}

// CheckFor refuses a source that could not serve the operation.
func (s Spec) CheckFor(source string) error {
	file, err := scriptdialect.Options.Parse("script", source, 0)
	if err != nil {
		return fmt.Errorf("the source does not parse: %w", err)
	}
	if scriptdialect.DefOf(file, s.Function, 1) == nil {
		return fmt.Errorf("the %s integration calls %s, and the source defines no top-level %s taking 1 parameter", strings.ToLower(s.Label), s.Contract(), s.Function)
	}
	return nil
}

// Item is one thing an operation hands the script: its key and the entry the
// request carries for it, which holds the key under the operation's key
// field.
type Item struct {
	Key   string
	Entry map[string]any
}

// The statuses an item reads.
const (
	StatusPending = "pending"
	StatusReady   = "ready"
	StatusFailed  = "failed"
)

// The reasons a run is made for.
const (
	ReasonChange = "change"
	ReasonTimer  = "timer"
)

// Request is what the script's function is handed.
type Request struct {
	Reason  string           `json:"reason"`
	Added   []map[string]any `json:"added"`
	Removed []map[string]any `json:"removed"`
	Items   []map[string]any `json:"-"`
}

// Map is the request as the script reads it, its items under the
// operation's field. Empty lists are [], never null.
func (r Request) Map(s Spec) map[string]any {
	nonNil := func(l []map[string]any) []any {
		out := make([]any, 0, len(l))
		for _, e := range l {
			out = append(out, e)
		}
		return out
	}
	return map[string]any{
		"reason": r.Reason, "added": nonNil(r.Added), "removed": nonNil(r.Removed), s.ItemsField: nonNil(r.Items),
	}
}

// The keys of the run's call: the operation, and the request the script is
// handed.
const (
	callOperation = "integration"
	callRequest   = "request"
)

// RunCall is what an integration's run carries in its call column.
func RunCall(s Spec, req Request) map[string]any {
	return map[string]any{callOperation: s.Name, callRequest: req.Map(s)}
}

// FromRunCall reads an integration run's call: its operation and request.
func FromRunCall(call map[string]any) (Spec, map[string]any, error) {
	name, _ := call[callOperation].(string)
	s, ok := SpecOf(name)
	if !ok {
		return Spec{}, nil, fmt.Errorf("the run names no integration the platform registers (%q)", name)
	}
	req, ok := call[callRequest].(map[string]any)
	if !ok {
		return Spec{}, nil, errors.New("the run carries no request")
	}
	return s, req, nil
}

// keysOf is the keys of a request list's entries, read from keyField.
func keysOf(list any, keyField string) []string {
	items, _ := list.([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			if k, ok := m[keyField].(string); ok && k != "" {
				out = append(out, k)
			}
		}
	}
	return out
}

// RequestKeys is every key a run's request names: its items now and the ones
// it removes.
func RequestKeys(s Spec, req map[string]any) (items, removed []string) {
	return keysOf(req[s.ItemsField], s.KeyField), keysOf(req["removed"], s.KeyField)
}

// ItemResult is the status the script returned for one item.
type ItemResult struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// maxMessage bounds the message an item keeps: it is shown to site owners.
const maxMessage = 500

// ReadResult reads the value the function returned against the keys of the
// request it was handed: a dict keyed by an item the request named, each value a dict
// of status ready or failed and an optional message. A key the request did
// not name is ignored, so a script cannot set the status of an item it was
// not asked about. The message is untrusted text: cleaned and bounded.
func ReadResult(value any, keys []string) (map[string]ItemResult, error) {
	m, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("it returned %s, and returns a dict keyed by each item's key", describe(value))
	}
	asked := make(map[string]bool, len(keys))
	for _, k := range keys {
		asked[k] = true
	}
	out := make(map[string]ItemResult, len(m))
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if !asked[k] {
			continue
		}
		r, err := readItem(k, m[k])
		if err != nil {
			return nil, err
		}
		out[k] = r
	}
	return out, nil
}

// readItem reads one item's value: a dict of status ready or failed and an
// optional string message.
func readItem(k string, v any) (ItemResult, error) {
	entry, ok := v.(map[string]any)
	if !ok {
		return ItemResult{}, fmt.Errorf("its value for %q is %s, and is a dict with status and message", k, describe(v))
	}
	status, _ := entry["status"].(string)
	if status != StatusReady && status != StatusFailed {
		return ItemResult{}, fmt.Errorf("its status for %q is %q, and is %q or %q", k, entry["status"], StatusReady, StatusFailed)
	}
	raw, present := entry["message"]
	if !present || raw == nil {
		return ItemResult{Status: status}, nil
	}
	msg, ok := raw.(string)
	if !ok {
		return ItemResult{}, fmt.Errorf("its message for %q is %s, and is a string", k, describe(raw))
	}
	return ItemResult{Status: status, Message: CleanMessage(msg)}, nil
}

// CleanMessage is a script's message as it is kept and shown: control and
// invisible runes removed, one line, bounded.
func CleanMessage(s string) string {
	s = untrusted.Line(s)
	if r := []rune(s); len(r) > maxMessage {
		s = string(r[:maxMessage])
	}
	return s
}

// describe names a value's type as a refusal reads it.
func describe(v any) string {
	switch v.(type) {
	case nil:
		return "None"
	case string:
		return "a string"
	case bool:
		return "a bool"
	case []any:
		return "a list"
	case map[string]any:
		return "a dict"
	case json.Number, float64, int64, int:
		return "a number"
	default:
		return fmt.Sprintf("a %T", v)
	}
}

// readStored reads a run's stored result against the request's keys.
func readStored(raw []byte, keys []string) (map[string]ItemResult, error) {
	if len(raw) == 0 {
		return nil, errors.New("the run returned nothing")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("decoding the result: %w", err)
	}
	return ReadResult(v, keys)
}

// Returned checks what an integration run's function returned against the
// request in the run's call, and is the result the run keeps: one entry per
// item it was asked about, message cleaned. A value of the wrong shape fails
// the run, naming what was wrong, which only an administrator reads.
func Returned(call map[string]any, value any) (json.RawMessage, error) {
	spec, req, err := FromRunCall(call)
	if err != nil {
		return nil, err
	}
	items, _ := RequestKeys(spec, req)
	results, err := ReadResult(value, items)
	if err != nil {
		return nil, fmt.Errorf("the return of %s: %w", spec.Contract(), err)
	}
	raw, err := json.Marshal(results)
	if err != nil {
		return nil, fmt.Errorf("encoding the result: %w", err)
	}
	return raw, nil
}

// Entry is the function and request an integration run calls, read from its
// call.
func Entry(call map[string]any) (function string, req map[string]any, err error) {
	spec, r, err := FromRunCall(call)
	if err != nil {
		return "", nil, err
	}
	return spec.Function, r, nil
}

// Handler is the call an integration's run makes: the operation's
// function with the request its call carries, what it returns checked against
// that request and kept in into. A value of the wrong shape fails the run.
func Handler(call map[string]any, into *json.RawMessage) (*scriptdialect.Handler, error) {
	function, req, err := Entry(call)
	if err != nil {
		return nil, fmt.Errorf("the integration run cannot start: %w", err)
	}
	arg, err := starlarkconv.ToStarlark(req)
	if err != nil {
		return nil, fmt.Errorf("the integration run cannot start: its request: %w", err)
	}
	arg.Freeze()
	return &scriptdialect.Handler{
		Name: function, Args: starlark.Tuple{arg},
		Returned: func(v starlark.Value) error {
			goValue, err := starlarkconv.FromStarlark(v)
			if err != nil {
				return fmt.Errorf("the function %s returned a value that is not data: %w", function, err)
			}
			raw, err := Returned(call, goValue)
			if err != nil {
				return err //nolint:wrapcheck // names the contract and what was wrong
			}
			*into = raw
			return nil
		},
	}, nil
}

// Prepare readies an integration's run (trigger integration): it calls the
// operation's function with the request its call carries in place of main(),
// and finish keeps what the function returned, checked, as the run's result.
// Any other run is left as it is.
func Prepare(trigger string, opts *scriptrun.Options) (finish func(*script.RunResult), err error) {
	var returned json.RawMessage
	finish = func(r *script.RunResult) {
		if returned != nil && r.Status == script.RunStatusSucceeded {
			r.Result = returned
		}
	}
	if trigger != runstate.TriggerIntegration {
		return finish, nil
	}
	handler, err := Handler(opts.Call, &returned)
	if err != nil {
		return finish, err
	}
	opts.Call, opts.Handler = nil, handler
	return finish, nil
}
