package scripttest

import (
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"
)

// AssertionError is an assertion that did not hold: what it expected, and the line of
// the test that asserted it.
type AssertionError struct {
	Message string
	Line    int
}

func (f *AssertionError) Error() string { return f.Message }

// assertModule is the assert module a test is handed. Each assertion fails the
// test at the line that made it, saying what it got and what it expected, and
// counts itself in made: a test that asserts nothing proves nothing.
func assertModule(made, failures *int) *starlarkstruct.Module {
	counted := func(name string, fn func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error)) *starlark.Builtin {
		return starlark.NewBuiltin(name, func(th *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			*made++
			return fn(th, b, args, kwargs)
		})
	}
	return &starlarkstruct.Module{Name: "assert", Members: starlark.StringDict{
		"eq":       counted("assert.eq", assertEq),
		"ne":       counted("assert.ne", assertNe),
		"true":     counted("assert.true", assertTrue),
		"contains": counted("assert.contains", assertContains),
		"fails": counted("assert.fails", func(th *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			v, err := assertFails(th, b, args, kwargs)
			// Only a failure of the run itself -- assert.fails(main) -- counts:
			// a test that makes any function fail is not thereby a test of
			// the script's failure.
			if fn, ok := firstCallable(args); err == nil && ok && fn.Name() == scriptdialect.EntryPointName {
				*failures++
			}
			return v, err
		}),
	}}
}

// msgArg is the optional message every comparison takes.
const msgArg = "msg?"

// fail is the AssertionError an assertion raises, at the line of the frame that
// called it.
func fail(thread *starlark.Thread, msg, format string, args ...any) error {
	text := fmt.Sprintf(format, args...)
	if msg != "" {
		text = msg + ": " + text
	}
	line := 0
	if thread.CallStackDepth() > 1 {
		line = int(thread.CallFrame(1).Pos.Line)
	}
	return &AssertionError{Message: text, Line: line}
}

func assertEq(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var got, want starlark.Value
	var msg string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "got", &got, "want", &want, msgArg, &msg); err != nil {
		return nil, err //nolint:wrapcheck // the interpreter's message names the builtin
	}
	eq, err := starlark.Equal(got, want)
	if err != nil {
		return nil, fmt.Errorf("in %s: %w", b.Name(), err)
	}
	if !eq {
		return nil, fail(thread, msg, "%s: got %s, want %s", b.Name(), got, want)
	}
	return starlark.None, nil
}

func assertNe(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var got, other starlark.Value
	var msg string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "got", &got, "other", &other, msgArg, &msg); err != nil {
		return nil, err //nolint:wrapcheck // the interpreter's message names the builtin
	}
	eq, err := starlark.Equal(got, other)
	if err != nil {
		return nil, fmt.Errorf("in %s: %w", b.Name(), err)
	}
	if eq {
		return nil, fail(thread, msg, "%s: both are %s", b.Name(), got)
	}
	return starlark.None, nil
}

func assertTrue(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var cond starlark.Value
	var msg string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "cond", &cond, msgArg, &msg); err != nil {
		return nil, err //nolint:wrapcheck // the interpreter's message names the builtin
	}
	if !cond.Truth() {
		return nil, fail(thread, msg, "%s: %s is not true", b.Name(), cond)
	}
	return starlark.None, nil
}

func assertContains(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var container, item starlark.Value
	var msg string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "container", &container, "item", &item, msgArg, &msg); err != nil {
		return nil, err //nolint:wrapcheck // the interpreter's message names the builtin
	}
	in, err := starlark.Binary(syntax.IN, item, container)
	if err != nil {
		return nil, fmt.Errorf("in %s: %w", b.Name(), err)
	}
	if !in.Truth() {
		return nil, fail(thread, msg, "%s: %s does not contain %s", b.Name(), container, item)
	}
	return starlark.None, nil
}

// assertFails calls fn with the rest of the arguments and returns the message
// it failed with, failing the test when it succeeds.
func assertFails(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("in %s: missing the function to call", b.Name())
	}
	fn, ok := args[0].(starlark.Callable)
	if !ok {
		return nil, fmt.Errorf("in %s: %s is not callable", b.Name(), args[0].Type())
	}
	_, err := starlark.Call(thread, fn, args[1:], kwargs)
	if err == nil {
		return nil, fail(thread, "", "%s: %s did not fail", b.Name(), fn.Name())
	}
	var evalErr *starlark.EvalError
	if errors.As(err, &evalErr) {
		return starlark.String(evalErr.Msg), nil
	}
	return starlark.String(err.Error()), nil
}

// firstCallable is the function an assert.fails call was handed.
func firstCallable(args starlark.Tuple) (starlark.Callable, bool) {
	if len(args) == 0 {
		return nil, false
	}
	fn, ok := args[0].(starlark.Callable)
	return fn, ok
}
