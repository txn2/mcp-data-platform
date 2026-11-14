// Package callhandler is what the three tool-call handler kinds share (#2436,
// #2634, #2635): a policy decides whether a tool call runs, a transform
// changes what it returns, and a watch runs a script after it. Each is a
// managed script bound by an administrator to a match the platform checks,
// never script: a connection, operations, who the handler applies to, and
// optional equalities on the call's arguments.
//
// A replica keeps every enabled handler in memory (Index) and answers a call
// no handler matches from that alone: no database read and no script.
package callhandler

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The kinds of tool-call handler.
const (
	KindPolicy    = "policy"
	KindTransform = "transform"
	KindWatch     = "watch"
)

// What a handler does when its script fails or runs out of time.
const (
	// FailHold holds a policy's call for an administrator's approval.
	FailHold = "hold"
	// FailDeny refuses a policy's call.
	FailDeny = "deny"
	// FailCall fails a transform's call, naming the handler.
	FailCall = "fail"
	// FailOriginal returns the result the transform was given.
	FailOriginal = "original"
)

// Where a condition reads its value.
const (
	InPath  = "path"
	InBody  = "body"
	InQuery = "query"
)

// Bounds a handler is held to.
const (
	DefaultTimeoutMS = 2000
	MaxTimeoutMS     = 60000
	maxOperations    = 50
	maxPeople        = 200
	maxConditions    = 10
	maxProtected     = 50
)

// ErrInvalid is a handler that names something wrongly.
var ErrInvalid = errors.New("invalid call handler")

func invalid(format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, ErrInvalid)...)
}

// Condition is one equality a call's argument must meet for the handler to
// apply: the path parameter, body field (dotted) or query parameter Name
// equal to Equals.
type Condition struct {
	In     string `json:"in"`
	Name   string `json:"name"`
	Equals string `json:"equals"`
}

