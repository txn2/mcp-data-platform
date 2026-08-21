package scripthttp

import (
	"context"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The connections a parameter may name (#1361).
//
// A `connection` parameter's value comes from a set the platform holds, so the
// surface that asks for it offers the set rather than asking somebody to
// remember the spelling. This route is where that set comes from: the
// connections the caller's own persona reaches, narrowed to the kind a
// connection-typed parameter binds. A run is authorized at query time against
// the roles captured at the script's last save, so what the middleware finally
// admits is the same boundary this picker draws from.

// ConnectionChoice is one connection a parameter may name: the value bound into
// a run, and what a person needs to pick it by.
type ConnectionChoice struct {
	Name string `json:"name" example:"warehouse"`
	// Kind is the toolkit serving it.
	Kind        string `json:"kind,omitempty" example:"trino"`
	Description string `json:"description,omitempty" example:"Production Trino cluster"`
}

// ConnectionScope is the caller a connection enumeration is narrowed to.
type ConnectionScope struct {
	// Persona is the caller's resolved persona, whose connections rules decide
	// what they may reach. An unresolved persona reaches nothing, which is the
	// same fail-closed default the authorizer applies to a tool call.
	Persona string
	// Unrestricted lifts the persona boundary for an administrator, whose reach
	// over this surface is unrestricted by design.
	Unrestricted bool
}

// ConnectionEnumerator lists the connections one caller may reach, in the
// deployment's terms. It is the composition root's, because resolving it means
// walking the live toolkit registry through the persona boundary and this
// package holds neither.
//
// Nil leaves the choices route unmounted: a deployment that cannot enumerate
// its connections should serve no set at all rather than an empty one, which a
// form would render as "this script may reach nothing".
type ConnectionEnumerator func(ctx context.Context, caller ConnectionScope) []ConnectionChoice

// connectionChoicesResponse is the set a connection parameter chooses from,
// and where it came from.
type connectionChoicesResponse struct {
	Data []ConnectionChoice `json:"data"`
	// Source names the boundary the set was drawn from.
	Source string `json:"source" example:"persona"`
	// Note states the source in the reader's terms, so a form can put it under
	// the control without composing the sentence itself.
	Note string `json:"note"`
}

// portalScriptConnections returns the connections a connection-typed parameter
// of this script may be bound to.
//
// @Summary      List the connections a script's parameters may name
// @Description  Returns the set a `connection` parameter chooses from: the connections the caller's persona reaches, narrowed to the kind the parameter binds. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  connectionChoicesResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/connections [get]
func (h *Handler) portalScriptConnections(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	if _, ok := h.ownedScript(w, r, user); !ok {
		return
	}
	reachable := bindableChoices(h.deps.Connections(r.Context(), ConnectionScope{
		Persona: user.Persona, Unrestricted: user.IsAdmin,
	}))
	httpjson.WriteJSON(w, http.StatusOK, connectionChoicesResponse{
		Data:   orEmptyChoices(reachable),
		Source: "persona",
		Note: "These are the connections your persona reaches that a script may " +
			"query. A run is authorized against the roles captured at the " +
			"script's last save, so a connection outside them is refused at the " +
			"query.",
	})
}

// bindableChoices narrows an enumeration to the connections a connection-typed
// parameter can actually name.
//
// The caller reaches connections of every kind the deployment holds, but the
// value bound here is passed to platform.query, which reaches one kind
// (script.ConnectionParamKind). Offering the others offers values the run
// refuses (#1384); narrowing to the bindable kind resolves a name to the
// connection the run will actually use, and to that one only.
func bindableChoices(reachable []ConnectionChoice) []ConnectionChoice {
	out := make([]ConnectionChoice, 0, len(reachable))
	for _, c := range reachable {
		if c.Kind == script.ConnectionParamKind {
			out = append(out, c)
		}
	}
	return out
}

// orEmptyChoices normalizes a nil enumeration so the payload carries a list
// rather than null.
func orEmptyChoices(choices []ConnectionChoice) []ConnectionChoice {
	if choices == nil {
		return []ConnectionChoice{}
	}
	return choices
}
