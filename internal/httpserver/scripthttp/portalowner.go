package scripthttp

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Moving a script to another owner is the one script write that is an
// administrator's rather than an owner's (#1404). It exists because ownership
// is now the whole of what a script is: who sees it, edits it, runs it, and
// under whose authority it runs unattended. The named use is moving a script to
// an administrator so a schedule fires it with the reach the administrator has
// rather than the reach the person who wrote it had.
//
// It is not an edit. The edit funnel snapshots when the substance moved, and
// nothing about the code changes here; the store writes the version directly,
// authored by the administrator, because the authority that version carries IS
// what the transfer changes.

// maxOwnerBodyBytes bounds a transfer request. The body is one address.
const maxOwnerBodyBytes = 4 << 10

// auditToolTransfer is the tool name the transfer is recorded under, so an
// administrator filtering the audit log by an admin action finds it by name.
const auditToolTransfer = "script_transfer_owner"

// ownerRequest is a change of a script's owner.
type ownerRequest struct {
	OwnerEmail string `json:"owner_email" example:"admin@example.com"`
}

// ownerResponse reports the completed transfer.
type ownerResponse struct {
	// OwnerEmail is the address the script now belongs to, normalized.
	OwnerEmail string `json:"owner_email" example:"admin@example.com"`
	// Version is the version the transfer recorded, which is the version a run
	// now executes and whose captured roles it presents.
	Version int `json:"version" example:"4"`
	// Message states the consequence in the administrator's terms.
	Message string `json:"message" example:"daily-sales-report now belongs to admin@example.com and runs with your access."`
}

// portalTransferOwner moves a script to a new owner.
//
// @Summary      Transfer a script to a new owner
// @Description  Moves a managed script to another person. Restricted to administrators. The move is recorded as a new version authored by the administrator, and a run of the script from now on presents the roles that administrator held, so transferring a script to an administrator is how it comes to run with administrative reach. Refused with 409 when the new owner already keeps a script of the same name.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id     path  string        true  "Script ID"
// @Param        owner  body  ownerRequest  true  "New owner"
// @Success      200  {object}  ownerResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/owner [put]
func (h *Handler) portalTransferOwner(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	if !user.IsAdmin {
		httpjson.WriteError(w, http.StatusForbidden, "only an administrator can change a script's owner")
		return
	}
	sc, ok := h.ownedScript(w, r, user)
	if !ok {
		return
	}
	var req ownerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOwnerBodyBytes)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// The pre-transfer state is captured before the write, not read back off
	// sc afterwards: what the audit row is for is naming where the script came
	// from, and a store that hands out a shared record would otherwise leave
	// both ends of the move reading as the destination.
	moved := transferRecord{ScriptID: sc.ID, Name: sc.Name, From: sc.OwnerEmail, To: req.OwnerEmail}
	err := h.deps.Scripts.Transfer(r.Context(), sc.ID, req.OwnerEmail, editAuthor(user))
	h.auditTransfer(r, user, moved, err)
	if err != nil {
		writeTransferError(w, err)
		return
	}
	after, readErr := h.deps.Scripts.GetByID(r.Context(), moved.ScriptID)
	if readErr != nil || after == nil {
		// The transfer landed; only the read-back failed. Report the address
		// that was asked for rather than failing a write that succeeded.
		httpjson.WriteJSON(w, http.StatusOK, ownerResponse{
			OwnerEmail: moved.To,
			Message:    transferMessage(moved.Name, moved.To, user),
		})
		return
	}
	slog.Info("script owner transferred", "script_id", moved.ScriptID,
		"from", moved.From, "to", after.OwnerEmail, "by", user.owner())
	httpjson.WriteJSON(w, http.StatusOK, ownerResponse{
		OwnerEmail: after.OwnerEmail,
		Version:    after.Version,
		Message:    transferMessage(after.Name, after.OwnerEmail, user),
	})
}

// transferMessage states what the transfer means for the next run, which is the
// question an administrator presses the button with. Moving a script to
// themselves is the case worth naming outright: it is how a script comes to run
// with administrative reach.
func transferMessage(name, owner string, user *PortalIdentity) string {
	if ownsEmail(owner, user.owner()) {
		return name + " now belongs to you and runs with the access you hold."
	}
	return name + " now belongs to " + owner + " and runs with the access you hold, captured now."
}

// writeTransferError maps a transfer failure to a status. The two a caller can
// act on are named: a name the receiving owner already uses is a conflict, and
// anything the domain refused is a bad request. Everything else is the
// platform's own failure and its detail stays in the log.
func writeTransferError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, script.ErrNameTaken):
		httpjson.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, script.ErrVersionConflict):
		httpjson.WriteError(w, http.StatusConflict, err.Error())
	default:
		slog.Error("failed to transfer script", "error", err)
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	}
}

// transferRecord is one move, as it stood when it was requested: what the audit
// row names and what the response falls back to.
type transferRecord struct {
	ScriptID string
	Name     string
	From     string
	To       string
}

// auditTransfer records the transfer as an administrative event, naming both
// ends of the move. Best-effort: a logging failure is warned and swallowed so
// it never fails the request that already happened.
//
// It is written whether the transfer succeeded or failed, because a refused
// attempt to move somebody else's automation is exactly as much of an
// administrative act as a successful one.
func (h *Handler) auditTransfer(
	r *http.Request, user *PortalIdentity, moved transferRecord, opErr error,
) {
	if h.deps.Audit == nil {
		return
	}
	ev := audit.NewEvent(auditToolTransfer)
	ev.UserID = user.UserID
	ev.UserEmail = user.Email
	ev.Persona = user.Persona
	ev.Source = "portal"
	ev.Transport = "http"
	ev.EventKind = audit.EventTypeAdmin
	ev.Authorized = true
	ev.Success = opErr == nil
	ev.Parameters = map[string]any{
		"script":     moved.Name,
		"script_id":  moved.ScriptID,
		"from_owner": moved.From,
		"to_owner":   moved.To,
	}
	if opErr != nil {
		ev.ErrorMessage = opErr.Error()
	}
	if err := h.deps.Audit.Log(r.Context(), *ev); err != nil {
		slog.Warn("script transfer audit log failed", "error", err, "script_id", moved.ScriptID)
	}
}
