package scriptprintf

import (
	"fmt"
	"maps"
	"reflect"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// PercentName and FormatName are the globals Rewrite routes `%` and .format
// through. Neither is an identifier a script can write, so a script can
// neither call nor shadow them: the resolver looks a name up by its text and
// does not check that it could have been typed.
const (
	PercentName = "%"
	FormatName  = ".format"
)

// percentBuiltin is the binding of PercentName.
var percentBuiltin = starlark.NewBuiltin("%", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	return Percent(args[0], args[1])
})

// formatBuiltin is the binding of FormatName: the receiver first, then the
// call's own arguments.
var formatBuiltin = starlark.NewBuiltin("format", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if s, ok := args[0].(starlark.String); ok {
		return Format(string(s), args[1:], kwargs)
	}
	var method starlark.Value
	if recv, ok := args[0].(starlark.HasAttrs); ok {
		m, err := recv.Attr("format")
		if err != nil {
			return nil, err //nolint:wrapcheck // the receiver's own attribute error
		}
		method = m
	}
	if method == nil {
		return nil, fmt.Errorf("a value of type %s has no .format field or method", args[0].Type())
	}
	return starlark.Call(thread, method, args[1:], kwargs) //nolint:wrapcheck // the method's own failure
})

// Bind returns env with PercentName and FormatName bound, for the module a
// rewritten file is compiled against.
func Bind(env starlark.StringDict) starlark.StringDict {
	out := make(starlark.StringDict, len(env))
	maps.Copy(out, env)
	out[PercentName] = percentBuiltin
	out[FormatName] = formatBuiltin
	return out
}

// Rewrite routes every `x % y`, `name %= y` and `x.format(...)` in file
// through PercentName and FormatName, in place. Positions are kept: the call
// is placed at the operator or the method name, so an error is reported
// where the script wrote it.
//
// It has to be a rewrite. Starlark evaluates `%` and a string's methods
// inside the interpreter, with no hook a predeclared value can take, and the
// alternative is a fork of the interpreter.
//
// A `%=` whose target is not a plain name (d["k"] %= v) is left to Starlark:
// rewriting it would evaluate the target twice.
func Rewrite(file *syntax.File) {
	syntax.Walk(file, func(n syntax.Node) bool {
		if a, ok := n.(*syntax.AssignStmt); ok && a.Op == syntax.PERCENT_EQ {
			if id, ok := a.LHS.(*syntax.Ident); ok {
				a.Op = syntax.EQ
				a.RHS = call(PercentName, a.OpPos, &syntax.Ident{NamePos: id.NamePos, Name: id.Name}, a.RHS)
			}
		}
		replaceChildren(n)
		return true
	})
}

// exprType is syntax.Expr, which a node's replaceable fields are typed as.
var exprType = reflect.TypeFor[syntax.Expr]()

// replaceChildren replaces each child expression of n that rewritten
// returns a call for. It reads the node's fields by reflection: every field
// typed syntax.Expr or []syntax.Expr is a child expression, whatever the
// node, and listing the twenty node types by hand would let a new one slip
// by.
func replaceChildren(n syntax.Node) {
	v := reflect.ValueOf(n)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return
	}
	for _, f := range v.Elem().Fields() {
		if !f.CanSet() {
			continue
		}
		switch {
		case f.Type() == exprType:
			replaceAt(f)
		case f.Kind() == reflect.Slice && f.Type().Elem() == exprType:
			for j := range f.Len() {
				replaceAt(f.Index(j))
			}
		}
	}
}

// replaceAt replaces the expression held in slot when it is one Rewrite
// routes.
func replaceAt(slot reflect.Value) {
	if slot.IsNil() {
		return
	}
	if e, ok := slot.Interface().(syntax.Expr); ok {
		if r := rewritten(e); r != nil {
			slot.Set(reflect.ValueOf(r))
		}
	}
}

// rewritten is the call e becomes, or nil when it stays.
func rewritten(e syntax.Expr) syntax.Expr {
	switch e := e.(type) {
	case *syntax.BinaryExpr:
		if e.Op == syntax.PERCENT {
			return call(PercentName, e.OpPos, e.X, e.Y)
		}
	case *syntax.CallExpr:
		if dot, ok := e.Fn.(*syntax.DotExpr); ok && dot.Name.Name == "format" {
			args := append([]syntax.Expr{dot.X}, e.Args...)
			return &syntax.CallExpr{Fn: &syntax.Ident{NamePos: dot.Name.NamePos, Name: FormatName}, Lparen: e.Lparen, Args: args, Rparen: e.Rparen}
		}
	}
	return nil
}

// call is a call of the global name at pos with args.
func call(name string, pos syntax.Position, args ...syntax.Expr) *syntax.CallExpr {
	return &syntax.CallExpr{Fn: &syntax.Ident{NamePos: pos, Name: name}, Lparen: pos, Args: args, Rparen: pos}
}
