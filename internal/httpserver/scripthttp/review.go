package scripthttp

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// reviewDiffContext is how many unchanged lines surround each hunk of the code
// diff a reviewer reads. The tool surface renders diffs at zero context because
// a model reads the change alone; a person deciding whether to approve code
// needs to see what the change sits in.
const reviewDiffContext = 3

// pendingReviewResponse is the review queue payload.
type pendingReviewResponse struct {
	Data  []script.PendingReview `json:"data"`
	Total int                    `json:"total" example:"2"`
}

// listPendingReviews returns every version awaiting approval, oldest first.
//
// @Summary      List script versions awaiting review
// @Description  Returns the versions waiting for a reviewer's decision across every script, oldest first: pending drafts, and the live version of any script that has never had an approved version. One row per script, because approving a version supersedes that script's other pending drafts.
// @Tags         Scripts
// @Produce      json
// @Success      200  {object}  pendingReviewResponse
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts/reviews [get]
func (h *Handler) listPendingReviews(w http.ResponseWriter, r *http.Request) {
	pending, err := h.deps.Reviews.ListPendingReviews(r.Context())
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the script review queue")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, pendingReviewResponse{Data: pending, Total: len(pending)})
}

// rejectVersion takes a pending draft out of consideration.
//
// @Summary      Reject a script version
// @Description  Marks a pending draft rejected. The live script and its approved version are untouched, so nothing about what executes changes. Only a pending draft can be rejected: declining the live version of a never-approved script means leaving it unapproved, which is already its state.
// @Tags         Scripts
// @Produce      json
// @Param        id       path  string  true  "Script ID"
// @Param        version  path  int     true  "Version number"
// @Success      200  {object}  map[string]string
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts/{id}/versions/{version}/reject [post]
func (h *Handler) rejectVersion(w http.ResponseWriter, r *http.Request) {
	v, ok := h.loadVersion(w, r)
	if !ok {
		return
	}
	if err := h.deps.Rejections.RejectVersion(r.Context(), v.ScriptID, v.Version); err != nil {
		writeDecisionError(w, err, "failed to reject version")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, map[string]string{"status": script.VersionStatusRejected})
}

// approvedBaseline is the version the platform executes today: the other half
// of both diffs a reviewer reads.
//
// It is part of the review response rather than a second request the client
// makes for itself. The two halves have to describe one moment — an approval
// landing between the two reads would show a reviewer a change against code
// that is no longer the baseline, and the diff would look smaller or larger
// than the decision actually is.
type approvedBaseline struct {
	// Version is the approved version's number, and VersionID its id.
	Version   int    `json:"version" example:"2"`
	VersionID string `json:"version_id" example:"sver_a1b2c3d4"`
	// Grants is the capability set that version executes under today: what the
	// script already holds, against which the proposed grant is read.
	Grants script.Grants `json:"grants"`
	// ApprovedBy and ApprovedAt are who bound that grant and when.
	ApprovedBy string     `json:"approved_by,omitempty" example:"admin@example.com"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	// SourceDiff is the unified diff from the approved source to the version
	// under review. It is empty when the two carry identical source, which is
	// what re-approving a version to widen its grant looks like.
	SourceDiff string `json:"source_diff,omitempty"`
}

// baselineFor resolves the version the script's execution gate points at and
// renders it against v, or nil when the script has no approved version.
//
// A missing baseline is a first approval: nothing of the script executes today,
// so there is no diff to draw and the whole source is the change. The response
// says so by omitting the field rather than by rendering every line as an
// addition, which the reviewer already has in the version's own source.
func (h *Handler) baselineFor(r *http.Request, sc *script.Script, v *script.Version) *approvedBaseline {
	if sc.ApprovedVersionID == "" {
		return nil
	}
	approved, err := h.deps.Versions.GetVersionByID(r.Context(), sc.ApprovedVersionID)
	if err != nil || approved == nil {
		// The gate points at a version that cannot be read. Reporting no
		// baseline would claim this is a first approval, which is the opposite
		// of the truth, so the review is answered without the comparison and
		// the caller sees the field missing rather than a fabricated one. It is
		// logged because a gate pointing at an unreadable version is a defect
		// somewhere, and a review that silently loses half its diff would
		// otherwise be the only symptom.
		slog.Error("script execution gate points at a version that cannot be read",
			"script_id", logsan.SanitizeForLog(sc.ID),
			"approved_version_id", logsan.SanitizeForLog(sc.ApprovedVersionID))
		return nil
	}
	return &approvedBaseline{
		Version:    approved.Version,
		VersionID:  approved.ID,
		Grants:     approved.Grants,
		ApprovedBy: approved.ApprovedBy,
		ApprovedAt: approved.ApprovedAt,
		SourceDiff: textpatch.UnifiedDiffLabeled(approved.Source, v.Source,
			fmt.Sprintf("v%d (approved)", approved.Version),
			fmt.Sprintf("v%d (under review)", v.Version), reviewDiffContext),
	}
}
