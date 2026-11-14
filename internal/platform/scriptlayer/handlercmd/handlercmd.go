// Package handlercmd is the part of manage_script's input and schema the
// handler commands read (#2441, #2434, #2436): the commands and the ones they
// replaced, the kind and handler they name, a tool-call handler's settings,
// and handler_test, which asks every tool-call handler on a connection what
// it would make of a call without making it.
package handlercmd

import (
	"context"
	"fmt"
	"maps"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/internal/platform/scripthandler"
)

// JSON Schema words the fragments are written in.
const (
	keyType        = "type"
	keyDescription = "description"
	keyEnum        = "enum"
	keyItems       = "items"
	valString      = "string"
	valArray       = "array"
	valObject      = "object"
	valInteger     = "integer"
	valBoolean     = "boolean"
)

// The handler commands (#2441, #2434): what starts a script.
const (
	Set     = "handler_set"
	List    = "handler_list"
	Get     = "handler_get"
	Enable  = "handler_enable"
	Disable = "handler_disable"
	Delete  = "handler_delete"
	// Test asks what every tool-call handler on a connection would make of
	// a call, without sending it (#2436).
	Test = "handler_test"
)

// Commands is every handler command, in the order the schema lists them,
// with form_preview, which opens an endpoint form's preview.
var Commands = []string{Set, List, Get, Enable, Disable, Delete, Test, epcmd.CmdFormPreview}

// Removed are the commands the handler commands replaced, with what replaces
// each: a call to one is refused naming it, and none is an alias.
var Removed = map[string]string{
	"endpoint_set":     "handler_set with kind=endpoint",
	"endpoint_list":    "handler_list with kind=endpoint",
	"endpoint_enable":  "handler_enable with the handler's id",
	"endpoint_disable": "handler_disable with the handler's id",
	"schedule_set":     "handler_set with kind=schedule",
	"schedule_list":    "handler_list with kind=schedule",
	"schedule_enable":  "handler_enable with the handler's id",
	"schedule_disable": "handler_disable with the handler's id",
}

// Target is the part of manage_script's input the handler commands read
// beside each kind's settings.
type Target struct {
	// Kind is the kind of handler handler_set adds or changes, and the kind
	// handler_list narrows to.
	Kind string `json:"kind,omitempty"`
	// Handler is the id of the handler handler_get, handler_enable,
	// handler_disable and handler_delete act on, and the one handler_set
	// changes.
	Handler string `json:"handler,omitempty"`
}

// Properties is the schema fragment for Target and CallSettings.
func Properties() map[string]any {
	props := map[string]any{
		"kind": map[string]any{
			keyType: valString, keyEnum: scripthandler.Kinds(),
			keyDescription: "For handler_set: what starts the script. endpoint answers HTTP requests at /endpoints/<endpoint> " +
				"with handle(req) (a form, defining form_spec and form_submit, is an endpoint); schedule runs main() on cron; " +
				"webhook runs main() for each event event_source stores, reading run.event. An administrator binds the three " +
				"that act on other people's tool calls on a connection: policy (decide(call) allows, denies or holds a call " +
				"before it runs), transform (transform(call, result) changes what it returns) and watch (main() runs after a " +
				"matched call succeeds, reading run.call). A script may hold several of each, " +
				fmt.Sprintf("at most %d of a kind. For handler_list: narrow to one kind.", scripthandler.MaxPerKind),
		},
		"handler": map[string]any{
			keyType: valString,
			keyDescription: "The id of a handler, as handler_set and handler_list report it: the one handler_get, handler_enable, " +
				"handler_disable and handler_delete act on, and, with handler_set, the one it changes. handler_set with no handler " +
				"adds a schedule or a webhook handler, and changes the script's endpoint handler named by endpoint (or its only one) " +
				"or adds it.",
		},
	}
	maps.Copy(props, callProperties())
	return props
}

