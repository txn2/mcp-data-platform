package scripthttp

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Editing a script's source from the portal (#1307).
//
// Until now the only way to change a script was to ask an agent, which is a
// strange thing to require of the person who owns the automation and is looking
// straight at the code. The edit is not a new authority: it crosses the same
// gate every other mutation crosses (script.ApplyEdit), so an edit to a script
// with an approved version becomes a DRAFT awaiting review and the approved
// version keeps running until somebody approves the change. Nothing here can
// approve anything.
//
// The route deliberately edits the source and nothing else. Scope, personas,
// status, and the parameter contract are structured decisions with their own
// rules — and a mixed edit is refused by the domain anyway — so the page that
// shows the code changes the code.

// maxSourceBodyBytes bounds an edit request. script.MaxSourceLength bounds the
// source itself; this is the envelope around it, generous enough that a body
// inside the domain limit is never refused here.
const maxSourceBodyBytes = 1 << 20

// sourceRequest is a change to a script's code.
type sourceRequest struct {
	Source string `json:"source"`
}

// sourceResponse reports where the edit landed: on the live script, or in the
// review queue as a draft. A page that said only "saved" would leave an owner
// believing their change is running when it is waiting for a reviewer.
type sourceResponse struct {
	// Applied is true when the live script now carries this source.
	Applied bool `json:"applied" example:"false"`
	// PendingVersion is the draft version awaiting review, zero when applied.
	PendingVersion int `json:"pending_version,omitempty" example:"4"`
	// Message states the outcome in the owner's terms.
	Message string `json:"message" example:"Saved as version 4, awaiting review."`
}

// portalSetSource saves a new version of a script's source.
//
// @Summary      Edit a script's source
// @Description  Saves new Starlark for a script the caller owns. A script with an approved version keeps executing that version and the edit becomes a draft awaiting review; a script with nothing approved yet applies the edit directly. The source is parsed before anything is stored. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id      path  string         true  "Script ID"
// @Param        source  body  sourceRequest  true  "Starlark source"
// @Success      200  {object}  sourceResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/source [put]
func (h *Handler) portalSetSource(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	sc, ok := h.ownedScript(w, r, user)
	if !ok {
		return
	}
	var req sourceRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSourceBodyBytes)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if detail := refuseSource(req.Source); detail != "" {
		httpjson.WriteError(w, http.StatusBadRequest, detail)
		return
	}
	before := *sc
	after := *sc
	after.Source = req.Source
	if err := after.Validate(); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.landEdit(w, r, &before, &after, user)
}

// refuseSource applies the same static read the tool applies before storing an
// edit: source that does not parse is refused here rather than at the next run,
// where nobody is watching. The detail names the first finding, which is what a
// person needs to fix it.
func refuseSource(source string) string {
	if report := scriptrun.Validate(source); !report.OK {
		detail := "the source does not parse, so it was not saved"
		if len(report.Findings) > 0 {
			return detail + ": " + report.Findings[0].Message
		}
		return detail
	}
	return ""
}

// landEdit puts the edit through script.ApplyEdit — the one gate every mutation
// surface crosses — and reports which of its two outcomes happened.
func (h *Handler) landEdit(
	w http.ResponseWriter,
	r *http.Request,
	before, after *script.Script,
	user *PortalIdentity,
) {
	outcome, err := script.ApplyEdit(r.Context(), h.deps.Scripts, before, after, editAuthor(user))
	if err != nil {
		writeEditError(w, err)
		return
	}
	if outcome.PendingVersion > 0 {
		httpjson.WriteJSON(w, http.StatusOK, sourceResponse{
			PendingVersion: outcome.PendingVersion,
			Message: "This script has an approved version, so the change was saved as a draft " +
				"awaiting review. The approved version keeps running until the draft is approved.",
		})
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, sourceResponse{
		Applied: true,
		Message: "Saved. Nothing is approved for this script yet, so nothing executes it unattended.",
	})
}

// editAuthor is who wrote the version and the authority they held while writing
// it. The roles are the load-bearing half: approving a version binds exactly
// these, so a script can never do what the person who wrote it could not.
func editAuthor(user *PortalIdentity) script.Author {
	roles := user.Roles
	if roles == nil {
		roles = []string{}
	}
	return script.Author{Email: user.owner(), Roles: roles}
}

// writeEditError maps an edit failure to a status. The one a caller can act on
// is the mixed-edit refusal, which is matched by sentinel; anything else is the
// platform's own failure and its detail stays in the log.
func writeEditError(w http.ResponseWriter, err error) {
	if errors.Is(err, script.ErrReviewRequiredMixedEdit) {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httpjson.WriteError(w, http.StatusInternalServerError, "failed to save the source")
}
