// Package jsonpath reads one value out of a decoded JSON document by a path an
// operator writes: "$" for the document itself, ".name" or "['name']" for an
// object member, and "[n]" for an array element, as in "$.data.events" or
// "$.contact['id']" (#1870).
//
// It is the subset of JSONPath that addresses one value. There are no
// wildcards, filters or recursive descent: a webhook source uses a path to
// find its event array, and an event's id, type and key, and each of those is
// one place in the document.
package jsonpath

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// step is one member name or one array index.
type step struct {
	name  string
	index int
	isIdx bool
}

// Path is a parsed path.
type Path struct {
	steps []step
	text  string
}

// String is the path as it was written.
func (p Path) String() string { return p.text }

// Parse reads a path. The leading "$" may be omitted: "data.id" is "$.data.id".
func Parse(text string) (Path, error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return Path{}, errors.New("path is empty")
	}
	rest := strings.TrimPrefix(s, "$")
	if rest != "" && rest[0] != '.' && rest[0] != '[' {
		rest = "." + rest
	}
	var steps []step
	for rest != "" {
		st, tail, err := nextStep(rest)
		if err != nil {
			return Path{}, fmt.Errorf("path %q: %w", text, err)
		}
		steps = append(steps, st)
		rest = tail
	}
	return Path{steps: steps, text: s}, nil
}

// nextStep reads the step at the front of rest and returns what follows it.
func nextStep(rest string) (step, string, error) {
	if rest[0] == '.' {
		return memberStep(rest[1:])
	}
	return bracketStep(rest)
}

// memberStep reads ".name", given what follows the dot.
func memberStep(rest string) (step, string, error) {
	end := strings.IndexAny(rest, ".[")
	name, tail := rest, ""
	if end >= 0 {
		name, tail = rest[:end], rest[end:]
	}
	if name == "" {
		return step{}, "", errors.New("empty member name")
	}
	return step{name: name}, tail, nil
}

// bracketStep reads "['name']", "[\"name\"]" or "[n]".
func bracketStep(rest string) (step, string, error) {
	closing := strings.IndexByte(rest, ']')
	if closing < 0 {
		return step{}, "", errors.New("unclosed [")
	}
	inner, tail := rest[1:closing], rest[closing+1:]
	if len(inner) >= 2 && (inner[0] == '\'' || inner[0] == '"') && inner[len(inner)-1] == inner[0] {
		return step{name: inner[1 : len(inner)-1]}, tail, nil
	}
	n, err := strconv.Atoi(inner)
	if err != nil || n < 0 {
		return step{}, "", fmt.Errorf("step [%s] is neither a quoted member name nor an array index", inner)
	}
	return step{index: n, isIdx: true}, tail, nil
}

// Lookup returns the value the path addresses in doc, and false when any step
// is missing or of the wrong kind.
func (p Path) Lookup(doc any) (any, bool) {
	cur := doc
	for _, st := range p.steps {
		if st.isIdx {
			arr, ok := cur.([]any)
			if !ok || st.index >= len(arr) {
				return nil, false
			}
			cur = arr[st.index]
			continue
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = obj[st.name]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// Scalar renders a looked-up value as the text a column stores: a string as
// itself, a number as written, true and false as words, null as empty, and an
// object or array as its compact JSON.
func Scalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
