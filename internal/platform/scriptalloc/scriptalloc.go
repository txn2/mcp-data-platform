// Package scriptalloc routes the operations that can build a large value in
// one interpreter step through checks against the run's memory budget, so the
// value that would take a run past it is refused before it is allocated.
//
// A run's memory is measured at host calls and every few thousand steps
// (scriptguard.PaceSteps), which bounds what a run builds a little at a time.
// It does not bound one operation that allocates a great deal at once: a
// repeat ([0] * n), a concatenation repeated (s = s + s), a list extended by
// itself, a replacement or a join that multiplies its input, or a range turned
// into a list. Each of those is routed here, sizes the value it is about to
// build, and asks scriptguard.Reserve first.
//
// It has to be a rewrite, for the reason scriptprintf gives: Starlark
// evaluates operators and a value's methods inside the interpreter, with no
// hook a predeclared value can take. The names the rewrite calls are not
// identifiers, so a script can neither call nor shadow them.
package scriptalloc

import (
	"fmt"
	"maps"
	"math"
	"strings"
	"unicode/utf8"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptguard"
)

// The globals the rewrite routes through.
const (
	mulName        = "*"
	addName        = "+"
	addAssignName  = "+="
	indexAddName   = "[]+="
	indexMulName   = "[]*="
	replaceName    = ".replace"
	joinName       = ".join"
	extendName     = ".extend"
	splitName      = ".split"
	rsplitName     = ".rsplit"
	splitlinesName = ".splitlines"
)

// partBytes is what one piece of a split costs beyond its text: the list slot
// and the string header boxed in it.
const partBytes = 32

// slotBytes is what one element of a list or tuple costs: an interface value.
// The elements themselves are shared with the operands, so a repeat or a
// concatenation allocates the slots and nothing else.
const slotBytes = 16

// materializers are the builtins that turn one iterable into a value holding
// every element, with what each element costs in it: a range is lazy, and
// list(range(n)) allocates all n in one step.
//
//nolint:gochecknoglobals // a read-only lookup table.
var materializers = map[string]int64{
	"list": slotBytes, "tuple": slotBytes, "sorted": slotBytes, "reversed": slotBytes,
	// A set entry is its hash-table share; an enumerate or zip element is a
	// slot and a tuple of two.
	"set": 128, "enumerate": 72, "zip": 72,
}

// Bind returns env with the rewrite's globals bound, and the builtins that
// materialize an iterable replaced by ones that size it first. A name env
// already binds is left as env binds it.
func Bind(env starlark.StringDict) starlark.StringDict {
	out := make(starlark.StringDict, len(env))
	maps.Copy(out, env)
	out[mulName] = starlark.NewBuiltin(mulName, mul)
	out[addName] = starlark.NewBuiltin(addName, add)
	out[addAssignName] = starlark.NewBuiltin(addName, addAssign)
	out[indexAddName] = starlark.NewBuiltin(addName, indexAdd)
	out[indexMulName] = starlark.NewBuiltin(mulName, indexMul)
	out[replaceName] = starlark.NewBuiltin("replace", method("replace", replaceSize))
	out[joinName] = starlark.NewBuiltin("join", method("join", joinSize))
	out[extendName] = starlark.NewBuiltin("extend", method("extend", extendSize))
	out[splitName] = starlark.NewBuiltin("split", method("split", splitSize))
	out[rsplitName] = starlark.NewBuiltin("rsplit", method("rsplit", splitSize))
	out[splitlinesName] = starlark.NewBuiltin("splitlines", method("splitlines", splitlinesSize))
	for name, per := range materializers {
		if _, shadowed := env[name]; !shadowed {
			out[name] = starlark.NewBuiltin(name, materializing(name, per))
		}
	}
	return out
}

// refund gives back the step the rewrite added. `x + y` is three
// instructions; the call it becomes is four (the global is loaded first), and
// a run's step budget is the author's, measured as they wrote the code.
func refund(thread *starlark.Thread) {
	if thread.Steps > 0 {
		thread.Steps--
	}
}

// mul is `x * y`.
func mul(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	refund(thread)
	return multiply(thread, args[0], args[1])
}