// CallSettings is a tool-call handler's settings (#2436, #2634, #2635) and the
// call handler_test reads. Each setting is a pointer, so handler_set naming a
// handler changes only what it sends; a handler's connection is the
// connection setting endpoints share.
type CallSettings struct {
	Operations *[]string                `json:"operations,omitempty"`
	Personas   *[]string                `json:"personas,omitempty"`
	Users      *[]string                `json:"users,omitempty"`
	Conditions *[]callhandler.Condition `json:"conditions,omitempty"`
	TimeoutMS  *int                     `json:"timeout_ms,omitempty"`
	OnFailure  *string                  `json:"on_failure,omitempty"`
	ItemsPath  *string                  `json:"items_path,omitempty"`
	Writes     *map[string]bool         `json:"writes,omitempty"`
	Protected  *[]string                `json:"protected,omitempty"`
	Order      *int                     `json:"order,omitempty"`
	// Approve approves the script's latest version for handlers as the
	// administrator setting the handler.
	Approve bool `json:"approve,omitempty"`

	// The call handler_test reads: the tool, its arguments as the caller
	// would send them, who calls (a person or a persona), when, and for a
	// transform a result to change.
	Tool      string         `json:"tool,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	AsUser    string         `json:"as_user,omitempty"`
	AsPersona string         `json:"as_persona,omitempty"`
	At        string         `json:"at,omitempty"`
	Result    any            `json:"result,omitempty"`
}

// Patch is the settings s sends, as a change to a handler on connection
// (the connection setting endpoints share; "" leaves it).
func (s CallSettings) Patch(connection string) callhandler.Patch {
	p := callhandler.Patch{
		Operations: s.Operations, Personas: s.Personas, Users: s.Users, Conditions: s.Conditions,
		TimeoutMS: s.TimeoutMS, OnFailure: s.OnFailure, ItemsPath: s.ItemsPath, Writes: s.Writes,
		Protected: s.Protected, Order: s.Order,
	}
	if connection != "" {
		p.Connection = &connection
	}
	return p
}

// strings and objects the schema describes.
var (
	stringList = map[string]any{keyType: valArray, keyItems: map[string]any{keyType: valString}}
)

func withDescription(base map[string]any, description string) map[string]any {
	out := map[string]any{keyDescription: description}
	maps.Copy(out, base)
	return out
}

// keyName is the schema property naming a condition's parameter or field.
const keyName = "name"

// forTest starts the description of a property only handler_test reads.
const forTest = "For " + Test + ": "

// callProperties is the schema fragment for CallSettings.
func callProperties() map[string]any {
	return map[string]any{
		"operations": withDescription(stringList, "For handler_set with kind policy, transform or watch: the calls it applies to on its "+
			"connection, as operation ids, tool names, or METHOD path patterns (PUT /customers/*, POST *). None is every call."),
		"personas": withDescription(stringList, "For a policy, transform or watch: the personas it applies to. With users empty too, "+
			"it applies to everyone; it only ever sees calls a persona may already make."),
		"users": withDescription(stringList, "For a policy, transform or watch: the people, by address, it applies to."),
		"conditions": map[string]any{
			keyType: valArray,
			keyDescription: "For a policy, transform or watch: equalities a call's argument must meet for it to apply, so one on " +
				"one record does not run for every call on its operation.",
			keyItems: map[string]any{
				keyType: valObject,
				"properties": map[string]any{
					"in":     map[string]any{keyType: valString, keyEnum: []string{callhandler.InPath, callhandler.InBody, callhandler.InQuery}},
					keyName:  map[string]any{keyType: valString, keyDescription: "The path parameter, body field (dotted) or query parameter."},
					"equals": map[string]any{keyType: valString},
				},
				"required": []string{"in", keyName, "equals"},
			},
		},
		"timeout_ms": map[string]any{keyType: valInteger, keyDescription: "For a policy or transform: how long its script may run on " +
			"one call, in milliseconds (default 2000). Past it, the handler has failed."},
		"on_failure": map[string]any{
			keyType: valString, keyEnum: []string{callhandler.FailHold, callhandler.FailDeny, callhandler.FailCall, callhandler.FailOriginal},
			keyDescription: "What a failed script does: a policy holds the call (hold, the default) or refuses it (deny); a transform " +
				"fails the call (fail, the default) or returns the original result (original, not with protected fields).",
		},
		"items_path": map[string]any{keyType: valString, keyDescription: "For a policy: the body field whose array length is the " +
			"number of items a call carries (call.items)."},
		"writes": map[string]any{
			keyType: valObject, "additionalProperties": map[string]any{keyType: valBoolean},
			keyDescription: "For a policy: which operations count as writes, overriding POST, PUT, PATCH and DELETE (call.write).",
		},
		"protected": withDescription(stringList, "For a transform: the columns (catalog.schema.table.column) or JSON paths "+
			"(body.card.number) it protects. A covered caller may select a protected column as it is; any other use is refused."),
		"order": map[string]any{keyType: valInteger, keyDescription: "For a transform: where it runs among the connection's transforms, lowest first."},
		"approve": map[string]any{keyType: valBoolean, keyDescription: "For handler_set by an administrator: approve the script's latest " +
			"version for handlers, as you. A handler runs the highest approved version, with the roles of who approved it."},
		"tool": map[string]any{keyType: valString, keyDescription: forTest + "the tool the call would be made with."},
		"arguments": map[string]any{keyType: valObject, keyDescription: forTest + "the call's arguments as the caller would " +
			"send them (a body may be an object or a string of JSON)."},
		"as_user":    map[string]any{keyType: valString, keyDescription: forTest + "the person, by address, who would call."},
		"as_persona": map[string]any{keyType: valString, keyDescription: forTest + "the persona the call would be made under."},
		"at":         map[string]any{keyType: valString, keyDescription: forTest + "when, in RFC 3339; default now."},
		"result":     map[string]any{keyDescription: forTest + "a result for the connection's transforms to change."},
	}
}

// Tester is what handler_test asks: every handler on a connection's outcome
// for a call, without calling the upstream (callrun.Evaluator).
type Tester interface {
	Test(ctx context.Context, req callhandler.TestRequest) (map[string]any, error)
}

// RunTest answers handler_test for a caller (an administrator's only): what
// every handler on connection makes of the call s names, or the refusal.
func RunTest(ctx context.Context, admin bool, t Tester, connection string, s CallSettings) (out map[string]any, refusal string) {
	if !admin {
		return nil, "handler_test is an administrator's: it shows what every handler on a connection makes of a call"
	}
	if t == nil {
		return nil, "this deployment cannot test calls against handlers"
	}
	out, err := t.Test(ctx, callhandler.TestRequest{
		Connection: connection, Tool: s.Tool, Arguments: s.Arguments,
		User: s.AsUser, Persona: s.AsPersona, At: s.At, Result: s.Result,
	})
	if err != nil {
		return nil, err.Error()
	}
	return out, ""
}

// Withheld is a watch's run as its owner, who is not an administrator, lists
// it: that it ran and how, never what it was given or wrote (#2635).
func Withheld(out map[string]any) map[string]any {
	kept := map[string]any{"withheld": "a watch's run holds another person's call: an administrator reads what it was given, printed and wrote"}
	for _, k := range []string{keyName, "run_id", "status", "version", "trigger", "attempt", "started_at", "finished_at", "duration_ms"} {
		if v, ok := out[k]; ok {
			kept[k] = v
		}
	}
	return kept
}