// Handler is one tool-call handler.
type Handler struct {
	ID       string `json:"id"`
	ScriptID string `json:"script_id"`
	Kind     string `json:"kind"`
	// Connection is a connection name or a pattern matching names.
	Connection string `json:"connection"`
	// Operations are operation ids, tool names or "METHOD path" patterns;
	// none is every call on the connection.
	Operations []string `json:"operations"`
	// Personas and Users are who it applies to; both empty is everyone.
	Personas   []string        `json:"personas"`
	Users      []string        `json:"users"`
	Conditions []Condition     `json:"conditions"`
	Args       map[string]any  `json:"args"`
	TimeoutMS  int             `json:"timeout_ms"`
	OnFailure  string          `json:"on_failure"`
	ItemsPath  string          `json:"items_path"`
	Writes     map[string]bool `json:"writes"`
	Protected  []string        `json:"protected"`
	Order      int             `json:"order"`
	Enabled    bool            `json:"enabled"`
	CreatedBy  string          `json:"created_by"`
	UpdatedBy  string          `json:"updated_by"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// IsKind reports whether name is a tool-call handler kind.
func IsKind(name string) bool {
	return name == KindPolicy || name == KindTransform || name == KindWatch
}

// Normalize trims h's fields and fills its defaults for its kind.
func (h *Handler) Normalize() {
	h.Connection = strings.TrimSpace(h.Connection)
	h.Operations = cleanList(h.Operations, false)
	h.Personas = cleanList(h.Personas, false)
	h.Users = cleanList(h.Users, true)
	h.Protected = cleanList(h.Protected, false)
	h.ItemsPath = strings.TrimSpace(h.ItemsPath)
	h.OnFailure = strings.TrimSpace(h.OnFailure)
	if h.TimeoutMS == 0 {
		h.TimeoutMS = DefaultTimeoutMS
	}
	if h.OnFailure == "" {
		switch h.Kind {
		case KindPolicy:
			h.OnFailure = FailHold
		case KindTransform:
			h.OnFailure = FailCall
		}
	}
	for i := range h.Conditions {
		h.Conditions[i].In = strings.ToLower(strings.TrimSpace(h.Conditions[i].In))
		h.Conditions[i].Name = strings.TrimSpace(h.Conditions[i].Name)
	}
	if h.Args == nil {
		h.Args = map[string]any{}
	}
	if h.Writes == nil {
		h.Writes = map[string]bool{}
	}
}

// Validate refuses a handler its kind could not run, naming what is wrong.
func (h *Handler) Validate() error {
	if !IsKind(h.Kind) {
		return invalid("kind %q is not a tool-call handler: one of policy, transform, watch", h.Kind)
	}
	if h.Connection == "" {
		return invalid("connection is required: the connection, or a pattern of connections, the %s applies to", h.Kind)
	}
	if _, err := filepath.Match(h.Connection, ""); err != nil {
		return invalid("connection %q is not a valid pattern", h.Connection)
	}
	if err := h.validateMatch(); err != nil {
		return err
	}
	if h.TimeoutMS < 1 || h.TimeoutMS > MaxTimeoutMS {
		return invalid("timeout_ms must be between 1 and %d", MaxTimeoutMS)
	}
	return h.validateKind()
}

func (h *Handler) validateMatch() error {
	if len(h.Operations) > maxOperations {
		return invalid("at most %d operations", maxOperations)
	}
	for _, op := range h.Operations {
		if _, err := filepath.Match(operationPattern(op), ""); err != nil {
			return invalid("operation %q is not a valid pattern", op)
		}
	}
	if len(h.Personas)+len(h.Users) > maxPeople {
		return invalid("at most %d personas and people", maxPeople)
	}
	return h.validateConditions()
}

func (h *Handler) validateConditions() error {
	if len(h.Conditions) > maxConditions {
		return invalid("at most %d conditions", maxConditions)
	}
	for _, c := range h.Conditions {
		if c.In != InPath && c.In != InBody && c.In != InQuery {
			return invalid("condition on %q reads %q: one of path, body, query", c.Name, c.In)
		}
		if c.Name == "" {
			return invalid("a condition names the parameter or field it reads")
		}
	}
	return nil
}

func (h *Handler) validateKind() error {
	switch h.Kind {
	case KindPolicy:
		return h.validatePolicy()
	case KindTransform:
		return h.validateTransform()
	case KindWatch:
		return h.validateWatch()
	}
	return nil
}

func (h *Handler) validatePolicy() error {
	if h.OnFailure != FailHold && h.OnFailure != FailDeny {
		return invalid("a policy's on_failure is hold or deny, not %q", h.OnFailure)
	}
	if len(h.Protected) > 0 {
		return invalid("protected fields are a transform's; a policy protects nothing")
	}
	return nil
}

func (h *Handler) validateTransform() error {
	if h.OnFailure != FailCall && h.OnFailure != FailOriginal {
		return invalid("a transform's on_failure is fail or original, not %q", h.OnFailure)
	}
	if h.OnFailure == FailOriginal && len(h.Protected) > 0 {
		return invalid("a transform that protects fields fails the call when it fails: returning the original " +
			"would hand a covered caller what it protects; choose fail, or protect nothing")
	}
	if len(h.Protected) > maxProtected {
		return invalid("at most %d protected fields", maxProtected)
	}
	if h.ItemsPath != "" {
		return invalid("items_path is a policy's: it counts what a call carries")
	}
	return nil
}

func (h *Handler) validateWatch() error {
	if h.OnFailure != "" {
		return invalid("a watch runs after the call, so nothing waits on it to fail: it takes no on_failure")
	}
	if len(h.Protected) > 0 || h.ItemsPath != "" {
		return invalid("a watch reads the call; protected fields and items_path are not its")
	}
	return nil
}

// cleanList trims entries, drops empty ones and duplicates, and folds case
// when fold is set (people, by address).
func cleanList(in []string, fold bool) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if fold {
			s = strings.ToLower(s)
		}
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// Call is what the platform knows of one tool call before it runs: what a
// handler is matched against, and what a script is given.
type Call struct {
	Tool       string            `json:"tool"`
	Connection string            `json:"connection"`
	Operation  string            `json:"operation"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	PathParams map[string]string `json:"path_params"`
	Query      map[string]any    `json:"query"`
	// Arguments are the call's arguments as the caller sent them, before
	// any stored secret is filled.
	Arguments map[string]any `json:"arguments"`
	// Body is the call's body, a string of JSON read as the value it holds.
	Body  any    `json:"body"`
	Items int    `json:"items"`
	Write bool   `json:"write"`
	User  string `json:"user"`
	// Persona is the persona the caller's call was authorized under.
	Persona string    `json:"persona"`
	Time    time.Time `json:"time"`
	// SQL is a SQL tool's statement; Tables are the tables it names.
	SQL string `json:"sql,omitempty"`
}

// Matches reports whether h applies to c. It is the whole of what a call no
// handler applies to pays.
func (h *Handler) Matches(c *Call) bool {
	if !h.Enabled || !ConnectionMatches(h.Connection, c.Connection) {
		return false
	}
	if !h.coversCaller(c) || !h.coversOperation(c) {
		return false
	}
	for _, cond := range h.Conditions {
		if !cond.holds(c) {
			return false
		}
	}
	return true
}

