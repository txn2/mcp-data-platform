// Package scriptfmt is the one format every managed script is stored in
// (#1937): the Starlark printer from github.com/bazelbuild/buildtools, without
// its rewrites, applied on every save so no agent has to think about layout
// and no diff between two versions is noise.
//
// It lays the source out and changes nothing else: keyword arguments are
// written key = value, the items of a multi-line list or dict go one per line,
// strings take double quotes, and every comment is kept. The rewrites the
// printer can also apply (sorting some lists and load statements) are for Bazel
// BUILD files and are not used, since reordering a script's lists could change
// what it does.
package scriptfmt

import (
	"fmt"
	"slices"
	"strings"

	"github.com/bazelbuild/buildtools/build"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
)

// Format returns source in the canonical format. The printed source is used
// only when the dialect parses it into the same program -- the same syntax
// tree, literal values and comments -- as the source sent; otherwise the
// source is returned as it was. The dialect is the authority on what a script
// is, so a save is never refused, and never changed in meaning, because of how
// the printer laid it out.
func Format(source string) string {
	file, err := build.ParseDefault("script", []byte(source))
	if err != nil {
		return source
	}
	out := string(build.FormatWithoutRewriting(file))
	if !sameProgram(source, out) {
		return source
	}
	return out
}

// sameProgram reports whether two sources parse under the dialect into the
// same program.
func sameProgram(a, b string) bool {
	fa, err := scriptdialect.Options.Parse("script", a, syntax.RetainComments)
	if err != nil {
		return false
	}
	fb, err := scriptdialect.Options.Parse("script", b, syntax.RetainComments)
	if err != nil {
		return false
	}
	return shape(fa) == shape(fb) && slices.Equal(comments(fa), comments(fb))
}

// shape renders a syntax tree without positions, comments or quote style: the
// node kinds, names, operators and literal values in walk order. Parentheses
// the printer adds or drops do not change the program and are left out.
func shape(f *syntax.File) string {
	var b strings.Builder
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case nil, *syntax.ParenExpr:
		case *syntax.Ident:
			b.WriteString("id:" + n.Name + " ")
		case *syntax.Literal:
			fmt.Fprintf(&b, "lit:%#v ", n.Value)
		case *syntax.BinaryExpr:
			b.WriteString("bin:" + n.Op.String() + " ")
		case *syntax.UnaryExpr:
			b.WriteString("un:" + n.Op.String() + " ")
		case *syntax.AssignStmt:
			b.WriteString("assign:" + n.Op.String() + " ")
		default:
			fmt.Fprintf(&b, "%T ", n)
		}
		return true
	})
	return b.String()
}

// comments is the text of every comment in a file, in order.
func comments(f *syntax.File) []string {
	var out []string
	syntax.Walk(f, func(n syntax.Node) bool {
		if n == nil {
			return false
		}
		if cm := n.Comments(); cm != nil {
			for _, group := range [][]syntax.Comment{cm.Before, cm.Suffix, cm.After} {
				for _, c := range group {
					out = append(out, strings.TrimSpace(c.Text))
				}
			}
		}
		return true
	})
	return out
}