// multiply is x * y, a repeat sized and reserved first.
func multiply(thread *starlark.Thread, x, y starlark.Value) (starlark.Value, error) {
	if size, ok := repeatSize(x, y); ok {
		if err := scriptguard.Reserve(thread, size, "a repeat (*)"); err != nil {
			return nil, err //nolint:wrapcheck // the refusal names the operation
		}
	}
	return starlark.Binary(syntax.STAR, x, y) //nolint:wrapcheck // the interpreter's own message
}

// add is `x + y`.
func add(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	refund(thread)
	return plus(thread, args[0], args[1])
}

// plus is x + y, a concatenation sized and reserved first.
func plus(thread *starlark.Thread, x, y starlark.Value) (starlark.Value, error) {
	if size, ok := concatSize(x, y); ok {
		if err := scriptguard.Reserve(thread, size, "a concatenation (+)"); err != nil {
			return nil, err //nolint:wrapcheck // the refusal names the operation
		}
	}
	return starlark.Binary(syntax.PLUS, x, y) //nolint:wrapcheck // the interpreter's own message
}

// addAssign is `x += y` for a name: the interpreter's in-place add, which
// extends a list in place (every name holding it sees the change) and is x + y
// for anything else.
func addAssign(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	refund(thread)
	return inplaceAdd(thread, args[0], args[1])
}

// inplaceAdd is the interpreter's INPLACE_ADD, sized and reserved first.
func inplaceAdd(thread *starlark.Thread, x, y starlark.Value) (starlark.Value, error) {
	list, ok := x.(*starlark.List)
	if !ok {
		return plus(thread, x, y)
	}
	iter, ok := y.(starlark.Iterable)
	if !ok {
		return plus(thread, x, y)
	}
	if n, known := length(y); known {
		if err := scriptguard.Reserve(thread, int64(list.Len()+n)*slotBytes, "a list extended (+=)"); err != nil {
			return nil, err //nolint:wrapcheck // the refusal names the operation
		}
	}
	// The elements are read before any is appended, so `x += x` doubles x
	// once, as the interpreter's in-place add does.
	var elems []starlark.Value
	it := iter.Iterate()
	var v starlark.Value
	for it.Next(&v) {
		elems = append(elems, v)
	}
	it.Done()
	for _, e := range elems {
		if err := list.Append(e); err != nil {
			return nil, fmt.Errorf("cannot apply += to %s: %w", list.Type(), err)
		}
	}
	return list, nil
}

// indexAdd is `x[k] += y`: x and k are evaluated once, as the interpreter
// does, then x[k] is set to x[k] += y.
func indexAdd(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	return indexOp(thread, args, inplaceAdd)
}

// indexMul is `x[k] *= y`.
func indexMul(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	return indexOp(thread, args, multiply)
}

// indexOp reads x[k], applies op with y, and stores the result at x[k].
func indexOp(thread *starlark.Thread, args starlark.Tuple, op func(*starlark.Thread, starlark.Value, starlark.Value) (starlark.Value, error)) (starlark.Value, error) {
	x, k, y := args[0], args[1], args[2]
	old, err := getIndex(x, k)
	if err != nil {
		return nil, err
	}
	z, err := op(thread, old, y)
	if err != nil {
		return nil, err
	}
	if err := setIndex(x, k, z); err != nil {
		return nil, err
	}
	return starlark.None, nil
}

// getIndex is x[k] for a mapping or an indexable sequence.
func getIndex(x, k starlark.Value) (starlark.Value, error) {
	switch x := x.(type) {
	case starlark.Mapping:
		v, found, err := x.Get(k)
		if err != nil {
			return nil, err //nolint:wrapcheck // the mapping's own message
		}
		if !found {
			return nil, fmt.Errorf("key %v not in %s", k, x.Type())
		}
		return v, nil
	case starlark.Indexable:
		i, err := position(k, x.Len(), x.Type())
		if err != nil {
			return nil, err
		}
		return x.Index(i), nil
	}
	return nil, fmt.Errorf("unhandled index operation %s[%s]", x.Type(), k.Type())
}

// setIndex stores v at x[k].
func setIndex(x, k, v starlark.Value) error {
	switch x := x.(type) {
	case starlark.HasSetKey:
		return x.SetKey(k, v) //nolint:wrapcheck // the mapping's own message
	case starlark.HasSetIndex:
		i, err := position(k, x.Len(), x.Type())
		if err != nil {
			return err
		}
		return x.SetIndex(i, v) //nolint:wrapcheck // the sequence's own message
	}
	return fmt.Errorf("a value of type %s does not support item assignment", x.Type())
}

