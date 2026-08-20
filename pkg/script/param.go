package script

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Parameter type names. The set is deliberately closed and stricter than
// prompt.Argument's untyped strings: a script's parameters are bound once at
// run creation and then frozen into run.params, so a value that reaches the
// interpreter has already been checked. Anything richer than a scalar belongs
// in the script's own logic, not in its signature.
const (
	ParamTypeString = "string"
	ParamTypeInt    = "int"
	ParamTypeFloat  = "float"
	ParamTypeBool   = "bool"
	ParamTypeDate   = "date"
	ParamTypeEnum   = "enum"

	// ParamTypeConnection is a connection name (#1361). It binds as a string,
	// and it is a type of its own because the platform knows the whole set of
	// values it can take and an approved version's grant names the subset this
	// script may use. That is the difference from a string: a surface asking
	// for one can OFFER the set instead of asking somebody to remember the
	// spelling, and a value outside it is refused where it was entered rather
	// than at the run it would have failed.
	ParamTypeConnection = "connection"
)

// ConnectionParamKind is the toolkit kind a connection-typed parameter's value
// names.
//
// A connection is identified by kind and name together, and a deployment may
// legitimately carry one name across several kinds. A grant records names
// alone, which left the surface offering the value unable to say which
// connection a granted name meant (#1384). It does not have to infer one: the
// kind is a property of the binding the value is passed to, not of the grant.
// The one host binding that takes a connection is platform.query, which runs
// its statement through the Trino toolkit (internal/platform/scriptrun/host.go
// calls trino_query), so the connection a run reaches under a granted name is
// the Trino one, whatever else carries that name. platform.export names a
// destination the approval pinned rather than a connection, so it does not
// widen this.
//
// Adding a binding that takes a connection of another kind means this stops
// being one constant, and the picker route that reads it is where that would
// show first.
const ConnectionParamKind = "trino"

// validParamTypes is the set of allowed parameter types.
var validParamTypes = map[string]bool{
	ParamTypeString:     true,
	ParamTypeInt:        true,
	ParamTypeFloat:      true,
	ParamTypeBool:       true,
	ParamTypeDate:       true,
	ParamTypeEnum:       true,
	ParamTypeConnection: true,
}

// DateLayout is the one accepted wire form for a date parameter and the form
// every date-module function reads and returns. One layout, everywhere: a
// script that computes a report date and a schedule that binds one are then
// talking about the same strings.
const DateLayout = "2006-01-02"

// maxParams bounds a script's parameter list. A signature past this is a
// configuration table, and a table belongs in a queried source.
const maxParams = 32

// Number base and bit size used when coercing a supplied string to a number.
const (
	decimalBase = 10
	bitSize64   = 64
)

// validParamNamePattern matches the identifier form a parameter name must take
// so it can be a key in the frozen run.params dict without surprising an author.
var validParamNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Param is one typed parameter of a script: the contract a caller, a schedule,
// or a draft run binds values against.
type Param struct {
	Name        string `json:"name" example:"report_date"`
	Type        string `json:"type" example:"date"`
	Description string `json:"description,omitempty" example:"The business date to report on"`
	Required    bool   `json:"required" example:"true"`
	// Default supplies the value when the caller omits an optional parameter.
	// It is bound through exactly the same coercion and checking as a
	// caller-supplied value, so a default cannot smuggle in a type the
	// parameter does not accept.
	Default any `json:"default,omitempty"`
	// Values enumerates the allowed values of an enum parameter, and is
	// meaningless (and refused) on every other type.
	Values []string `json:"values,omitempty" example:"daily,weekly"`
}

// ParamsEqual reports whether two parameter contracts are identical. Param
// carries an untyped Default and a value list, so it is not a comparable type
// and slices.Equal cannot be used on a []Param; this is the one definition of
// parameter-contract equality, shared by the edit funnel and the diff surface.
func ParamsEqual(a, b []Param) bool {
	return slices.EqualFunc(a, b, func(x, y Param) bool {
		return x.Name == y.Name &&
			x.Type == y.Type &&
			x.Description == y.Description &&
			x.Required == y.Required &&
			reflect.DeepEqual(x.Default, y.Default) &&
			slices.Equal(x.Values, y.Values)
	})
}

