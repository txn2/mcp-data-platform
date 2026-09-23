package script

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

// Form metadata bounds (#1844). A label or group is a heading on a form, not
// documentation; the description is the help text.
const (
	maxParamLabelLen   = 200
	maxParamPatternLen = 500
	// maxListItems bounds a list parameter's length. A value set larger than
	// this is a table, and a table belongs in a queried source.
	maxListItems = 1000
)

// listItemTypes are the element types a list parameter may carry.
var listItemTypes = map[string]bool{
	ParamTypeString: true, ParamTypeInt: true, ParamTypeFloat: true,
	ParamTypeDate: true, ParamTypeEnum: true,
}

// validateFormShape checks the list, range and form-metadata fields of one
// parameter: each is used only on the types it means something for, and each
// constraint is itself well formed.
func validateFormShape(p Param) error {
	if err := validateListShape(p); err != nil {
		return err
	}
	if len(p.Label) > maxParamLabelLen || len(p.Group) > maxParamLabelLen {
		return fmt.Errorf("parameter %q: label and group are at most %d characters", p.Name, maxParamLabelLen)
	}
	if err := validateBind(p); err != nil {
		return err
	}
	elem := elementType(p)
	if p.Pattern != "" {
		if elem != ParamTypeString {
			return fmt.Errorf("parameter %q: pattern applies to a string or a list of string", p.Name)
		}
		if len(p.Pattern) > maxParamPatternLen {
			return fmt.Errorf("parameter %q: pattern is at most %d characters", p.Name, maxParamPatternLen)
		}
		if _, err := regexp.Compile(p.Pattern); err != nil {
			return fmt.Errorf("parameter %q: pattern does not compile: %w", p.Name, err)
		}
	}
	return validateBounds(p, elem)
}

// callerBindPattern is the one form a bind takes: caller. and a claim name,
// dotted for a nested claim.
var callerBindPattern = regexp.MustCompile(`^caller\.[A-Za-z_][A-Za-z0-9_-]*(\.[A-Za-z_][A-Za-z0-9_-]*)*$`)

// validateBind checks a caller-bound parameter: the bind names a claim, the
// value is a scalar a claim can carry, and nothing supplies it but the caller.
func validateBind(p Param) error {
	if p.Bind == "" {
		return nil
	}
	if len(p.Bind) > maxParamLabelLen || !callerBindPattern.MatchString(p.Bind) {
		return fmt.Errorf("parameter %q: bind is caller.<claim>, for example caller.tenant", p.Name)
	}
	switch p.Type {
	case ParamTypeString, ParamTypeInt, ParamTypeFloat, ParamTypeEnum, ParamTypeDate:
	default:
		return fmt.Errorf("parameter %q: a caller-bound parameter is a string, int, float, enum or date", p.Name)
	}
	if p.Default != nil {
		return fmt.Errorf("parameter %q: a caller-bound parameter takes no default; its value is the caller's", p.Name)
	}
	return nil
}

// validateListShape checks Items, MinItems and MaxItems.
func validateListShape(p Param) error {
	if p.Type != ParamTypeList {
		if p.Items != "" || p.MinItems != nil || p.MaxItems != nil {
			return fmt.Errorf("parameter %q is of type %s; items, min_items and max_items belong to a list", p.Name, p.Type)
		}
		return nil
	}
	if !listItemTypes[p.Items] {
		return fmt.Errorf("list parameter %q must name its element type in items: string, int, float, date or enum", p.Name)
	}
	lo, hi := listBounds(p)
	if lo < 0 || hi < 1 || hi > maxListItems || lo > hi {
		return fmt.Errorf("list parameter %q: min_items and max_items must satisfy 0 <= min_items <= max_items <= %d", p.Name, maxListItems)
	}
	return nil
}

// listBounds is a list's length bounds, its declared ones or the defaults.
func listBounds(p Param) (lo, hi int) {
	lo, hi = 0, maxListItems
	if p.MinItems != nil {
		lo = *p.MinItems
	}
	if p.MaxItems != nil {
		hi = *p.MaxItems
	}
	return lo, hi
}

// validateBounds checks that Min and Max are values of the parameter's own
// type and in order. They apply to numbers and dates, and to a range's ends.
func validateBounds(p Param, elem string) error {
	if p.Min == nil && p.Max == nil {
		return nil
	}
	switch elem {
	case ParamTypeInt, ParamTypeFloat, ParamTypeDate:
	default:
		return fmt.Errorf("parameter %q: min and max apply to a number or a date", p.Name)
	}
	bounds, err := coerceBounds(p, elem)
	if err != nil {
		return err
	}
	if bounds[0] != nil && bounds[1] != nil && compare(bounds[0], bounds[1]) > 0 {
		return fmt.Errorf("parameter %q: min is greater than max", p.Name)
	}
	return nil
}

