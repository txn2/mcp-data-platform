package scriptdialect

import (
	"fmt"

	"go.starlark.net/starlark"
)

// TestOnly is what a run binds a test module's name to: any use of it fails,
// saying where it belongs.
type TestOnly string

// String, Type, Freeze, Truth and AttrNames make the stub a Starlark value
// that reads as the module it stands in for.
func (t TestOnly) String() string { return string(t) }

// Type is the module's.
func (TestOnly) Type() string { return "module" }

// Freeze has nothing to freeze.
func (TestOnly) Freeze() {}

// Truth is a module's.
func (TestOnly) Truth() starlark.Bool { return starlark.True }

// AttrNames lists nothing: every attribute is refused.
func (TestOnly) AttrNames() []string { return nil }

// Hash is the module name's, so the stub can key a dict as a module can.
func (t TestOnly) Hash() (uint32, error) { return starlark.String(t).Hash() } //nolint:wrapcheck // a string's hash cannot fail

// Attr refuses every attribute, naming where the module belongs.
func (t TestOnly) Attr(name string) (starlark.Value, error) {
	return nil, fmt.Errorf("module %s.%s is available only inside a %s function; a run never calls one", t, name, TestPrefix+"*")
}
