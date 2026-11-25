package toolhelp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// argumentReport is what a call's arguments got wrong against a tool's
// schema, and what the tool accepts.
type argumentReport struct {
	tool       string
	unexpected []unexpectedArgument
	mismatches []string
	accepted   []string
}

// unexpectedArgument is one property the schema does not declare, with the
// accepted name it most likely meant.
type unexpectedArgument struct {
	name    string
	closest string
}

// describeArguments checks a call's arguments against the tool's schema and
// reports every property it does not declare, every value whose JSON type or
// enum it does not admit, and the parameters it does accept. ok is false when
// the schema declares no properties to compare against.
func describeArguments(tool string, schema map[string]any, rawArgs json.RawMessage) (argumentReport, bool) {
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return argumentReport{}, false
	}
	required := stringSet(schema["required"])
	var args map[string]any
	if len(rawArgs) > 0 {
		_ = json.Unmarshal(rawArgs, &args)
	}
	missing := missingRequired(required, args)

	r := argumentReport{tool: tool, accepted: acceptedParameters(props, required)}
	for _, name := range sortedKeys(args) {
		prop, declared := props[name].(map[string]any)
		if !declared {
			if _, present := props[name]; !present {
				r.unexpected = append(r.unexpected, unexpectedArgument{name: name, closest: closestName(name, props, missing)})
			}
			continue
		}
		if m := mismatch(name, prop, args[name]); m != "" {
			r.mismatches = append(r.mismatches, m)
		}
	}
	return r, true
}

// messageOver is the refusal sentence over the validator's own: its faults
// when it found any at the top level, else the validator's message (a fault
// inside a nested object, which it does not describe), then what the tool
// accepts either way.
func (r argumentReport) messageOver(original string) string {
	if len(r.unexpected) > 0 || len(r.mismatches) > 0 {
		return r.message()
	}
	return strings.TrimSpace(original) + " " + r.message()
}

// message is the refusal sentence: each fault, then what the tool accepts.
func (r argumentReport) message() string {
	var b strings.Builder
	for _, u := range r.unexpected {
		fmt.Fprintf(&b, "%s does not take %q", r.tool, u.name)
		if u.closest != "" {
			fmt.Fprintf(&b, "; did you mean %q?", u.closest)
		} else {
			_, _ = b.WriteString(".")
		}
		_, _ = b.WriteString(" ")
	}
	for _, m := range r.mismatches {
		_, _ = b.WriteString(m)
		_, _ = b.WriteString(" ")
	}
	fmt.Fprintf(&b, "%s accepts: %s.", r.tool, strings.Join(r.accepted, ", "))
	return b.String()
}

// acceptedParameters lists each declared property as `name (type)`, required
// ones first and marked, the rest in name order.
func acceptedParameters(props map[string]any, required map[string]bool) []string {
	names := sortedKeys(props)
	sort.SliceStable(names, func(i, j int) bool { return required[names[i]] && !required[names[j]] })
	out := make([]string, 0, len(names))
	for _, name := range names {
		prop, _ := props[name].(map[string]any)
		desc := name + " (" + typeOf(prop)
		if required[name] {
			desc += ", required"
		}
		out = append(out, desc+")")
	}
	return out
}

// mismatch describes a value the property's declared type or enum does not
// admit, or "" when it is admitted or the schema says nothing to check.
func mismatch(name string, prop map[string]any, value any) string {
	if enum, ok := prop["enum"].([]any); ok && len(enum) > 0 && !slices.ContainsFunc(enum, func(e any) bool { return equalJSON(e, value) }) {
		valid := make([]string, 0, len(enum))
		for _, e := range enum {
			valid = append(valid, fmt.Sprint(e))
		}
		return fmt.Sprintf("%q is not one of %s's values; it takes one of: %s.", fmt.Sprint(value), name, strings.Join(valid, ", "))
	}
	want := declaredTypes(prop)
	if len(want) == 0 {
		return ""
	}
	got := jsonType(value)
	for _, w := range want {
		if w == got || (w == "number" && got == "integer") {
			return ""
		}
	}
	return fmt.Sprintf("%s must be %s; the call sent %s.", name, strings.Join(want, " or "), got)
}

// closestName is the accepted property a misnamed one most likely meant: one
// within a small edit distance (a typo, a case difference), else the one
// required property the call left out, which is what a caller who used
// another tool's name for it (query for search's intent) was sending.
func closestName(name string, props map[string]any, missing []string) string {
	best, bestDist := "", -1
	for candidate := range props {
		d := editDistance(strings.ToLower(name), strings.ToLower(candidate))
		if d <= max(1, len(candidate)/3) && (bestDist < 0 || d < bestDist || (d == bestDist && candidate < best)) {
			best, bestDist = candidate, d
		}
	}
	if best != "" {
		return best
	}
	if len(missing) == 1 {
		return missing[0]
	}
	return describedAs(name, props)
}

// describedAs is the one property whose description names the unknown word
// ("query" for search's intent, described as the query), "" when none or more
// than one does.
func describedAs(name string, props map[string]any) string {
	word := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(name) + `\b`)
	found := ""
	for candidate, p := range props {
		desc, _ := asMap(p)["description"].(string)
		if !word.MatchString(desc) {
			continue
		}
		if found != "" {
			return ""
		}
		found = candidate
	}
	return found
}

// missingRequired is the required properties the call did not send, sorted.
func missingRequired(required map[string]bool, args map[string]any) []string {
	var out []string
	for name := range required {
		if _, sent := args[name]; !sent {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// declaredTypes reads a property's "type", a string or a list of strings.
func declaredTypes(prop map[string]any) []string {
	switch t := prop["type"].(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, v := range t {
			if s, ok := v.(string); ok && s != "null" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// typeOf is a property's declared type for the accepted list, "any" when it
// declares none.
func typeOf(prop map[string]any) string {
	if t := declaredTypes(prop); len(t) > 0 {
		return strings.Join(t, "|")
	}
	return "any"
}

// jsonType names the JSON type of a decoded value.
func jsonType(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		if x == float64(int64(x)) {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

// equalJSON compares two decoded JSON scalars.
func equalJSON(a, b any) bool {
	return fmt.Sprint(a) == fmt.Sprint(b) && jsonType(a) == jsonType(b)
}

// stringSet reads a JSON array of strings into a set.
func stringSet(v any) map[string]bool {
	out := map[string]bool{}
	list, _ := v.([]any)
	for _, s := range list {
		if name, ok := s.(string); ok {
			out[name] = true
		}
	}
	return out
}

// sortedKeys is a map's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// editDistance is the Levenshtein distance between two strings.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, 0, len(rb))
	for j := 0; j <= len(rb); j++ {
		prev = append(prev, j)
	}
	for i := 1; i <= len(ra); i++ {
		cur := append(make([]int, 0, len(prev)), i)
		cur = append(cur, make([]int, len(rb))...)
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}
