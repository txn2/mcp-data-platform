package feedbackapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/selfcall"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal/mention"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// Editing and deleting a message (#2514). Only the author edits; the author,
// the target's owner or editor, and an administrator delete. Only a comment
// event -- an opening message of a comment, question, correction or
// suggestion thread, and every reply -- can be changed: every other event
// records a decision and is refused, so an edit can never move a sign-off.

const (
	pathKeyEventID = "eventID"
	// feedbackToolName is the tool a portal edit or delete is audited as,
	// the one an agent makes the same change with.
	feedbackToolName  = "manage_feedback"
	auditSourcePortal = "portal"
	errEventsUnavail  = "editing messages is not available"
	errEventNotFound  = "message not found"
)

// editEventRequest is the body of PATCH /threads/{id}/events/{eventID}.
type editEventRequest struct {
	Body string `json:"body"`
}

// deleteEventResponse reports a deleted message, and whether the thread went
// with it (an opening message nothing else followed).
type deleteEventResponse struct {
	EventID       string `json:"event_id"`
	Deleted       bool   `json:"deleted"`
	ThreadDeleted bool   `json:"thread_deleted"`
}

// editThreadEvent handles PATCH /api/v1/portal/threads/{id}/events/{eventID}.
//
// @Summary      Edit a message
// @Description  Replaces the text of a message the caller wrote. Only comment events (the opening message of a comment, question, correction or suggestion thread, and every reply) can be edited; an approval, rejection, rating, status change or validation result cannot. The previous text is kept. A person the new text mentions who was not mentioned before is notified; nobody else is.
// @Tags         Feedback
// @Accept       json
// @Produce      json
// @Param        id       path  string            true  "Thread ID"
// @Param        eventID  path  string            true  "Event ID"
// @Param        body     body  editEventRequest  true  "New text"
// @Success      200  {object}  threads.ThreadEvent
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads/{id}/events/{eventID} [patch]
func (h *Handler) editThreadEvent(w http.ResponseWriter, r *http.Request) {
	c := h.loadEventForChange(w, r)
	if c == nil {
		return
	}
	user, thread, event := c.user, c.thread, c.event
	var req editEventRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "body is required")
		return
	}
	if !isAuthor(event, user) {
		httpjson.WriteError(w, http.StatusForbidden, "only the author of a message can edit it")
		return
	}
	mentions := h.resolveMentions(r.Context(), thread, body, user.Email)
	metadata, err := mention.ReplaceMentions(event.Metadata, mentions)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the message")
		return
	}
	edited, err := c.editor.EditEvent(r.Context(), threads.EventEdit{
		ThreadID: thread.ID, EventID: event.ID, Body: body, Metadata: metadata, EditorEmail: user.Email,
	})
	h.audit(r, c, "edit", err)
	if err != nil {
		writeEventChangeError(w, err)
		return
	}
	if added := newlyMentioned(mention.FromMetadata(event.Metadata), mentions); len(added) > 0 {
		h.notifyThreadEvent(r.Context(), threads.MessageNotice{
			Thread: thread, EventID: event.ID, ActorEmail: user.Email, Body: body,
			Mentioned: added, MentionsOnly: true,
		})
	}
	httpjson.WriteJSON(w, http.StatusOK, edited)
}

// deleteThreadEvent handles DELETE /api/v1/portal/threads/{id}/events/{eventID}.
//
// @Summary      Delete a message
// @Description  Deletes a message. Its author, the target's owner or editor, and an administrator may delete one. Only comment events can be deleted. The thread shows the message as deleted in its place and no route returns its text; deleting the opening message of a thread nothing else followed deletes the thread.
// @Tags         Feedback
// @Produce      json
// @Param        id       path  string  true  "Thread ID"
// @Param        eventID  path  string  true  "Event ID"
// @Success      200  {object}  deleteEventResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads/{id}/events/{eventID} [delete]
func (h *Handler) deleteThreadEvent(w http.ResponseWriter, r *http.Request) {
	c := h.loadEventForChange(w, r)
	if c == nil {
		return
	}
	user, thread, event := c.user, c.thread, c.event
	if !isAuthor(event, user) && !h.moderatesTarget(r, user, thread) {
		httpjson.WriteError(w, http.StatusForbidden, "only the author, the target owner, or an admin can delete this message")
		return
	}
	threadDeleted, err := c.editor.DeleteEvent(r.Context(), thread.ID, event.ID, user.Email)
	h.audit(r, c, "delete", err)
	if err != nil {
		writeEventChangeError(w, err)
		return
	}
	h.retractThreadMessage(r.Context(), event.ID)
	httpjson.WriteJSON(w, http.StatusOK, deleteEventResponse{EventID: event.ID, Deleted: true, ThreadDeleted: threadDeleted})
}

