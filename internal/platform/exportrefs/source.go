package exportrefs

import (
	"fmt"

	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/portal/contentrefs"
)

// Literal is one reference string a script's source writes that no export
// declares, and the line it is written on.
type Literal struct {
	URI  string
	Line int
}

// Message and Hint are what an author reads about one undeclared literal.
func (l Literal) Message() string {
	return fmt.Sprintf("%q is not declared in the references= of any platform.export, "+
		"so a document that names it serves it exactly as written and it resolves to nothing", l.URI)
}

// Hint names the fix, on the export that writes the document.
func (l Literal) Hint() string {
	return fmt.Sprintf("List it on the export that writes the document: platform.export(name, html, format=\"html\", references=[%q]). "+
		"Only something the script's author can read may be declared, and declaring it lets everyone the asset is shared with load it.", l.URI)
}

// InSource returns each reference string the source writes as a literal that
// no platform.export declares in references=, once, at its first line.
// exportMember and callMember are the platform module's export and call
// members.
//
// The string is read wherever it is written, because the usual shape assigns
// it to a name first (LOGO = "mcp://...") and formats it into the markup
// later, where no static read can follow it. A literal is not proof the string
// reaches a document, so three readings return nothing rather than guess. A
// script with no export writes no document, so a URI in it is handed to a
// tool. An export whose references= is computed declares a list this read
// cannot see; a name bound once to a string literal is read as that string. And a literal inside a platform.call is an argument to that tool,
// not markup, and is not read.
func InSource(file *syntax.File, exportMember, callMember string) []Literal {
	declared, exports, dynamic := exportDeclarations(file, exportMember)
	if !exports || dynamic {
		return nil
	}
	var out []Literal
	seen := map[string]bool{}
	syntax.Walk(file, func(n syntax.Node) bool {
		if _, ok := platformCall(n, callMember); ok {
			return false
		}
		lit, ok := n.(*syntax.Literal)
		if !ok {
			return true
		}
		s, ok := lit.Value.(string)
		if !ok {
			return true
		}
		for _, uri := range contentrefs.Named(s) {
			if !declared[uri] && !seen[uri] {
				seen[uri] = true
				out = append(out, Literal{URI: uri, Line: int(lit.TokenPos.Line)})
			}
		}
		return true
	})
	return out
}

// Declares reports whether one platform.export call passes references=.
func Declares(call *syntax.CallExpr) bool {
	_, ok := keywordValue(call, Keyword)
	return ok
}

// exportDeclarations reads every export's references= argument: the strings
// declared literally across them, whether the source exports at all, and
// whether any declaration is computed, which makes the set incomplete.
func exportDeclarations(file *syntax.File, exportMember string) (declared map[string]bool, exports, dynamic bool) {
	declared = map[string]bool{}
	constants := stringConstants(file)
	syntax.Walk(file, func(n syntax.Node) bool {
		call, ok := platformCall(n, exportMember)
		if !ok {
			return true
		}
		exports = true
		if hasStarArg(call) {
			dynamic = true
			return true
		}
		if value, ok := keywordValue(call, Keyword); ok && !literalStrings(value, constants, declared) {
			dynamic = true
		}
		return true
	})
	return declared, exports, dynamic
}

// platformCall recognizes a call of one member of the platform module.
func platformCall(n syntax.Node, member string) (*syntax.CallExpr, bool) {
	call, ok := n.(*syntax.CallExpr)
	if !ok {
		return nil, false
	}
	dot, ok := call.Fn.(*syntax.DotExpr)
	if !ok || dot.Name.Name != member {
		return nil, false
	}
	ident, ok := dot.X.(*syntax.Ident)
	return call, ok && ident.Name == "platform"
}

// hasStarArg reports whether a call spreads a computed list or dict into its
// arguments, which hides every keyword it might carry.
func hasStarArg(call *syntax.CallExpr) bool {
	for _, arg := range call.Args {
		if un, ok := arg.(*syntax.UnaryExpr); ok && (un.Op == syntax.STAR || un.Op == syntax.STARSTAR) {
			return true
		}
	}
	return false
}

// keywordValue returns the expression a call passes for one keyword argument.
func keywordValue(call *syntax.CallExpr, keyword string) (syntax.Expr, bool) {
	for _, arg := range call.Args {
		bin, ok := arg.(*syntax.BinaryExpr)
		if !ok || bin.Op != syntax.EQ {
			continue
		}
		if key, ok := bin.X.(*syntax.Ident); ok && key.Name == keyword {
			return bin.Y, true
		}
	}
	return nil, false
}

// stringConstants maps each name the source assigns exactly once, to a string
// literal, to that string. It is what lets references=[LOGO] be read where
// LOGO = "mcp://..." is the one binding of LOGO: the usual way to name a file
// once and use it in both the markup and the declaration. A name assigned
// twice, or to anything else, is not a constant and is left out.
func stringConstants(file *syntax.File) map[string]string {
	values := map[string]string{}
	counts := map[string]int{}
	syntax.Walk(file, func(n syntax.Node) bool {
		assign, ok := n.(*syntax.AssignStmt)
		if !ok {
			return true
		}
		ident, ok := assign.LHS.(*syntax.Ident)
		if !ok {
			return true
		}
		counts[ident.Name]++
		if lit, ok := assign.RHS.(*syntax.Literal); ok && assign.Op == syntax.EQ {
			if s, ok := lit.Value.(string); ok {
				values[ident.Name] = s
			}
		}
		return true
	})
	for name := range values {
		if counts[name] != 1 {
			delete(values, name)
		}
	}
	return values
}

// literalStrings adds each element of a list or tuple of strings to into, an
// element being a string literal or a name constants holds, and reports
// whether the whole expression was one.
func literalStrings(expr syntax.Expr, constants map[string]string, into map[string]bool) bool {
	var elems []syntax.Expr
	switch e := expr.(type) {
	case *syntax.ListExpr:
		elems = e.List
	case *syntax.TupleExpr:
		elems = e.List
	case *syntax.ParenExpr:
		return literalStrings(e.X, constants, into)
	default:
		return false
	}
	for _, elem := range elems {
		s, ok := stringValue(elem, constants)
		if !ok {
			return false
		}
		into[s] = true
	}
	return true
}

// stringValue is the string an element names: a string literal, or a name
// bound once to one.
func stringValue(elem syntax.Expr, constants map[string]string) (string, bool) {
	if ident, ok := elem.(*syntax.Ident); ok {
		s, found := constants[ident.Name]
		return s, found
	}
	lit, ok := elem.(*syntax.Literal)
	if !ok {
		return "", false
	}
	s, ok := lit.Value.(string)
	return s, ok
}
