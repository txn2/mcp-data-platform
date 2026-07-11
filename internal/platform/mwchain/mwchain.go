// Package mwchain validates an ordered middleware chain against declared
// ordering dependencies.
//
// A chain is a slice of specs in execution order (outermost first); each spec
// may name other specs that MUST be outer to it — the canonical case being a
// middleware that reads a value another writes via context.WithValue, which is
// invisible unless the writer runs first. Validate rejects any chain in which a
// required dependency is positioned inner to (or at the same position as) the
// spec that needs it, so an accidental reorder fails fast instead of silently
// mis-wiring the chain (issue #758).
package mwchain

import "fmt"

// Name identifies a middleware within a chain.
type Name string

// Spec declares one middleware's identity, the middlewares that must be outer
// to it (run earlier in execution order), and how to register it.
type Spec struct {
	Name     Name
	Requires []Name
	Register func()
}

// Validate checks that the chain is internally consistent: names are unique,
// every Requires target exists, and every required middleware is outer to (has
// a lower index than) the middleware that depends on it. It returns a named
// error on the first violation so a reorder is caught deterministically.
func Validate(specs []Spec) error {
	index := make(map[Name]int, len(specs))
	for i, s := range specs {
		if _, dup := index[s.Name]; dup {
			return fmt.Errorf("middleware %q declared more than once", s.Name)
		}
		index[s.Name] = i
	}
	for i, s := range specs {
		for _, req := range s.Requires {
			reqIdx, ok := index[req]
			if !ok {
				return fmt.Errorf("middleware %q requires unknown middleware %q", s.Name, req)
			}
			if reqIdx >= i {
				return fmt.Errorf("middleware %q requires %q to be outer, but %q is inner (%q at position %d, %q at position %d)",
					s.Name, req, req, s.Name, i, req, reqIdx)
			}
		}
	}
	return nil
}
