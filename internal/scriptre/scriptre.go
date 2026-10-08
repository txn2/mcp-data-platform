// Package scriptre is the regular-expression matching a managed script is
// given, as a Starlark module (#2050).
//
// It is separate from the engine for the reason internal/scriptdate is: every
// function is a pure transformation of its arguments, there is no caller and
// no state but a per-run cache of compiled patterns, and the engine's only
// use of it is to predeclare the module.
//
// The engine is Go's regexp, which is RE2: matching is linear in the input,
// with no backtracking, so a pattern run over an untrusted page cannot spend
// a run's deadline on one match. The price is the two Python features RE2
// does not have, backreferences in a pattern and lookaround; a pattern using
// either is refused when it is compiled, naming both.
//
// The functions follow Python's re: their names, their arguments, and what
// they return, including findall's shape and split's captured groups.
// Offsets are byte offsets, which is what Starlark's own string indexing and
// slicing use, so s[m.start(1):m.end(1)] is m.group(1).
package scriptre

import (
	"fmt"
	"regexp"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// Name is the global the module is bound to.
const Name = "re"

// Flag values, as Python numbers them, so a ported re.I or re.IGNORECASE
// reads the same. Each is applied as the RE2 inline flag it names.
const (
	flagIgnoreCase = 2
	flagMultiline  = 8
	flagDotAll     = 16
)

// Module is the re module the engine predeclares.
var Module = &starlarkstruct.Module{
	Name: Name,
	Members: starlark.StringDict{
		"compile":    starlark.NewBuiltin(Name+".compile", compileFn),
		"search":     patternCall("search"),
		"match":      patternCall("match"),
		"fullmatch":  patternCall("fullmatch"),
		"findall":    patternCall("findall"),
		"finditer":   patternCall("finditer"),
		"sub":        patternCall("sub"),
		"split":      patternCall("split"),
		"escape":     starlark.NewBuiltin(Name+".escape", escapeFn),
		"I":          starlark.MakeInt(flagIgnoreCase),
		"IGNORECASE": starlark.MakeInt(flagIgnoreCase),
		"M":          starlark.MakeInt(flagMultiline),
		"MULTILINE":  starlark.MakeInt(flagMultiline),
		"S":          starlark.MakeInt(flagDotAll),
		"DOTALL":     starlark.MakeInt(flagDotAll),
	},
}

// BytesPerStep is how much input one interpreter step pays for. A match
// costs ceil(len(s)/BytesPerStep) steps on top of the call's own, so a
// script's step budget still bounds the text it scans, without a pattern
// over a 300 KB page costing more than the hand-written find() loop it
// replaces.
const BytesPerStep = 1024

// maxCachedPatterns bounds the per-run cache of compiled patterns. A script
// that builds patterns from data rather than writing them is the only one
// that reaches it, and past it a pattern is compiled on each use.
const maxCachedPatterns = 1024

// cacheKey is the thread-local slot the per-run cache lives in.
const cacheKey = "scriptre.cache"

// unsupportedHint is appended to a compile error, since the two features RE2
// lacks are what a pattern ported from Python or Java most often uses.
const unsupportedHint = "; re is RE2, which has no backreferences (\\1 in a pattern) and no lookaround ((?=, (?!, (?<=, (?<!)"

// charge adds the steps scanning n bytes costs to the thread's count. The
// interpreter checks the count against the run's limit at its next step.
func charge(thread *starlark.Thread, n int) {
	if thread != nil {
		thread.Steps += uint64(n/BytesPerStep) + 1 // #nosec G115 -- n is a string length, never negative
	}
}

// compiled returns the program for pattern under flags, from the run's cache
// when it was compiled before.
func compiled(thread *starlark.Thread, pattern string, flags int) (*regexp.Regexp, error) {
	source, err := withFlags(pattern, flags)
	if err != nil {
		return nil, err
	}
	return compileSource(thread, source, pattern)
}

// compileSource compiles source through the run's cache. pattern is what the
// script wrote, which an error names.
func compileSource(thread *starlark.Thread, source, pattern string) (*regexp.Regexp, error) {
	var cache map[string]*regexp.Regexp
	if thread != nil {
		cache, _ = thread.Local(cacheKey).(map[string]*regexp.Regexp)
		if cache == nil {
			cache = map[string]*regexp.Regexp{}
			thread.SetLocal(cacheKey, cache)
		}
		if re, ok := cache[source]; ok {
			return re, nil
		}
	}
	re, err := regexp.Compile(source)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern %q: %w%s", pattern, err, unsupportedHint)
	}
	if cache != nil && len(cache) < maxCachedPatterns {
		cache[source] = re
	}
	return re, nil
}