// position is the element k names in a sequence of n, counting a negative
// index from the end.
func position(k starlark.Value, n int, typ string) (int, error) {
	i, err := starlark.AsInt32(k)
	if err != nil {
		return 0, fmt.Errorf("index into a %s: %w", typ, err)
	}
	if i < 0 {
		i += n
	}
	if i < 0 || i >= n {
		return 0, fmt.Errorf("index %d out of range [0:%d] for a %s", i, n, typ)
	}
	return i, nil
}

// method is a builtin routing recv.name(args) through size: a receiver size
// knows is sized and reserved first; every receiver then has its own method
// called, so the result and any error are the method's.
func method(name string, size func(recv starlark.Value, args starlark.Tuple, kwargs []starlark.Tuple) (int64, bool)) func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		recv, rest := args[0], args[1:]
		if n, ok := size(recv, rest, kwargs); ok {
			if err := scriptguard.Reserve(thread, n, "."+name+"()"); err != nil {
				return nil, err //nolint:wrapcheck // the refusal names the operation
			}
		}
		attrs, ok := recv.(starlark.HasAttrs)
		if !ok {
			return nil, fmt.Errorf("a value of type %s has no .%s field or method", recv.Type(), name)
		}
		m, err := attrs.Attr(name)
		if err != nil {
			return nil, err //nolint:wrapcheck // the receiver's own attribute error
		}
		if m == nil {
			return nil, fmt.Errorf("a value of type %s has no .%s field or method", recv.Type(), name)
		}
		return starlark.Call(thread, m, rest, kwargs) //nolint:wrapcheck // the method's own failure
	}
}

// materializing is the universe builtin name, its sized arguments reserved
// first at per bytes an element. Only arguments with a known length are
// sized; the builtin itself reports any argument error.
func materializing(name string, per int64) func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	builtin := starlark.Universe[name]
	return func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := scriptguard.Reserve(thread, product(materialized(name, args), per), name+"()"); err != nil {
			return nil, err //nolint:wrapcheck // the refusal names the operation
		}
		return starlark.Call(thread, builtin, args, kwargs) //nolint:wrapcheck // the builtin's own result and message
	}
}

// materialized is how many elements the builtin name builds from args: the
// sum of the sized arguments' lengths, or for zip, which stops at its
// shortest argument, the least of them.
func materialized(name string, args starlark.Tuple) int64 {
	var elems int64
	for _, a := range args {
		n, known := length(a)
		switch {
		case !known:
		case name != "zip":
			elems += int64(n)
		case elems == 0 || int64(n) < elems:
			elems = int64(n)
		}
	}
	return elems
}

// length is the element count of v when it is a sized value.
func length(v starlark.Value) (int, bool) {
	if s, ok := v.(starlark.Sequence); ok {
		return s.Len(), true
	}
	return 0, false
}

// elemBytes is what one element of a sequence costs in a new value of its
// type, its length, and whether the type is one a repeat or a concatenation
// builds.
func elemBytes(v starlark.Value) (per int64, n int, ok bool) {
	switch v := v.(type) {
	case starlark.String:
		return 1, len(v), true
	case starlark.Bytes:
		return 1, len(v), true
	case *starlark.List:
		return slotBytes, v.Len(), true
	case starlark.Tuple:
		return slotBytes, len(v), true
	}
	return 0, 0, false
}

// repeatSize is the size of x * y when one is a sequence and the other an int.
func repeatSize(x, y starlark.Value) (int64, bool) {
	seq, count := x, y
	if _, isInt := x.(starlark.Int); isInt {
		seq, count = y, x
	}
	per, n, ok := elemBytes(seq)
	if !ok {
		return 0, false
	}
	c, isInt := count.(starlark.Int)
	if !isInt {
		return 0, false
	}
	times, exact := c.Int64()
	if !exact {
		return math.MaxInt64, true
	}
	return product(per*int64(n), times), true
}

// concatSize is the size of x + y when both are sequences of one type.
func concatSize(x, y starlark.Value) (int64, bool) {
	px, nx, ok := elemBytes(x)
	if !ok {
		return 0, false
	}
	py, ny, ok := elemBytes(y)
	if !ok || px != py || x.Type() != y.Type() {
		return 0, false
	}
	return px * int64(nx+ny), true
}

