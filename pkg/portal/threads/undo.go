package threads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Taking back a status change (#2532). The person who changed a thread's
// status may undo it for StatusUndoWindow, and only until someone else adds
// an event to the thread. Undo restores the status the change replaced and
// removes the line it wrote, so the thread reads as if the change never
// happened; the audit log keeps both. After that, a change is reverted by
// picking the earlier status, which writes a line of its own.

// StatusUndoWindow is how long after a status change its author may undo it.
const StatusUndoWindow = 10 * time.Minute

// Errors an undo refuses with.
var (
	// ErrUndoNotStatus is an event that is not a status change.
	ErrUndoNotStatus = errors.New("only a status change can be undone")
	// ErrUndoNotAuthor is a caller who did not make the change.
	ErrUndoNotAuthor = errors.New("only the person who changed the status can undo it")
	// ErrUndoExpired is a change older than StatusUndoWindow.
	ErrUndoExpired = errors.New("a status change can be undone for 10 minutes; pick the earlier status instead")
	// ErrUndoSuperseded is a change someone else has added to the thread
	// since, or one a later change replaced.
	ErrUndoSuperseded = errors.New("the thread changed since; pick the earlier status instead")
)

// StatusUndoer takes back a status change. The PostgreSQL thread store
// implements it; it is apart from ThreadStore so the stores that never undo
// need not.
type StatusUndoer interface {
	// UndoStatusChange restores the status the event replaced and removes
	// the event, returning the restored status.
	UndoStatusChange(ctx context.Context, u StatusUndo) (string, error)
}

// StatusUndo names the change to undo and who asks.
type StatusUndo struct {
	ThreadID   string
	EventID    string
	ActorID    string
	ActorEmail string
	// Now is when the undo is asked, which the window is measured to.
	Now time.Time
}

// UndoStatusChange undoes a status change in one transaction: the event and
// the thread are locked, every condition is checked against what is stored,
// and the status is restored and the event deleted together.
func (s *postgresThreadStore) UndoStatusChange(ctx context.Context, u StatusUndo) (string, error) { //nolint:revive // interface impl
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf(errBeginTx, err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	var e storedChange
	err = tx.QueryRowContext(ctx, `
		SELECT event_type, author_id, author_email, created_at, metadata FROM portal_thread_events
		 WHERE thread_id = $1 AND id = $2 FOR UPDATE`, u.ThreadID, u.EventID,
	).Scan(&e.kind, &e.authorID, &e.authorEmail, &e.created, &e.metadata)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrEventNotFound
	}
	if err != nil {
		return "", fmt.Errorf("loading the status change: %w", err)
	}
	change, err := undoable(e, u)
	if err != nil {
		return "", err
	}
	if err := undoStillCurrent(ctx, tx, u, e.created, change.NewStatus); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE portal_threads SET status = $2, updated_at = NOW() WHERE id = $1`, u.ThreadID, change.OldStatus); err != nil {
		return "", fmt.Errorf("restoring the status: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM portal_thread_events WHERE thread_id = $1 AND id = $2`, u.ThreadID, u.EventID); err != nil {
		return "", fmt.Errorf("removing the status change: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf(errCommitTx, err)
	}
	return change.OldStatus, nil
}

// statusChange is what a status change event's metadata records.
type statusChange struct {
	OldStatus string `json:"old_status"`
	NewStatus string `json:"new_status"`
}

// storedChange is the event an undo names, as it is stored.
type storedChange struct {
	kind, authorID, authorEmail string
	created                     time.Time
	metadata                    []byte
}

// undoable checks the event alone: a status change, by the caller, within
// the window, naming the status it replaced.
func undoable(e storedChange, u StatusUndo) (statusChange, error) {
	var c statusChange
	if e.kind != EventTypeStatusChange && e.kind != EventTypeResolution {
		return c, ErrUndoNotStatus
	}
	if !sameActor(e.authorID, e.authorEmail, u.ActorID, u.ActorEmail) {
		return c, ErrUndoNotAuthor
	}
	if u.Now.Sub(e.created) > StatusUndoWindow {
		return c, ErrUndoExpired
	}
	if err := json.Unmarshal(e.metadata, &c); err != nil || !ValidThreadStatus(c.OldStatus) {
		return c, ErrUndoNotStatus
	}
	return c, nil
}

// undoStillCurrent checks the thread: its status is still the one the change
// set, and nobody else has added an event since.
func undoStillCurrent(ctx context.Context, tx *sql.Tx, u StatusUndo, created time.Time, newStatus string) error {
	var status string
	var later bool
	err := tx.QueryRowContext(ctx, `
		SELECT t.status, EXISTS (
		  SELECT 1 FROM portal_thread_events e
		   WHERE e.thread_id = t.id AND e.id <> $2
		     AND (e.created_at, e.id) > ($3, $2)
		     AND NOT ((e.author_id <> '' AND e.author_id = $4) OR (e.author_email <> '' AND LOWER(e.author_email) = LOWER($5))))
		  FROM portal_threads t WHERE t.id = $1 AND t.deleted_at IS NULL FOR UPDATE OF t`,
		u.ThreadID, u.EventID, created, u.ActorID, u.ActorEmail,
	).Scan(&status, &later)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEventNotFound
	}
	if err != nil {
		return fmt.Errorf("loading the thread: %w", err)
	}
	if later || status != newStatus {
		return ErrUndoSuperseded
	}
	return nil
}

// sameActor reports whether the caller is the event's author, by the id
// they signed in with or their address.
func sameActor(authorID, authorEmail, actorID, actorEmail string) bool {
	if authorID != "" && authorID == actorID {
		return true
	}
	return authorEmail != "" && strings.EqualFold(authorEmail, actorEmail)
}
