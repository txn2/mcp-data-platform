package scripthttp

import (
	"context"
	"net/http"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The connections a parameter may name (#1361).
//
// A `connection` parameter's value comes from a set the platform holds, so the
// surface that asks for it offers the set rather than asking somebody to
// remember the spelling. This route is where that set comes from, and there are
// two of them because there are two ways a script executes:
//
//   - An approved run is confined by the grant its approval bound, so the set
//     is exactly Grants.Connections. Nothing else can succeed, whatever the
//     person asking for the run can reach themselves.
//   - A draft dry run executes as its author with no grant layer, so the set is
//     the connections that author's persona reaches.
//
// Serving one where the other applies would offer values the run then refuses,
// which is the failure this whole ticket exists to remove.

// audienceDraft asks for the set a dry run executing as the caller may reach,
// rather than the default: the set the approved version was granted.
const audienceDraft = "draft"

// ConnectionChoice is one connection a parameter may name: the value bound into
// a run, and what a person needs to pick it by.
type ConnectionChoice struct {
	Name string `json:"name" example:"warehouse"`
	// Kind is the toolkit serving it, empty for a granted connection the caller
	// cannot themselves enumerate.
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
// and where it came from. The source is part of the answer rather than a
// detail: the two sets are different, and a form that showed one while
// labeling it the other would be lying about what the run may do.
type connectionChoicesResponse struct {
	Data []ConnectionChoice `json:"data"`
	// Source is "grant" when the set is what the approved version was approved
	// to reach, and "persona" when it is what the caller reaches themselves.
	Source string `json:"source" example:"grant"`
	// Note states the source in the reader's terms, so a form can put it under
	// the control without composing the sentence itself.
	Note string `json:"note"`
}

// portalScriptConnections returns the connections a connection-typed parameter
// of this script may be bound to.
//
// @Summary      List the connections a script's parameters may name
// @Description  Returns the set a `connection` parameter chooses from. By default that is what the approved version was granted, which is the only set an approved run can use; `audience=draft` returns what a dry run executing as the caller would reach instead. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Produce      json
// @Param        id        path   string  true   "Script ID"
// @Param        audience  query  string  false  "run (default) or draft"
// @Success      200  {object}  connectionChoicesResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/connections [get]
func (h *Handler) portalScriptConnections(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	sc, ok := h.ownedScript(w, r, user)
	if !ok {
		return
	}
	reachable := bindableChoices(h.deps.Connections(r.Context(), ConnectionScope{
		Persona: user.Persona, Unrestricted: user.IsAdmin,
	}))
	if r.URL.Query().Get("audience") == audienceDraft {
		httpjson.WriteJSON(w, http.StatusOK, connectionChoicesResponse{
			Data:   orEmptyChoices(reachable),
			Source: "persona",
			Note: "A dry run executes as you, so these are the connections you reach " +
				"that a script may query. An approved run is confined to what its " +
				"approval granted instead.",
		})
		return
	}
	granted, ok := h.grantedConnections(w, r, sc)
	if !ok {
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, connectionChoicesResponse{
		Data:   describeChoices(granted, reachable),
		Source: "grant",
		Note:   grantNote(len(granted)),
	})
}

// grantedConnections is what the approved version may reach, or an empty set
// when nothing is approved. An unapproved script executes nothing, so there is
// no connection a run of it could name; saying so is better than offering the
// caller's own connections under a label that claims otherwise.
func (h *Handler) grantedConnections(w http.ResponseWriter, r *http.Request, sc *script.Script) ([]string, bool) {
	if !sc.Executable() {
		return nil, true
	}
	version, err := h.deps.Versions.GetVersionByID(r.Context(), sc.ApprovedVersionID)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the approved version")
		return nil, false
	}
	if version == nil {
		return nil, true
	}
	return version.Grants.Connections, true
}

// bindableChoices narrows an enumeration to the connections a connection-typed
// parameter can actually name.
//
// The caller reaches connections of every kind the deployment holds, but the
// value bound here is passed to platform.query, which reaches one kind
// (script.ConnectionParamKind). Offering the others offers values the run
// refuses, and it is why a granted name carried by several kinds used to be
// described as whichever kind the enumeration happened to reach first (#1384).
// Narrowing to the bindable kind resolves a granted name to the connection the
// run will actually use, and to that one only.
func bindableChoices(reachable []ConnectionChoice) []ConnectionChoice {
	out := make([]ConnectionChoice, 0, len(reachable))
	for _, c := range reachable {
		if c.Kind == script.ConnectionParamKind {
			out = append(out, c)
		}
	}
	return out
}

// describeChoices renders the granted names, borrowing the kind and the
// description from the caller's own enumeration where it carries them.
//
// A granted name the enumeration does not carry is still listed, bare. The
// grant is what the run is checked against, so dropping a name because this
// reader cannot enumerate it would hide a value the run would have accepted —
// and the grant is already on this script's version history, which is the same
// audience this route serves.
func describeChoices(granted []string, reachable []ConnectionChoice) []ConnectionChoice {
	out := make([]ConnectionChoice, 0, len(granted))
	for _, name := range granted {
		i := slices.IndexFunc(reachable, func(c ConnectionChoice) bool { return c.Name == name })
		if i < 0 {
			out = append(out, ConnectionChoice{Name: name})
			continue
		}
		out = append(out, reachable[i])
	}
	return out
}

// grantNote states where the set came from, including the case that reads as
// an empty list and is really a statement about the script.
func grantNote(granted int) string {
	if granted == 0 {
		return "Nothing is approved for this script, or its approval granted no connection, " +
			"so a run of it can name none."
	}
	return "These are the connections this script's approved version may reach. " +
		"A run naming any other is refused."
}

// orEmptyChoices normalizes a nil enumeration so the payload carries a list
// rather than null.
func orEmptyChoices(choices []ConnectionChoice) []ConnectionChoice {
	if choices == nil {
		return []ConnectionChoice{}
	}
	return choices
}