// withFlags prefixes pattern with the inline flags the flags argument names.
func withFlags(pattern string, flags int) (string, error) {
	if flags&^(flagIgnoreCase|flagMultiline|flagDotAll) != 0 {
		return "", fmt.Errorf("flags=%d names a flag re does not support; use re.I, re.M, re.S or their inline forms (?i), (?m), (?s)", flags)
	}
	var inline strings.Builder
	for _, f := range []struct {
		bit  int
		char byte
	}{{flagIgnoreCase, 'i'}, {flagMultiline, 'm'}, {flagDotAll, 's'}} {
		if flags&f.bit != 0 {
			_ = inline.WriteByte(f.char)
		}
	}
	if inline.Len() == 0 {
		return pattern, nil
	}
	return "(?" + inline.String() + ")" + pattern, nil
}

// Validate reports why pattern would be refused when a run compiles it, or
// nil. The authoring gates call it on a pattern written as a literal, so a
// pattern RE2 cannot compile is refused at save rather than mid-run.
func Validate(pattern string) error {
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("invalid pattern %q: %w%s", pattern, err, unsupportedHint)
	}
	return nil
}

// compileFn is re.compile(pattern, flags=0).
func compileFn(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var pattern string
	flags := 0
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "pattern", &pattern, "flags?", &flags); err != nil {
		return nil, fmt.Errorf("in %s: %w", b.Name(), err)
	}
	re, err := compiled(thread, pattern, flags)
	if err != nil {
		return nil, fmt.Errorf("in %s: %w", b.Name(), err)
	}
	return &Pattern{pattern: pattern, flags: flags, re: re}, nil
}

// escapeFn is re.escape(s): s with every metacharacter escaped.
func escapeFn(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var s string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "s", &s); err != nil {
		return nil, fmt.Errorf("in %s: %w", b.Name(), err)
	}
	return starlark.String(regexp.QuoteMeta(s)), nil
}

// patternCall is re.<method>(pattern, ...): the pattern compiled (or taken
// from the run's cache) and the same method of a compiled Pattern called on
// the rest of the arguments. flags= is the module function's alone.
func patternCall(method string) *starlark.Builtin {
	return starlark.NewBuiltin(Name+"."+method, func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("in %s: missing argument for pattern", b.Name())
		}
		pattern, ok := starlark.AsString(args[0])
		if !ok {
			return nil, fmt.Errorf("in %s: pattern must be a string, not %s", b.Name(), args[0].Type())
		}
		flags, rest, err := takeFlags(kwargs)
		if err != nil {
			return nil, fmt.Errorf("in %s: %w", b.Name(), err)
		}
		re, err := compiled(thread, pattern, flags)
		if err != nil {
			return nil, fmt.Errorf("in %s: %w", b.Name(), err)
		}
		p := &Pattern{pattern: pattern, flags: flags, re: re}
		return p.call(thread, method, b.Name(), args[1:], rest)
	})
}

// takeFlags removes flags= from kwargs.
func takeFlags(kwargs []starlark.Tuple) (int, []starlark.Tuple, error) {
	rest := make([]starlark.Tuple, 0, len(kwargs))
	flags := 0
	for _, kv := range kwargs {
		if key, _ := kv[0].(starlark.String); key != "flags" {
			rest = append(rest, kv)
			continue
		}
		if err := starlark.AsInt(kv[1], &flags); err != nil {
			return 0, nil, fmt.Errorf("flags must be an int: %w", err)
		}
	}
	return flags, rest, nil
}
