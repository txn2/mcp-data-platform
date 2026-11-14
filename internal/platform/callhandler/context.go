package callhandler

import "context"

type ctxKey int

const withinKey ctxKey = 0

// WithinHandler marks ctx as a tool-call handler's own run: the calls its
// script makes are not evaluated by tool-call handlers, which is what keeps a
// policy's read from being decided by a policy, and a handler from deciding
// its own calls.
func WithinHandler(ctx context.Context) context.Context {
	return context.WithValue(ctx, withinKey, true)
}

// Within reports whether ctx is a tool-call handler's own run.
func Within(ctx context.Context) bool {
	v, _ := ctx.Value(withinKey).(bool)
	return v
}
