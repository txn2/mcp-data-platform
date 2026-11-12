// Package auditredact carries the values one request must never leave in an
// audit row (#2555): an endpoint request's application cookies, which its
// handler may pass to the platform calls it makes. The request's context holds
// them, and the audit middleware replaces them wherever they appear in a
// call's parameters and error before anything reads the event.
package auditredact

import "context"

// Redactor replaces the values it holds in a string.
type Redactor interface {
	String(s string) string
	Empty() bool
}

type key struct{}

// With has every audit row written for a call made under ctx replace red's
// values.
func With(ctx context.Context, red Redactor) context.Context {
	return context.WithValue(ctx, key{}, red)
}

// From is the redactor ctx holds, nil for none or an empty one.
func From(ctx context.Context) Redactor {
	red, _ := ctx.Value(key{}).(Redactor)
	if red == nil || red.Empty() {
		return nil
	}
	return red
}