// ConnectionMatches reports whether a handler's connection pattern applies
// to the named connection.
func ConnectionMatches(pattern, name string) bool {
	if pattern == name {
		return true
	}
	ok, _ := filepath.Match(pattern, name)
	return ok
}

func (h *Handler) coversCaller(c *Call) bool {
	if len(h.Personas) == 0 && len(h.Users) == 0 {
		return true
	}
	return (c.Persona != "" && slices.Contains(h.Personas, c.Persona)) ||
		(c.User != "" && slices.Contains(h.Users, strings.ToLower(c.User)))
}

func (h *Handler) coversOperation(c *Call) bool {
	if len(h.Operations) == 0 {
		return true
	}
	for _, op := range h.Operations {
		if operationMatches(op, c) {
			return true
		}
	}
	return false
}

// operationMatches reports whether one operation pattern matches c: "*", an
// operation id or tool name (a glob), or "METHOD path" where either may be *
// and the path is a glob over the call's path.
func operationMatches(pattern string, c *Call) bool {
	if pattern == "*" {
		return true
	}
	if method, p, ok := strings.Cut(pattern, " "); ok {
		if method != "*" && !strings.EqualFold(method, c.Method) {
			return false
		}
		p = strings.TrimSpace(p)
		return p == "*" || globPath(p, c.Path) || (c.Operation != "" && strings.EqualFold(pattern, c.Operation))
	}
	return glob(pattern, c.Operation) || glob(pattern, c.Tool)
}

func operationPattern(op string) string {
	if _, p, ok := strings.Cut(op, " "); ok {
		return strings.TrimSpace(p)
	}
	return op
}

func glob(pattern, s string) bool {
	if s == "" {
		return false
	}
	if pattern == s {
		return true
	}
	ok, _ := filepath.Match(pattern, s)
	return ok
}

// globPath matches a path pattern segment by segment, * matching one
// segment.
func globPath(pattern, p string) bool {
	if p == "" {
		return false
	}
	ok, _ := path.Match(pattern, p)
	return ok
}

// holds reports whether c's argument equals the condition's value.
func (cond Condition) holds(c *Call) bool {
	var v any
	switch cond.In {
	case InPath:
		s, ok := c.PathParams[cond.Name]
		if !ok {
			return false
		}
		v = s
	case InQuery:
		v = c.Query[cond.Name]
	case InBody:
		v = Lookup(c.Body, cond.Name)
	}
	return v != nil && scalar(v) == cond.Equals
}

// Lookup reads a dotted path out of a decoded JSON value, nil when any step
// is missing.
func Lookup(v any, dotted string) any {
	for step := range strings.SplitSeq(dotted, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[step]
	}
	return v
}

// float64Bits is the bit size a JSON number is formatted at.
const float64Bits = 64

// scalar is v as the string a condition compares: a JSON number without a
// trailing .0, a string as it is.
func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, float64Bits)
	case bool:
		return strconv.FormatBool(t)
	case []any:
		if len(t) == 1 {
			return scalar(t[0])
		}
	}
	return fmt.Sprint(v)
}

// ItemCount is how many items a call carries: the length of the array at
// itemsPath in its body, or one.
func ItemCount(body any, itemsPath string) int {
	if itemsPath == "" {
		return 1
	}
	if list, ok := Lookup(body, itemsPath).([]any); ok {
		return len(list)
	}
	return 1
}

// writeMethods are the HTTP methods a call counts as a write by, unless the
// handler says otherwise for its operation.
var writeMethods = []string{"POST", "PUT", "PATCH", "DELETE"}

// IsWrite reports whether c writes, by its method or the handler's override
// for its operation.
func (h *Handler) IsWrite(c *Call) bool {
	if w, ok := h.Writes[c.Operation]; ok && c.Operation != "" {
		return w
	}
	return slices.Contains(writeMethods, strings.ToUpper(c.Method))
}

// TestRequest is a call the test panel asks about: what every handler on a
// connection would make of it, without the upstream being called or any
// counter or allowance spent.
type TestRequest struct {
	Connection string
	Tool       string
	Arguments  map[string]any
	// User or Persona is who would call.
	User    string
	Persona string
	// At is when, in RFC 3339; empty is now.
	At string
	// Result is a result for transforms to change.
	Result any
}

// Statement is a script's application database statement as the handlers
// are asked about it: who runs it, under which persona, on which connection,
// through which binding, and its SQL.
type Statement struct {
	User, Persona, Connection, Tool, SQL string
}
