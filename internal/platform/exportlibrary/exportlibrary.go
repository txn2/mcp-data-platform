// Package exportlibrary is the library a managed script's "resources" export
// files its output in (#2080): platform.export's scope= and scope_id=, read the
// way manage_resource reads the same pair. Without scope the output lands in
// the library of the person the run acts for; with one it lands in the library
// scope names, and whether the run may write there is the resource rule
// (resource.CanWriteScope) against the identity the run presents, checked when
// the script is saved and again when the file is written.
//
// It is kept out of internal/platform/scriptrun for that package's size
// budget, as exporttable and exportrefs are: the argument's rules and its
// static read live here, and the engine and the validator call them.
package exportlibrary

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Library is one library a script's "resources" exports write to, as the
// source names it: Scope empty is the library of the person the run acts for.
type Library struct {
	Scope   string `json:"scope"`
	ScopeID string `json:"scope_id,omitempty"`
}

// Label names the library the way a refusal and a reader of the reach do.
func (l Library) Label() string {
	switch {
	case l.Own():
		return "the library of the person the run acts for"
	case l.Scope == string(resource.ScopeGlobal):
		return "the global library"
	case l.Scope == string(resource.ScopePersona):
		return fmt.Sprintf("the %s persona library", l.ScopeID)
	default:
		return fmt.Sprintf("the library of %s", l.ScopeID)
	}
}

// Own reports whether the library is the one the run acts for, which a run
// can always write to.
func (l Library) Own() bool {
	return l.Scope == "" || (l.Scope == string(resource.ScopeUser) && l.ScopeID == "")
}

// Normalized states the run's own library one way: scope="user" with no
// scope_id is the same library as no scope at all.
func (l Library) Normalized() Library {
	if l.Own() {
		return Library{}
	}
	return l
}

// RecordScope is the library as an export record states it: the scope, and
// "user" with no id for the run's own.
func (l Library) RecordScope() (scope, scopeID string) {
	if l.Scope == "" {
		return string(resource.ScopeUser), ""
	}
	return l.Scope, l.ScopeID
}

// ErrNotLibrary is a scope given to a destination that files nothing in a
// library.
var ErrNotLibrary = errors.New(`scope and scope_id name a library, and only the "resources" destination files an output in one`)

// Check checks the scope and scope_id an export names, the same pair
// manage_resource takes: both empty is the run's own library.
func Check(scope, scopeID string) error {
	switch resource.Scope(scope) {
	case "":
		if scopeID != "" {
			return errors.New(`scope_id names a library only together with scope="persona" or scope="user"`)
		}
	case resource.ScopeUser:
		// scope_id is optional: empty is the run's own library, an address
		// is that person's, which only a platform administrator may write.
	case resource.ScopePersona:
		if scopeID == "" {
			return errors.New(`scope="persona" needs scope_id, the persona whose library the file goes in`)
		}
	case resource.ScopeGlobal:
		if scopeID != "" {
			return errors.New(`scope="global" takes no scope_id: there is one global library`)
		}
	default:
		return fmt.Errorf(`scope %q is not one of "user", "persona" or "global"`, scope)
	}
	return nil
}

// CheckFor checks the scope and scope_id an export names against whether its
// destination files in a library: none named is always fine.
func CheckFor(library bool, scope, scopeID string) error {
	if scope == "" && scopeID == "" {
		return nil
	}
	if !library {
		return ErrNotLibrary
	}
	return Check(scope, scopeID)
}

// Reader reads an argument as the string it is written as, directly or
// through a module constant, reporting whether it is one.
type Reader func(syntax.Expr) (string, bool)

// Finding is what a source's export says that names no library a reader can
// state: the message, and the hint that says what to write.
type Finding struct {
	Message string
	Hint    string
}

// computedHint is what a computed scope is told to write instead.
const computedHint = `Name the library in the source, scope="global" or scope="persona", scope_id="finance": ` +
	"which library a script writes to has to be readable from its source, and checked when it is saved."

// FromCall reads the library one platform.export call files in. It returns
// the library for a "resources" export whose scope arguments are written out,
// nil for one that files in no library or whose destination is computed, and
// a Finding for scope arguments a reader cannot state: computed, naming no
// library, or given to a destination that files in none. resources is the
// built-in destination's name.
func FromCall(call *syntax.CallExpr, read Reader, resources string) (*Library, *Finding) {
	scope, scopeOK, scopeGiven := keyword(call, "scope", read)
	scopeID, idOK, idGiven := keyword(call, "scope_id", read)
	if (scopeGiven && !scopeOK) || (idGiven && !idOK) {
		return nil, &Finding{Message: "platform.export's scope and scope_id must be written out as strings", Hint: computedHint}
	}
	if files, finding := filesInLibrary(call, read, resources, scopeGiven || idGiven); !files {
		return nil, finding
	}
	if err := Check(scope, scopeID); err != nil {
		return nil, &Finding{Message: "platform.export: " + err.Error()}
	}
	lib := Library{Scope: scope, ScopeID: scopeID}.Normalized()
	return &lib, nil
}

// filesInLibrary reports whether the call's destination is read as the
// library destination, and the finding when it names a library anyway.
func filesInLibrary(call *syntax.CallExpr, read Reader, resources string, named bool) (bool, *Finding) {
	destination, destOK, destGiven := keyword(call, "destination", read)
	switch {
	case destGiven && !destOK:
		return false, nil // a computed destination is reported as one; the library cannot be read either
	case destination != resources && named:
		return false, &Finding{Message: ErrNotLibrary.Error()}
	default:
		return destination == resources, nil
	}
}

// keyword reads one keyword argument: its value, whether it is written out,
// and whether the call passes it at all.
func keyword(call *syntax.CallExpr, name string, read Reader) (value string, literal, given bool) {
	for _, arg := range call.Args {
		bin, ok := arg.(*syntax.BinaryExpr)
		if !ok || bin.Op != syntax.EQ {
			continue
		}
		if key, ok := bin.X.(*syntax.Ident); !ok || key.Name != name {
			continue
		}
		s, ok := read(bin.Y)
		return strings.TrimSpace(s), ok, true
	}
	return "", false, false
}

// Sorted returns the libraries in a stable order, never nil.
func Sorted(set map[Library]bool) []Library {
	out := make([]Library, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	slices.SortFunc(out, func(a, b Library) int {
		return cmp.Or(cmp.Compare(a.Scope, b.Scope), cmp.Compare(a.ScopeID, b.ScopeID))
	})
	return out
}
