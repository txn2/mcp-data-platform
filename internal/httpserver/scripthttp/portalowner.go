package scripthttp

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// This file holds the two portal writes that are about a script's existence
// rather than its contents: moving it to another owner, and removing it. They
// are read together because they are the two ways a script stops being one
// person's, and they share the audit path — an act on somebody's automation
// that is not an edit is recorded by name.
//
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
// auditToolDelete is the same for a removal (#1575): the tool surface's own
// delete is already in the log as a manage_script call, and a portal that
// removed an automation silently would be the one way to do it without a trace.
const (
	auditToolTransfer = "script_transfer_owner"
	auditToolDelete   = "script_delete"
)

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
	slog.Info("script owner transferred", keyScriptID, moved.ScriptID,
		"from", logsan.SanitizeForLog(moved.From), "to", logsan.SanitizeForLog(after.OwnerEmail),
		"by", logsan.SanitizeForLog(user.owner()))
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
		slog.Error("failed to transfer script", "error", logsan.SanitizeForLog(err.Error()))
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
// ends of the move.
func (h *Handler) auditTransfer(
	r *http.Request, user *PortalIdentity, moved transferRecord, opErr error,
) {
	h.auditScriptAct(r, user, scriptAct{tool: auditToolTransfer, scriptID: moved.ScriptID}, map[string]any{
		"script":     moved.Name,
		"from_owner": moved.From,
		"to_owner":   moved.To,
	}, opErr)
}

// scriptAct names one act on one script: what it is recorded under, and which
// script it touched. The two travel together because neither is meaningful in
// the log without the other.
type scriptAct struct {
	tool     string
	scriptID string
}

// auditScriptAct records one act on a script that is not an edit, under the
// tool name that act is filtered by. It stamps the script's id onto the
// parameters itself, so every act on a script is findable by the same key
// whatever else the act records. Best-effort: a logging failure is warned and
// swallowed so it never fails the request that already happened.
//
// It is written whether the store accepted the act or refused it, because an
// attempt on somebody else's automation that failed at the write is as much of
// an administrative act as one that succeeded.
//
// A caller the ROUTE refuses reaches no act at all: both handlers resolve the
// script through ownedScript first and answer there, so a delete or a transfer
// aimed at a script the caller may not see is answered without a record. That
// is the same shape every other portal script route has, and changing it here
// alone would make this one route's log mean something different from the rest.
func (h *Handler) auditScriptAct(
	r *http.Request, user *PortalIdentity, act scriptAct, params map[string]any, opErr error,
) {
	if h.deps.Audit == nil {
		return
	}
	params[keyScriptID] = act.scriptID
	ev := audit.NewEvent(act.tool)
	ev.UserID = user.UserID
	ev.UserEmail = user.Email
	ev.Persona = user.Persona
	ev.Source = "portal"
	ev.Transport = "http"
	ev.EventKind = audit.EventTypeAdmin
	ev.Authorized = true
	ev.Success = opErr == nil
	ev.Parameters = params
	if opErr != nil {
		ev.ErrorMessage = opErr.Error()
	}
	if err := h.deps.Audit.Log(r.Context(), *ev); err != nil {
		slog.Warn("script act audit log failed", "error", logsan.SanitizeForLog(err.Error()),
			"tool", act.tool, keyScriptID, act.scriptID)
	}
}

// deleteResponse reports the completed removal. It names the script rather than
// answering with an empty 204 because this route is read by callers with no
// dialog in front of them -- an operator with curl, an agent driving the REST
// API -- and they need the same account of what went and what stayed that the
// portal's confirmation gives a person before the fact. The portal's own
// control leaves for the listing on success and renders none of it.
type deleteResponse struct {
	Status string `json:"status" example:"deleted"`
	Name   string `json:"name" example:"daily-sales-report"`
	// Message states what went and what did not, in the terms the confirmation
	// stated them before the delete ran.
	Message string `json:"message" example:"daily-sales-report is gone, with its saved versions, its schedule, its run history and the state it carried."`
}

// deleteMessage states the consequence, both halves of it. What went is the
// part a person is warned about; what stayed is the part they are most likely
// to be wrong about, because "delete the script" reads to many people as
// "delete the reports it wrote".
func deleteMessage(name string) string {
	return name + " is gone, with its saved versions, its schedule, its run history and the state it carried. " +
		"The assets and resources it wrote remain, and they still record that it wrote them."
}

// portalDeleteScript removes a script.
//
// @Summary      Delete a script
// @Description  Removes a managed script and everything that belongs to it: its saved versions, its schedule, its run history, and the state it carried between runs. Restricted to the script's owner and to administrators, and answered as not-found for anybody else, so the refusal cannot be used to learn that a script exists. The assets and resources the script wrote are NOT removed, and the records naming it as their producer remain. It is the same removal manage_script command=delete performs, through the same store.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  deleteResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id} [delete]
func (h *Handler) portalDeleteScript(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	// ownedScript applies the rule the tool applies through editable: the
	// script's owner, or an administrator over any script. A script that does
	// not exist and one this caller may not have are the same 404 here, which
	// is also what answers a caller who deletes the same script twice.
	sc, ok := h.ownedScript(w, r, user)
	if !ok {
		return
	}
	// Read off the record before the write, for the reason the transfer does:
	// what the audit row is for is naming what was removed, and after the
	// delete there is nothing left to read it from.
	id, name, owner := sc.ID, sc.Name, sc.OwnerEmail
	err := h.deps.Scripts.Delete(r.Context(), id)
	h.auditScriptAct(r, user, scriptAct{tool: auditToolDelete, scriptID: id}, map[string]any{
		"script": name, "owner": owner,
	}, err)
	if err != nil {
		writeDeleteError(w, err, id)
		return
	}
	slog.Info("script deleted", keyScriptID, id,
		"name", logsan.SanitizeForLog(name), "by", logsan.SanitizeForLog(user.owner()))
	httpjson.WriteJSON(w, http.StatusOK, deleteResponse{
		Status: "deleted", Name: name, Message: deleteMessage(name),
	})
}

// writeDeleteError maps a delete failure to a status. The one a caller can act
// on is a script that was already gone: every caller reads the script before
// removing it, so reaching this means somebody removed it in between, and the
// answer is the same not-found a second delete of the same script gets rather
// than a server error over a script that is in fact deleted. Everything else is
// the platform's own failure and its detail stays in the log.
func writeDeleteError(w http.ResponseWriter, err error, scriptID string) {
	if errors.Is(err, script.ErrNotFound) {
		httpjson.WriteError(w, http.StatusNotFound, errScriptNot)
		return
	}
	slog.Error("failed to delete script", "error", logsan.SanitizeForLog(err.Error()), keyScriptID, scriptID)
	httpjson.WriteError(w, http.StatusInternalServerError, "failed to delete script")
}
