package httpauth

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/selfcall"
	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// AdminSelfCalls wraps the admin API's authenticator so a request served
// inside this process for a caller the tool-call middleware authenticated (a
// managed script's call on the platform-admin connection, #2086) is admitted
// as that caller when its roles resolve to the admin persona, as a token's
// would be, and refused with a *selfcall.PersonaError, which the admin API
// answers 403, when they do not. Every other request is next's.
func AdminSelfCalls(next admin.Authenticator, registry *persona.Registry, adminPersona string) admin.Authenticator {
	return adminSelfCalls{next: next, registry: registry, persona: adminPersona}
}

type adminSelfCalls struct {
	next     admin.Authenticator
	registry *persona.Registry
	persona  string
}

// Authenticate implements admin.Authenticator.
func (a adminSelfCalls) Authenticate(r *http.Request) (*admin.User, error) {
	c, ok := selfcall.From(r.Context())
	if !ok {
		return a.next.Authenticate(r) //nolint:wrapcheck // the wrapped authenticator's answer, as it gave it
	}
	if resolved, found := a.registry.GetForRoles(c.Roles); found && resolved.Name == a.persona {
		return &admin.User{UserID: c.UserID, Email: c.Email, Roles: c.Roles}, nil
	}
	refusal := &selfcall.PersonaError{Surface: "admin API", Persona: a.persona, Caller: c.UserID, Has: c.Roles}
	if p, found := a.registry.Get(a.persona); found {
		refusal.Need = p.Roles
	}
	return nil, refusal
}
