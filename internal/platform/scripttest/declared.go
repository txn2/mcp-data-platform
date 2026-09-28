package scripttest

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
)

// Contracts holds a declared answer to what its tool always answers
// (internal/toolanswer). checked is false when no contract covers the call.
type Contracts interface {
	Check(tool string, args, answer map[string]any) (checked bool, err error)
}

// declared is the answers one test declared with testing.answer (#1953). It
// is consulted before the recording, in the order the answers were declared,
// and each answers one call: the first unused answer for the call's tool whose
// arguments are all among the call's.
type declared struct {
	answers   []declaredAnswer
	contracts Contracts
	// alter, when set, is applied to the rows of every answer declared, as
	// the altered-rows check applies it to a recording's query results.
	alter func([]any) []any
	// rows is true once an answer carrying rows was declared: data the
	// altered-rows check holds the test to reading.
	rows bool
	// unchecked is the tools an answer was declared for that declare no
	// answer contract, in the order first declared.
	unchecked []string
}

type declaredAnswer struct {
	tool    string
	args    map[string]any
	out     map[string]any
	errText string
	used    bool
}

// Answer implements scriptrec.Answerer.
func (d *declared) Answer(tool string, args map[string]any) (out map[string]any, errText string, ok bool) {
	for i := range d.answers {
		a := &d.answers[i]
		if a.used || a.tool != tool || !within(a.args, args) {
			continue
		}
		a.used = true
		return a.out, a.errText, true
	}
	return nil, "", false
}

// within reports whether every key of want is in got with an equal value, as
// the two read after a JSON round trip.
func within(want, got map[string]any) bool {
	norm := jsonMap(got)
	for k, v := range jsonMap(want) {
		gv, ok := norm[k]
		if !ok || !reflect.DeepEqual(v, gv) {
			return false
		}
	}
	return true
}

// jsonMap is m as JSON decodes it, so a Starlark int and a recorded float64
// compare equal.
func jsonMap(m map[string]any) map[string]any {
	data, err := json.Marshal(m)
	if err != nil {
		return m
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return m
	}
	return out
}

// answerBuiltin is testing.answer(tool, args, answer=None, error=""): the
// answer a call to tool whose arguments include every key and value of args
// gets, or with error= the failure it gets instead.
func (d *declared) answerBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		tool     string
		callArgs *starlark.Dict
		answer   starlark.Value = starlark.None
		errText  string
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "tool", &tool, "args", &callArgs, "answer?", &answer, "error?", &errText); err != nil {
		return nil, err //nolint:wrapcheck // the interpreter's message names the builtin
	}
	_, isNone := answer.(starlark.NoneType)
	if isNone == (errText == "") {
		return nil, fmt.Errorf("in %s: declare an answer or an error=, one of the two", b.Name())
	}
	a := declaredAnswer{tool: tool, errText: errText}
	var err error
	if a.args, err = dictArg(callArgs); err != nil {
		return nil, fmt.Errorf("in %s: args: %w", b.Name(), err)
	}
	if !isNone {
		dict, ok := answer.(*starlark.Dict)
		if !ok {
			return nil, fmt.Errorf("in %s: answer is a dict, as the tool answers, not %s", b.Name(), answer.Type())
		}
		if a.out, err = dictArg(dict); err != nil {
			return nil, fmt.Errorf("in %s: answer: %w", b.Name(), err)
		}
		if err := d.hold(tool, a.args, a.out); err != nil {
			return nil, fmt.Errorf("in %s: %w", b.Name(), err)
		}
	}
	d.answers = append(d.answers, a)
	return starlark.None, nil
}

// hold checks one declared answer against its tool's contract and applies the
// altered-rows check's alteration to its rows.
func (d *declared) hold(tool string, args, out map[string]any) error {
	checked := false
	if d.contracts != nil {
		var err error
		if checked, err = d.contracts.Check(tool, args, out); err != nil {
			return err //nolint:wrapcheck // the contract names the tool and the field
		}
	}
	if !checked && !slices.Contains(d.unchecked, tool) {
		d.unchecked = append(d.unchecked, tool)
	}
	rows, ok := out["rows"].([]any)
	if !ok || len(rows) == 0 {
		return nil
	}
	d.rows = true
	if d.alter != nil {
		out["rows"] = d.alter(rows)
	}
	return nil
}

// setRunBuiltin is testing.set_run(params=None, state=None): what run.params
// and run.state read for the rest of the test, in place of the recording's.
func setRunBuiltin(in *scriptrun.TestInputs) *starlark.Builtin {
	return starlark.NewBuiltin("testing.set_run", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var params, state *starlark.Dict
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "params?", &params, "state?", &state); err != nil {
			return nil, err //nolint:wrapcheck // the interpreter's message names the builtin
		}
		var err error
		if params != nil {
			if in.Params, err = dictArg(params); err != nil {
				return nil, fmt.Errorf("in %s: params: %w", b.Name(), err)
			}
		}
		if state != nil {
			if in.State, err = dictArg(state); err != nil {
				return nil, fmt.Errorf("in %s: state: %w", b.Name(), err)
			}
		}
		in.Set = true
		return starlark.None, nil
	})
}

// dictArg is a Starlark dict as JSON decodes it.
func dictArg(d *starlark.Dict) (map[string]any, error) {
	if d == nil {
		return map[string]any{}, nil
	}
	v, err := starlarkconv.FromStarlark(d)
	if err != nil {
		return nil, err //nolint:wrapcheck // the conversion names the value
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("keys are strings, as a tool's arguments are")
	}
	return jsonMap(m), nil
}
