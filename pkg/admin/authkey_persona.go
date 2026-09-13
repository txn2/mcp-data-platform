package admin

import (
	"fmt"
	"strings"
)

// keyPersona reports the persona a key's roles reach, and whether they reach
// none (#1705). With no resolver wired it reports neither, rather than flagging
// every key as unmapped.
func (h *Handler) keyPersona(roles []string) (name string, noPersona bool) {
	if h.deps.PersonaResolver == nil {
		return "", false
	}
	p, ok := h.deps.PersonaResolver.Resolve(roles)
	if !ok {
		return "", true
	}
	return p.Name, false
}

// noPersonaWarning is the sentence a key whose roles reach no persona is
// created with. It says what the key will do, which roles would have worked,
// and, for the mistake that prompted it, that a role naming a persona is not
// one of that persona's roles.
func (h *Handler) noPersonaWarning(roles []string) string {
	sentences := []string{fmt.Sprintf("No persona carries any of the roles %s, so this key authenticates and lists no tools.", quoteList(roles))}
	if granting := h.deps.PersonaResolver.GrantingRoles(); len(granting) == 0 {
		sentences = append(sentences, "This deployment defines no persona that carries a role.")
	} else {
		sentences = append(sentences, fmt.Sprintf("Roles the personas carry: %s.", quoteList(granting)))
	}
	if h.deps.PersonaRegistry == nil {
		return strings.Join(sentences, " ")
	}
	for _, role := range roles {
		p, ok := h.deps.PersonaRegistry.Get(role)
		if !ok {
			continue
		}
		carries := "that persona carries no roles, so no key reaches it."
		if len(p.Roles) > 0 {
			carries = fmt.Sprintf("that persona carries %s.", quoteList(p.Roles))
		}
		sentences = append(sentences, fmt.Sprintf("%q is the name of a persona, not one of its roles; %s", role, carries))
	}
	return strings.Join(sentences, " ")
}

// quoteList renders names as a comma-separated list of quoted strings.
func quoteList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(quoted, ", ")
}
