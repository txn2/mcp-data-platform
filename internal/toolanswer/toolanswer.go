// Package toolanswer holds a managed script's declared answers to what the
// tools answer (#1953). A test that declares the answer a call gets
// (testing.answer) is checked here against the tool's answer contract
// (toolkit.AnswerContract), so a test cannot pass by relying on a shape the
// tool never returns: an answer lacking a field the tool always returns, a
// field of the wrong type, or a key a nested object never carries is refused,
// naming the field.
//
// The top level of an answer admits keys the contract does not name, because
// the platform adds its own there after the tool returns (the call reference,
// the enrichment blocks).
package toolanswer

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Set is the answer contracts a deployment's tools declare.
type Set struct {
	byTool map[string][]toolkit.AnswerContract
}

// New is the set of the given contracts.
func New(contracts ...toolkit.AnswerContract) *Set {
	s := &Set{byTool: map[string][]toolkit.AnswerContract{}}
	for _, c := range contracts {
		if c.Tool != "" && c.Schema != nil {
			s.byTool[c.Tool] = append(s.byTool[c.Tool], c)
		}
	}
	return s
}

// Check holds answer, declared for a call to tool with args, to the tool's
// contract. checked is false when no contract covers the call, and the answer
// was not checked.
func (s *Set) Check(tool string, args, answer map[string]any) (checked bool, err error) {
	c, ok := s.contractFor(tool, args)
	if !ok {
		return false, nil
	}
	found := problems(c.Schema, answer, "", true)
	if len(found) == 0 {
		return true, nil
	}
	return true, fmt.Errorf("the answer declared for %s %s; the tool never answers that way", describe(c, args), strings.Join(found, ", "))
}

func (s *Set) contractFor(tool string, args map[string]any) (toolkit.AnswerContract, bool) {
	if s == nil {
		return toolkit.AnswerContract{}, false
	}
	for _, c := range s.byTool[tool] {
		if c.Arg == "" {
			return c, true
		}
		v, _ := args[c.Arg].(string)
		if slices.Contains(c.Values, v) {
			return c, true
		}
	}
	return toolkit.AnswerContract{}, false
}

// describe names the calls a contract covers, as the author wrote them.
func describe(c toolkit.AnswerContract, args map[string]any) string {
	if c.Arg == "" {
		return c.Tool
	}
	if v, ok := args[c.Arg].(string); ok && v != "" {
		return fmt.Sprintf("%s (%s=%s)", c.Tool, c.Arg, v)
	}
	return c.Tool
}

// problems is every way v departs from s, each naming the field at path.
func problems(s *jsonschema.Schema, v any, path string, top bool) []string {
	if s == nil || s.Ref != "" {
		return nil
	}
	if kinds := typesOf(s); len(kinds) > 0 && !admits(kinds, kindOf(v)) {
		return []string{fmt.Sprintf("has %s as %s, where the tool answers %s", field(path), kindOf(v), strings.Join(kinds, " or "))}
	}
	switch v := v.(type) {
	case map[string]any:
		return objectProblems(s, v, path, top)
	case []any:
		var out []string
		for i, item := range v {
			out = append(out, problems(s.Items, item, fmt.Sprintf("%s[%d]", path, i), false)...)
		}
		return out
	}
	return nil
}

func objectProblems(s *jsonschema.Schema, v map[string]any, path string, top bool) []string {
	var out []string
	var missing []string
	for _, name := range s.Required {
		if _, ok := v[name]; !ok {
			missing = append(missing, field(join(path, name)))
		}
	}
	if len(missing) > 0 {
		out = append(out, "lacks "+strings.Join(missing, ", "))
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		prop, ok := s.Properties[k]
		switch {
		case ok:
			out = append(out, problems(prop, v[k], join(path, k), false)...)
		case !top && closed(s):
			out = append(out, "has "+field(join(path, k))+", which the tool never returns")
		}
	}
	return out
}

// closed reports whether s admits no key it does not name: the
// additionalProperties false a struct-derived schema carries.
func closed(s *jsonschema.Schema) bool {
	ap := s.AdditionalProperties
	return ap != nil && ap.Not != nil && ap.Not.Type == "" && len(ap.Not.Types) == 0 && ap.Not.Properties == nil
}

// admits reports whether a value of kind is one of kinds; a whole number is
// also a number.
func admits(kinds []string, kind string) bool {
	return slices.Contains(kinds, kind) || (kind == "integer" && slices.Contains(kinds, "number"))
}

func typesOf(s *jsonschema.Schema) []string {
	if s.Type != "" {
		return []string{s.Type}
	}
	return s.Types
}

// kindOf is the JSON Schema type of a decoded JSON value.
func kindOf(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if v == math.Trunc(v) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func field(path string) string { return fmt.Sprintf("%q", path) }

// Lister is the live toolkit registry, which gains and loses toolkits as
// connections are added and removed.
type Lister interface {
	All() []registry.Toolkit
}

// Live is the contracts of the toolkits a registry holds at the moment a
// declared answer is checked, and Extra's: the contracts of the tools the
// platform registers outside any toolkit (notify). A nil registry holds none.
type Live struct {
	Kits  Lister
	Extra []toolkit.AnswerContract
}

// Check is Set.Check over the registry's toolkits now and Extra.
func (l Live) Check(tool string, args, answer map[string]any) (bool, error) {
	var kits []registry.Toolkit
	if l.Kits != nil {
		kits = l.Kits.All()
	}
	all := slices.Clone(l.Extra)
	for _, k := range kits {
		if c, ok := k.(toolkit.AnswerContractor); ok {
			all = append(all, c.AnswerContracts()...)
		}
	}
	return New(all...).Check(tool, args, answer)
}
