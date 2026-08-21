package scriptrun

import (
	"fmt"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// sumBuiltinName is the global `sum` is bound to. It is spelled once so the
// binding, the resolver's view of the environment, and the error messages
// cannot disagree.
const sumBuiltinName = "sum"

// sumBuiltin adds an iterable of numbers.
//
// Starlark's universe has no sum: min, max, any and all are there, and the one
// reduction a reporting script performs on every run is not. Totalling a
// numeric column is the operation these scripts exist to do, and without it
// every author writes the same four-line accumulator — or, more often, writes
// sum() from Python instinct and gets `undefined: sum` back.
//
// It follows Python's signature rather than inventing one: sum(iterable,
// start=0), left to right, an int result when every addend is an int. The
// values it is pointed at are usually float(r["total"]) over a DECIMAL column,
// which arrives from SQL as a string.
var sumBuiltin = starlark.NewBuiltin(sumBuiltinName, sumFn)

// sumFn implements sum(iterable, start=0).
func sumFn(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var iterable starlark.Iterable
	var start starlark.Value = starlark.MakeInt(0)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "iterable", &iterable, "start?", &start); err != nil {
		return nil, argErr(b, err)
	}
	if err := requireNumber(b, start); err != nil {
		return nil, err
	}
	return sumOver(b, iterable, start)
}

// sumOver folds the iterable onto the running total, refusing the first
// non-number it meets by position. The position is the part an author needs:
// the offending element is almost always a DECIMAL column that arrived as a
// string, and the row index is what points at it.
func sumOver(b *starlark.Builtin, iterable starlark.Iterable, total starlark.Value) (starlark.Value, error) {
	iter := iterable.Iterate()
	defer iter.Done()

	var elem starlark.Value
	for i := 0; iter.Next(&elem); i++ {
		if err := requireNumberAt(b, elem, i); err != nil {
			return nil, err
		}
		sum, err := starlark.Binary(syntax.PLUS, total, elem)
		if err != nil {
			return nil, argErr(b, err)
		}
		total = sum
	}
	return total, nil
}

// requireNumber refuses a start value that is not a number.
func requireNumber(b *starlark.Builtin, v starlark.Value) error {
	if isNumber(v) {
		return nil
	}
	return fmt.Errorf("in %s: start must be a number, got %s", b.Name(), v.Type())
}

// requireNumberAt refuses a non-number element and says which one it was.
func requireNumberAt(b *starlark.Builtin, v starlark.Value, index int) error {
	if isNumber(v) {
		return nil
	}
	return fmt.Errorf("in %s: element %d is %s, not a number; a SQL DECIMAL column arrives as a string, so pass it through float() first",
		b.Name(), index, v.Type())
}

// isNumber reports whether a value is one sum may add. bool is excluded
// deliberately: Starlark will add it, and a total that silently counted True as
// 1 is a wrong number reported as a right one.
func isNumber(v starlark.Value) bool {
	switch v.(type) {
	case starlark.Int, starlark.Float:
		return true
	default:
		return false
	}
}