// replaceSize is the size of s.replace(old, new[, count]).
func replaceSize(recv starlark.Value, args starlark.Tuple, _ []starlark.Tuple) (int64, bool) {
	s, ok := recv.(starlark.String)
	if !ok || len(args) < 2 {
		return 0, false
	}
	oldS, ok1 := args[0].(starlark.String)
	newS, ok2 := args[1].(starlark.String)
	if !ok1 || !ok2 || len(newS) <= len(oldS) {
		return 0, false
	}
	return int64(len(s)) + product(replacements(string(s), string(oldS), args[2:]), int64(len(newS)-len(oldS))), true
}

// replacements is how many times s.replace(old, new[, count]) replaces old:
// every occurrence (every rune boundary for an empty old), at most count.
func replacements(s, old string, rest starlark.Tuple) int64 {
	matches := int64(strings.Count(s, old))
	if old == "" {
		matches = int64(utf8.RuneCountInString(s) + 1)
	}
	if len(rest) == 0 {
		return matches
	}
	c, isInt := rest[0].(starlark.Int)
	if !isInt {
		return matches
	}
	if limit, exact := c.Int64(); exact && limit >= 0 && limit < matches {
		return limit
	}
	return matches
}

// joinSize is the size of sep.join(iterable) of strings.
func joinSize(recv starlark.Value, args starlark.Tuple, _ []starlark.Tuple) (int64, bool) {
	sep, ok := recv.(starlark.String)
	if !ok || len(args) != 1 {
		return 0, false
	}
	iter, ok := args[0].(starlark.Iterable)
	if !ok {
		return 0, false
	}
	var total, n int64
	it := iter.Iterate()
	defer it.Done()
	var v starlark.Value
	for it.Next(&v) {
		if s, isStr := v.(starlark.String); isStr {
			total += int64(len(s))
		}
		n++
	}
	if n > 1 {
		total += product(int64(len(sep)), n-1)
	}
	return total, true
}

// extendSize is the size of list.extend(iterable) when the iterable is sized.
func extendSize(recv starlark.Value, args starlark.Tuple, _ []starlark.Tuple) (int64, bool) {
	list, ok := recv.(*starlark.List)
	if !ok || len(args) != 1 {
		return 0, false
	}
	n, known := length(args[0])
	if !known {
		return 0, false
	}
	return int64(list.Len()+n) * slotBytes, true
}

// splitSize is the size of s.split(sep, maxsplit) or s.rsplit: one piece per
// separator plus one (per run of whitespace with no sep), at most maxsplit+1,
// each costing partBytes beside the text. Starlark takes both positionally;
// an argument of the wrong type is the method's to refuse.
func splitSize(recv starlark.Value, args starlark.Tuple, _ []starlark.Tuple) (int64, bool) {
	s, ok := recv.(starlark.String)
	if !ok {
		return 0, false
	}
	sep, limit := splitArgs(args)
	var parts int64
	if sep == "" {
		parts = int64(len(strings.Fields(string(s))))
	} else {
		parts = int64(strings.Count(string(s), sep)) + 1
	}
	if limit >= 0 && limit+1 < parts {
		parts = limit + 1
	}
	return int64(len(s)) + product(parts, partBytes), true
}

// splitArgs reads split's sep ("" for whitespace or None) and maxsplit (-1
// for no limit) from its positional arguments.
func splitArgs(args starlark.Tuple) (sep string, maxsplit int64) {
	maxsplit = -1
	if len(args) > 0 {
		if v, ok := args[0].(starlark.String); ok {
			sep = string(v)
		}
	}
	if len(args) > 1 {
		if v, ok := args[1].(starlark.Int); ok {
			if n, exact := v.Int64(); exact {
				maxsplit = n
			}
		}
	}
	return sep, maxsplit
}

// splitlinesSize is the size of s.splitlines(): one piece per line.
func splitlinesSize(recv starlark.Value, _ starlark.Tuple, _ []starlark.Tuple) (int64, bool) {
	s, ok := recv.(starlark.String)
	if !ok {
		return 0, false
	}
	return int64(len(s)) + product(int64(strings.Count(string(s), "\n"))+1, partBytes), true
}

// product is a * b, saturating at MaxInt64 rather than overflowing; a
// negative factor is a product of zero.
func product(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}