// ValidateParams checks a parameter list is a well-formed contract: known
// types, unique identifier-shaped names, enums that enumerate something, and
// defaults that satisfy their own declared type.
func ValidateParams(params []Param) error {
	if len(params) > maxParams {
		return fmt.Errorf("too many parameters: %d (max %d)", len(params), maxParams)
	}
	seen := make(map[string]bool, len(params))
	for _, p := range params {
		if !validParamNamePattern.MatchString(p.Name) {
			return fmt.Errorf("parameter name %q must start with a lowercase letter and contain only lowercase letters, digits, and underscores", p.Name)
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate parameter %q", p.Name)
		}
		seen[p.Name] = true
		if err := validateParamShape(p); err != nil {
			return err
		}
	}
	return nil
}

// validateParamShape checks one parameter's type, enum values, and default.
func validateParamShape(p Param) error {
	if !validParamTypes[p.Type] {
		return fmt.Errorf("parameter %q has invalid type %q: must be string, int, float, bool, date, or enum", p.Name, p.Type)
	}
	if err := validateEnumValues(p); err != nil {
		return err
	}
	if p.Default == nil {
		// An omitted optional parameter binds to a typed zero so a script never
		// branches on absence, but "" is not a member of an enum's value set and
		// is not a date, so those two would hand the script a value outside the
		// contract they just declared. Requiring a default is the honest fix:
		// the author states what the parameter means when it is not supplied,
		// and the failure lands at authoring time rather than mid-run.
		if !p.Required && requiresDefault(p.Type) {
			return fmt.Errorf("optional %s parameter %q must declare a default; there is no meaningful empty %s", p.Type, p.Name, p.Type)
		}
		return nil
	}
	if _, err := coerceParam(p, p.Default); err != nil {
		return fmt.Errorf("default for parameter %q: %w", p.Name, err)
	}
	return nil
}

// requiresDefault reports whether a type has no meaningful empty value, so an
// optional parameter of it must state what it means when nobody supplies one.
// A connection joins the list for the same reason a date does: "" is not a
// connection, and a run that binds it is refused by the grant, which turns an
// omitted optional parameter into a failed run.
func requiresDefault(paramType string) bool {
	switch paramType {
	case ParamTypeEnum, ParamTypeDate, ParamTypeConnection:
		return true
	default:
		return false
	}
}

// validateEnumValues checks that an enum enumerates something and that nothing
// else carries a value list.
func validateEnumValues(p Param) error {
	if p.Type == ParamTypeEnum && len(p.Values) == 0 {
		return fmt.Errorf("enum parameter %q must list its allowed values", p.Name)
	}
	if p.Type != ParamTypeEnum && len(p.Values) > 0 {
		return fmt.Errorf("parameter %q is of type %s; only an enum parameter carries values", p.Name, p.Type)
	}
	return nil
}

// ErrUnknownParam marks a bind rejected because the caller supplied a name the
// script does not declare. It is a distinct error because the corrective action
// differs from a bad value: the caller is passing something the script will
// never read, which is nearly always a typo.
var ErrUnknownParam = errors.New("unknown parameter")

// BindParams checks caller-supplied values against the declared contract and
// returns the bound set: every declared parameter present exactly once, with
// defaults applied and each value coerced to its declared type. Undeclared
// names are refused rather than passed through, so a typo never reaches the
// script as a silently ignored argument.
//
// The result is what becomes the frozen run.params dict, which is why binding
// happens here — in the domain, once, before any interpreter is involved —
// rather than inside the engine.
func BindParams(defs []Param, values map[string]any) (map[string]any, error) {
	declared := make(map[string]bool, len(defs))
	for _, p := range defs {
		declared[p.Name] = true
	}
	for name := range values {
		if !declared[name] {
			return nil, fmt.Errorf("this script has no parameter %q (%w); it declares %s", name, ErrUnknownParam, paramNameList(defs))
		}
	}

	bound := make(map[string]any, len(defs))
	for _, p := range defs {
		v, err := bindOne(p, values)
		if err != nil {
			return nil, err
		}
		bound[p.Name] = v
	}
	return bound, nil
}

// bindOne resolves one declared parameter against the supplied values: the
// caller's value, else the declared default, else the typed zero for an
// optional parameter that has neither.
func bindOne(p Param, values map[string]any) (any, error) {
	raw, supplied := values[p.Name]
	if !supplied || raw == nil {
		switch {
		case p.Default != nil:
			raw = p.Default
		case p.Required:
			return nil, fmt.Errorf("parameter %q is required", p.Name)
		default:
			return zeroParam(p), nil
		}
	}
	v, err := coerceParam(p, raw)
	if err != nil {
		return nil, fmt.Errorf("parameter %q: %w", p.Name, err)
	}
	return v, nil
}