// coerceBounds reads Min and Max as values of elem, nil where unset.
func coerceBounds(p Param, elem string) ([2]any, error) {
	unbounded := Param{Name: p.Name, Type: elem}
	var bounds [2]any
	for i, raw := range []any{p.Min, p.Max} {
		if raw == nil {
			continue
		}
		v, err := coerceScalar(unbounded, elem, raw)
		if err != nil {
			return bounds, fmt.Errorf("parameter %q: %s bound: %w", p.Name, [2]string{"min", "max"}[i], err)
		}
		bounds[i] = v
	}
	return bounds, nil
}

// elementType is the scalar type a parameter's constraints apply to: a
// list's element type, a date range's date, or the parameter's own type.
func elementType(p Param) string {
	switch p.Type {
	case ParamTypeList:
		return p.Items
	case ParamTypeDateRange:
		return ParamTypeDate
	default:
		return p.Type
	}
}

// checkConstraints applies Min, Max and Pattern to one coerced value of typ.
func checkConstraints(p Param, typ string, v any) error {
	if p.Pattern != "" && typ == ParamTypeString {
		s, _ := v.(string)
		if !regexp.MustCompile(`^(?:` + p.Pattern + `)$`).MatchString(s) {
			return fmt.Errorf("value %q does not match the pattern %s", s, p.Pattern)
		}
	}
	return checkRange(p, typ, v)
}

// checkRange applies Min and Max to one coerced value of typ.
func checkRange(p Param, typ string, v any) error {
	if p.Min == nil && p.Max == nil {
		return nil
	}
	unbounded := Param{Name: p.Name, Type: typ}
	if p.Min != nil {
		if lo, err := coerceScalar(unbounded, typ, p.Min); err == nil && compare(v, lo) < 0 {
			return fmt.Errorf("value %v is below the minimum %v", v, lo)
		}
	}
	if p.Max != nil {
		if hi, err := coerceScalar(unbounded, typ, p.Max); err == nil && compare(v, hi) > 0 {
			return fmt.Errorf("value %v is above the maximum %v", v, hi)
		}
	}
	return nil
}

// compare orders two coerced values of one type: int64, float64, or a
// YYYY-MM-DD string, whose lexical order is its date order.
func compare(a, b any) int {
	switch x := a.(type) {
	case int64:
		y, _ := b.(int64)
		return cmp.Compare(x, y)
	case float64:
		y, _ := b.(float64)
		return cmp.Compare(x, y)
	case string:
		y, _ := b.(string)
		return cmp.Compare(x, y)
	default:
		return 0
	}
}

// coerceList binds a list parameter: a JSON array whose every element is a
// value of Items, within MinItems and MaxItems. A refusal names the element,
// so a typo in one id among twenty is found without guessing.
func coerceList(p Param, raw any) (any, error) {
	items, err := listItems(raw)
	if err != nil {
		return nil, err
	}
	lo, hi := listBounds(p)
	if len(items) < lo || len(items) > hi {
		return nil, fmt.Errorf("expected between %d and %d values, got %d", lo, hi, len(items))
	}
	out := make([]any, 0, len(items))
	for i, item := range items {
		v, err := coerceScalar(p, p.Items, item)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// listItems reads a JSON array in the forms decoding and callers produce.
func listItems(raw any) ([]any, error) {
	switch v := raw.(type) {
	case []any:
		return v, nil
	case []string:
		out := make([]any, 0, len(v))
		for _, s := range v {
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected a list of values, got %T", raw)
	}
}

// errRangeOrder is the refusal of a range whose ends are reversed.
var errRangeOrder = errors.New("from is after to")

// coerceDateRange binds {"from": date, "to": date} with from on or before
// to. Each end is a date and takes the parameter's min and max.
func coerceDateRange(p Param, raw any) (any, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf(`expected {"from": "YYYY-MM-DD", "to": "YYYY-MM-DD"}, got %T`, raw)
	}
	for key := range m {
		if key != "from" && key != "to" {
			return nil, fmt.Errorf("a date range has from and to, not %s", strconv.Quote(key))
		}
	}
	ends := make(map[string]any, 2)
	for _, key := range []string{"from", "to"} {
		v, err := coerceScalar(p, ParamTypeDate, m[key])
		if err != nil {
			return nil, fmt.Errorf("date range %s: %w", key, err)
		}
		ends[key] = v
	}
	if compare(ends["from"], ends["to"]) > 0 {
		return nil, fmt.Errorf("date range from %v is after to %v: %w", ends["from"], ends["to"], errRangeOrder)
	}
	return ends, nil
}
