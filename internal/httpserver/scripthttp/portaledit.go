package scripthttp

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The two things a script's owner writes from the portal: its SOURCE (#1307)
// and what it SAYS about itself (#1369). They live together because they cross
// one gate — script.ApplyEdit, the funnel every mutation surface crosses — and
// differ in exactly one way, which is worth stating beside each other rather
// than in two files that each describe half of it.
//
// Until these routes existed the only way to change a script was to ask an
// agent, which is a strange thing to require of the person who owns the
// automation and is looking straight at it. Neither is a new authority, and
// nothing here can approve anything.
//
// An edit to the SOURCE of a script with an approved version becomes a DRAFT
// awaiting review, and the approved version keeps running until somebody
// approves the change. An edit to what the script SAYS applies at once, because
// script.RequiresReview keys on the source and the parameter contract alone: a
// description is not an input to any decision the platform makes. Both are
// captured as versions.
//
// Neither route touches scope, personas, status, or the parameter contract.
// Those are structured decisions with their own rules — and a mixed edit is
// refused by the domain anyway — so the page that shows the code changes the
// code, and the page that shows the document changes the document.

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
	// Approved is true when the saved version is now the version the platform
	// executes, because this is the owner's own personal script and the platform
	// approved it for them (#1367).
	Approved bool `json:"approved,omitempty" example:"true"`
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

// applyEdit puts the edit through script.ApplyEdit — the one gate every mutation
// surface crosses — writing the error response and reporting false when it
// fails. Both portal write routes go through it, so neither can acquire its own
// idea of what an edit is allowed to do.
func (h *Handler) applyEdit(
	w http.ResponseWriter,
	r *http.Request,
	before, after *script.Script,
	user *PortalIdentity,
) (script.EditOutcome, bool) {
	outcome, err := script.ApplyEdit(r.Context(), h.deps.Scripts, script.Edit{
		Before: before, After: after, Author: editAuthor(user), Auto: h.deps.Auto,
	})
	if err != nil {
		writeEditError(w, err)
		return script.EditOutcome{}, false
	}
	return outcome, true
}

// landEdit applies a source edit and reports which of its two outcomes happened.
func (h *Handler) landEdit(
	w http.ResponseWriter,
	r *http.Request,
	before, after *script.Script,
	user *PortalIdentity,
) {
	outcome, ok := h.applyEdit(w, r, before, after, user)
	if !ok {
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
		Applied:  true,
		Approved: outcome.Auto.Approved,
		Message:  savedMessage(after, outcome.Auto),
	})
}

// savedMessage states what an applied edit means for whether anything will run
// it, which is the question an owner presses save with (#1367).
//
// A refusal is quoted rather than summarized: it names the connection, the
// destination, or the shape of the code that stopped the approval, and that is
// the sentence the owner can act on.
func savedMessage(sc *script.Script, auto script.AutoOutcome) string {
	switch {
	case auto.Approved:
		return "Saved and approved. This script is yours alone, so the platform approved this version " +
			"for you and runs it under the access you hold. " + runsNow(sc)
	case auto.Reason != "":
		return "Saved, and not approved: " + auto.Reason +
			". Until a version is approved, nothing executes this script on its own; " +
			"dry-run it here to execute it as yourself."
	default:
		return "Saved. Nothing is approved for this script yet, so nothing executes it unattended."
	}
}

// runsNow reports what an approval actually buys this script, which is not
// always a run: the execution gate refuses a disabled or deprecated script
// whatever is approved on it, and a save that said "it runs now" over one would
// be a false statement the owner acts on.
func runsNow(sc *script.Script) string {
	switch {
	case !sc.Enabled:
		return "This script is disabled, so nothing executes it until it is enabled again."
	case sc.Status == script.StatusDeprecated:
		return "This script is deprecated, so nothing executes it."
	default:
		return "It runs now, and on its schedule."
	}
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

// maxMetadataBodyBytes bounds a metadata request. script.MaxDescriptionBytes
// bounds the description itself; this is the envelope around it, generous
// enough that a body inside the domain limit is never refused here.
const maxMetadataBodyBytes = 1 << 20

// metadataRequest is a change to what a script says about itself. Every field
// is a pointer so that "not sent" is distinct from "cleared": a client editing
// only the category must not blank the description by omitting it, and an owner
// deleting the last tag must not be read as having sent nothing.
type metadataRequest struct {
	DisplayName *string   `json:"display_name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Category    *string   `json:"category,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
}

// metadataResponse reports the saved state and, when the description has grown
// past the point of documenting one script, the advisory that says so.
type metadataResponse struct {
	// Version is the version number the script now carries. It advances when the
	// edit moved anything the snapshot holds and stays put when it did not, so a
	// client can tell a real change from a re-save of identical text.
	Version int `json:"version" example:"4"`
	// DescriptionNotice is the non-blocking suggestion that a very long
	// description belongs somewhere of its own. Empty for every description
	// under that threshold, which is nearly all of them.
	DescriptionNotice string `json:"description_notice,omitempty"`
	// Message states the outcome in the owner's terms.
	Message string `json:"message" example:"Saved."`
}

// portalSetMetadata saves what a script says about itself.
//
// @Summary      Edit what a script says about itself
// @Description  Saves the display name, markdown description, category and tags of a script the caller owns. None of these is review-gated, so the change applies immediately and the approved version keeps executing untouched; it is still captured as a version. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id        path  string           true  "Script ID"
// @Param        metadata  body  metadataRequest  true  "Documentation fields"
// @Success      200  {object}  metadataResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/metadata [put]
func (h *Handler) portalSetMetadata(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	sc, ok := h.ownedScript(w, r, user)
	if !ok {
		return
	}
	var req metadataRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMetadataBodyBytes)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	before := *sc
	after := *sc
	req.applyTo(&after)
	if err := after.Validate(); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := h.applyEdit(w, r, &before, &after, user); !ok {
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, metadataResponse{
		Version:           after.Version,
		DescriptionNotice: script.DescriptionNotice(after.Description),
		Message:           "Saved. This changes what the script says about itself and not what it does, so nothing was sent for review.",
	})
}

// applyTo copies the sent fields onto the script. Tags are copied rather than
// aliased: the request is short-lived and the script outlives the handler, so
// sharing the backing array would let one be mutated through the other.
func (r metadataRequest) applyTo(sc *script.Script) {
	if r.DisplayName != nil {
		sc.DisplayName = *r.DisplayName
	}
	if r.Description != nil {
		sc.Description = *r.Description
	}
	if r.Category != nil {
		sc.Category = *r.Category
	}
	if r.Tags != nil {
		sc.Tags = append([]string{}, *r.Tags...)
	}
}