// paramNameList renders the declared parameter names for an error message.
func paramNameList(defs []Param) string {
	if len(defs) == 0 {
		return "no parameters"
	}
	names := make([]string, 0, len(defs))
	for _, p := range defs {
		names = append(names, strconv.Quote(p.Name))
	}
	return "parameters " + strings.Join(names, ", ")
}

// zeroParam is the value an optional parameter takes when the caller supplies
// nothing and the script declares no default. It is typed rather than None so
// a script never has to branch on absence: an omitted int reads as 0, an
// omitted string as "".
func zeroParam(p Param) any {
	switch p.Type {
	case ParamTypeInt:
		return int64(0)
	case ParamTypeFloat:
		return float64(0)
	case ParamTypeBool:
		return false
	default:
		return ""
	}
}

// coerceParam converts one supplied value to its declared type, accepting the
// forms JSON decoding actually produces (numbers arrive as float64, and a
// JSON-encoded schedule binding may carry a scalar as a string) and refusing
// everything else.
func coerceParam(p Param, raw any) (any, error) {
	switch p.Type {
	case ParamTypeString:
		return coerceString(raw)
	case ParamTypeEnum:
		return coerceEnum(p, raw)
	case ParamTypeInt:
		return coerceInt(raw)
	case ParamTypeFloat:
		return coerceFloat(raw)
	case ParamTypeBool:
		return coerceBool(raw)
	case ParamTypeDate:
		return coerceDate(raw)
	case ParamTypeConnection:
		return coerceConnection(raw)
	default:
		return nil, fmt.Errorf("invalid type %q", p.Type)
	}
}

// coerceConnection accepts a non-empty connection name. Emptiness is refused
// here rather than passed on because the grant refuses an unnamed connection
// too (Grants.AllowsConnection), and a value the run is certain to reject is
// worth rejecting while somebody is still looking at the form.
func coerceConnection(raw any) (any, error) {
	s, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("expected a connection name, got %T", raw)
	}
	if s == "" {
		return nil, errors.New("a connection name cannot be empty")
	}
	return s, nil
}

// coerceString accepts only a string; a number or bool spelled as a string
// parameter is a contract mismatch worth reporting.
func coerceString(raw any) (any, error) {
	s, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("expected a string, got %T", raw)
	}
	return s, nil
}

// coerceEnum accepts one of the declared values.
func coerceEnum(p Param, raw any) (any, error) {
	s, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("expected one of %v, got %T", p.Values, raw)
	}
	if !slices.Contains(p.Values, s) {
		return nil, fmt.Errorf("value %q is not one of %v", s, p.Values)
	}
	return s, nil
}

// coerceInt accepts an integer, a float that is exactly an integer (JSON
// decodes every number as float64), or a decimal string.
func coerceInt(raw any) (any, error) {
	switch v := raw.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		if v != float64(int64(v)) {
			return nil, fmt.Errorf("expected a whole number, got %v", v)
		}
		return int64(v), nil
	case string:
		n, err := strconv.ParseInt(v, decimalBase, bitSize64)
		if err != nil {
			return nil, fmt.Errorf("value %q is not a whole number", v)
		}
		return n, nil
	default:
		return nil, fmt.Errorf("expected a whole number, got %T", raw)
	}
}

// coerceFloat accepts any numeric form or a numeric string.
func coerceFloat(raw any) (any, error) {
	switch v := raw.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		f, err := strconv.ParseFloat(v, bitSize64)
		if err != nil {
			return nil, fmt.Errorf("value %q is not a number", v)
		}
		return f, nil
	default:
		return nil, fmt.Errorf("expected a number, got %T", raw)
	}
}

// coerceBool accepts a bool or the canonical string spellings.
func coerceBool(raw any) (any, error) {
	switch v := raw.(type) {
	case bool:
		return v, nil
	case string:
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("value %q is not true or false", v)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("expected true or false, got %T", raw)
	}
}

// coerceDate accepts a YYYY-MM-DD string and returns it normalized, so a date
// that reaches a script is always in the one layout the date module reads.
func coerceDate(raw any) (any, error) {
	s, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("expected a date as YYYY-MM-DD, got %T", raw)
	}
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return nil, fmt.Errorf("value %q is not a date in YYYY-MM-DD form", s)
	}
	return t.Format(DateLayout), nil
}