// eventChange is a message a caller asked to change, with the thread it is
// in and the store that changes it.
type eventChange struct {
	user   *access.User
	thread *threads.Thread
	editor threads.EventEditor
	event  *threads.ThreadEvent
}

// loadEventForChange loads the thread the caller may read, and the message
// named in the path. It writes the refusal itself and returns nil on one.
func (h *Handler) loadEventForChange(w http.ResponseWriter, r *http.Request) *eventChange {
	editor, ok := h.cfg.Threads.(threads.EventEditor)
	if !ok {
		httpjson.WriteError(w, http.StatusServiceUnavailable, errEventsUnavail)
		return nil
	}
	user, thread := h.loadThreadForRead(w, r)
	if thread == nil {
		return nil
	}
	event, err := editor.GetEvent(r.Context(), thread.ID, r.PathValue(pathKeyEventID))
	if err != nil {
		writeEventChangeError(w, err)
		return nil
	}
	if event.EventType != threads.EventTypeComment {
		writeEventChangeError(w, threads.ErrEventNotEditable)
		return nil
	}
	if event.DeletedAt != nil {
		writeEventChangeError(w, threads.ErrEventDeleted)
		return nil
	}
	return &eventChange{user: user, thread: thread, editor: editor, event: event}
}

// moderatesTarget reports whether the user owns or edits the thread's target,
// or administers the platform: the people who may remove any message on it.
// Unlike CanModerateThread it does not admit the thread's author, whose right
// covers their own messages only.
func (h *Handler) moderatesTarget(r *http.Request, user *access.User, thread *threads.Thread) bool {
	asTarget := *thread
	asTarget.AuthorID = noAuthor
	return h.access.CanModerateThread(r.Context(), user, &asTarget)
}

// noAuthor is an author id no caller signs in with, so the thread-author arm
// of CanModerateThread never admits anyone in moderatesTarget.
const noAuthor = "\x00"

// isAuthor reports whether the user wrote the event: the id they signed in
// with, or their address.
func isAuthor(e *threads.ThreadEvent, user *access.User) bool {
	if e.AuthorID != "" && e.AuthorID == user.UserID {
		return true
	}
	return e.AuthorEmail != "" && strings.EqualFold(e.AuthorEmail, user.Email)
}

// newlyMentioned returns the addresses in now that before did not hold.
func newlyMentioned(before, now []string) []string {
	had := make(map[string]struct{}, len(before))
	for _, b := range before {
		had[strings.ToLower(b)] = struct{}{}
	}
	added := make([]string, 0, len(now))
	for _, n := range now {
		if _, ok := had[strings.ToLower(n)]; !ok {
			added = append(added, n)
		}
	}
	return added
}

// writeEventChangeError answers a refused or failed edit or delete.
func writeEventChangeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, threads.ErrEventNotFound):
		httpjson.WriteError(w, http.StatusNotFound, errEventNotFound)
	case errors.Is(err, threads.ErrEventNotEditable), errors.Is(err, threads.ErrEventDeleted):
		httpjson.WriteError(w, http.StatusConflict, err.Error())
	default:
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to change the message")
	}
}

// audit records a portal edit or delete as the manage_feedback call an agent
// would make. The parameters name the thread and the message, never the text:
// a deleted message's text is in no route, the audit log's among them. A
// change an agent made through manage_feedback is served here in process and
// already audited as that tool call, so it is not recorded twice.
func (h *Handler) audit(r *http.Request, c *eventChange, action string, opErr error) {
	if h.cfg.Audit == nil {
		return
	}
	if _, viaTool := selfcall.From(r.Context()); viaTool {
		return
	}
	ev := middleware.AuditEvent{
		ID: audit.NewEventID(), Timestamp: time.Now().UTC(), ToolName: feedbackToolName,
		Source: auditSourcePortal, Transport: "http", Authorized: true, Success: opErr == nil,
		UserID: c.user.UserID, UserEmail: c.user.Email,
		Parameters: map[string]any{"action": action, "thread_id": c.thread.ID, "event_id": c.event.ID},
	}
	if h.cfg.PersonaName != nil {
		ev.Persona = h.cfg.PersonaName(c.user.Roles)
	}
	if opErr != nil {
		ev.ErrorMessage = opErr.Error()
	}
	if err := h.cfg.Audit.Log(r.Context(), ev); err != nil {
		slog.Warn("feedback audit failed", "action", action, // #nosec G706 -- structured slog call; error sanitized
			logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}
