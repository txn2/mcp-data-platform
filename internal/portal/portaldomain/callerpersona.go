package portaldomain

import "context"

type callerPersonaKey struct{}

// WithCallerPersona records on ctx the persona the portal caller resolved to
// (#2089). The share store matches a share addressed to a persona against it,
// so a person's access follows their persona's membership at the moment they
// open an item. The portal's persona gate sets it on every request it admits.
func WithCallerPersona(ctx context.Context, persona string) context.Context {
	return context.WithValue(ctx, callerPersonaKey{}, persona)
}

// CallerPersona is the persona WithCallerPersona recorded, "" when none was:
// a caller with no persona matches no persona share.
func CallerPersona(ctx context.Context) string {
	p, _ := ctx.Value(callerPersonaKey{}).(string)
	return p
}
