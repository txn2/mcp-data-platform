package scripthttp

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptbehavior"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The two things a script's owner writes from the portal: its SOURCE (#1307)
// and what it SAYS about itself (#1369). They live together because they cross
// one gate — script.ApplyEdit, the funnel every mutation surface crosses — and
// both apply to the live row and are captured as versions: saving a version
// makes it the version that runs.
//
// Neither route touches the status or the parameter contract, which are
// structured decisions with their own rules, nor the owner, which is the
// administrator's transfer (#1404). The page that shows the code changes the
// code, and the page that shows the document changes the document.

// maxSourceBodyBytes bounds an edit request. script.MaxSourceLength bounds the
// source itself; this is the envelope around it, generous enough that a body
// inside the domain limit is never refused here.
const maxSourceBodyBytes = 1 << 20

// sourceRequest is a change to a script's code. ChangeSummary and UserAgreed
// carry a change in what the automation does (#1942): what it will now do
// differently, in plain words, and that the person saving agreed to it.
type sourceRequest struct {
	Source        string `json:"source"`
	ChangeSummary string `json:"change_summary,omitempty"`
	UserAgreed    bool   `json:"user_agreed,omitempty"`
}

// changeNeededProblem is the refusal of a save that changes what the
// automation does without a summary of the change (#1942): the differences
// the person saving is asked to describe and agree to.
type changeNeededProblem struct {
	httpjson.ProblemDetail
	Differences []scriptbehavior.Difference `json:"differences"`
}

// sourceResponse reports the saved edit.
type sourceResponse struct {
	// Applied is true when the live script now carries this source, which is
	// every successful save.
	Applied bool `json:"applied" example:"true"`
	// Message states the outcome in the owner's terms.
	Message string `json:"message" example:"Saved, and this version is what runs now."`
}

// portalSetSource saves a new version of a script's source.
//
// @Summary      Edit a script's source
// @Description  Saves new Starlark for a script the caller owns; the saved version is the version that runs. The source is parsed, formatted, linted and its tests run before anything is stored (400 names what failed). A version whose replay of the script's recent runs, or whose reach, differs from the saved one is refused with 409 and the differences until it carries change_summary and user_agreed. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id      path  string         true  "Script ID"
// @Param        source  body  sourceRequest  true  "Starlark source"
// @Success      200  {object}  sourceResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  changeNeededProblem
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
	gate := h.gate()
	gated := gate.Check(r.Context(), scriptsave.Request{
		Existing: sc, Name: sc.Name, Source: req.Source,
		Caller:        scriptsave.Caller{Email: user.owner(), Admin: user.IsAdmin},
		ChangeSummary: req.ChangeSummary, Agreed: req.UserAgreed,
	})
	if gated.ChangeNeeded {
		httpjson.WriteJSON(w, http.StatusConflict, changeNeededProblem{
			ProblemDetail: httpjson.ProblemDetail{
				Type: httpjson.ProblemTypePrefix + "behavior_change", Title: http.StatusText(http.StatusConflict),
				Status: http.StatusConflict, Detail: gated.Refusal,
			},
			Differences: gated.Differences,
		})
		return
	}
	if gated.Refused() {
		httpjson.WriteError(w, http.StatusBadRequest, gated.Refusal)
		return
	}
	before := *sc
	after := *sc
	gated.Apply(&after)
	if err := after.Validate(); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.landEdit(w, r, &before, &after, user)
}

// gate is the save gate every source edit crosses, the lint and tests alone
// when the deployment gave none.
func (h *Handler) gate() *scriptsave.Gate {
	if h.deps.Gate == nil {
		return &scriptsave.Gate{Destinations: h.deps.Destinations}
	}
	return h.deps.Gate
}

// refuseSource applies the same static read the tool applies before storing an
// edit: source that does not parse is refused here rather than at the next run,
// where nobody is watching. The detail names the first finding, which is what a
// person needs to fix it.
//
// A save is deliberately NOT checked against the deployment's declared
// destinations: the declared set is configuration that changes under a stored
// script, and refusing the save would take away the edit that fixes it. The
// dry-run path checks it (scriptlint.DraftRefusal), because that is the surface
// answering "would this run".
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
) bool {
	err := script.ApplyEdit(r.Context(), h.deps.Scripts, script.Edit{
		Before: before, After: after, Author: editAuthor(user),
	})
	if err != nil {
		writeEditError(w, err)
		return false
	}
	return true
}

// landEdit applies a source edit and reports it.
func (h *Handler) landEdit(
	w http.ResponseWriter,
	r *http.Request,
	before, after *script.Script,
	user *PortalIdentity,
) {
	if !h.applyEdit(w, r, before, after, user) {
		return
	}
	// The recordings the saved tests name are kept past the retention sweep.
	if err := h.gate().Keep(r.Context(), after, user.owner()); err != nil {
		slog.Warn("failed to keep the recordings a script's tests name", "script", after.Name, "error", err)
	}
	httpjson.WriteJSON(w, http.StatusOK, sourceResponse{
		Applied: true,
		Message: script.SavedMessage(after),
	})
}

// editAuthor is who wrote the version and the authority they held while
// writing it. The roles are the load-bearing half: a run of the version
// presents exactly these, so a script can never do what the person who wrote
// it could not.
func editAuthor(user *PortalIdentity) script.Author {
	roles := user.Roles
	if roles == nil {
		roles = []string{}
	}
	return script.Author{Email: user.owner(), Roles: roles}
}

// writeEditError maps an edit failure to a status. A version conflict is the
// one a caller can act on, matched by sentinel; anything else is the
// platform's own failure and its detail stays in the log.
func writeEditError(w http.ResponseWriter, err error) {
	if errors.Is(err, script.ErrVersionConflict) {
		httpjson.WriteError(w, http.StatusConflict, err.Error())
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
// @Description  Saves the display name, markdown description, category and tags of a script the caller owns. The change applies immediately and is captured as a version. Restricted to the script's owner and to administrators.
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
	if !h.applyEdit(w, r, &before, &after, user) {
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, metadataResponse{
		Version:           after.Version,
		DescriptionNotice: script.DescriptionNotice(after.Description),
		Message:           "Saved. This changes what the script says about itself and not what it does.",
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
