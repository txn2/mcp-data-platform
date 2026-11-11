package threads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Editing and deleting one message of a thread (#2514), and the people a
// thread involves (#2512).
//
// Only a comment event is a message a person can change: the opening message
// of a comment, question, correction or suggestion thread, and every reply.
// Every other event records a decision (an approval, a rating, a status
// change, a validation result) or a link the platform made, and is never
// edited or deleted, so editing a message can never change a sign-off count.

// Errors an edit or delete refuses with.
var (
	// ErrEventNotFound is a message that is not in the thread.
	ErrEventNotFound = errors.New("thread event not found")
	// ErrEventNotEditable is an event that records a decision or a link,
	// which is never edited or deleted.
	ErrEventNotEditable = errors.New("only comment and reply text can be edited or deleted")
	// ErrEventDeleted is a message already deleted.
	ErrEventDeleted = errors.New("the message was deleted")
)

// eventSelectColumns reads an event with its text withheld once it is
// deleted: the body and the metadata (which names the people it mentioned)
// come back empty, so no read of a deleted message returns what it said.
const eventSelectColumns = `id, thread_id, event_type, author_id, author_email,
	CASE WHEN deleted_at IS NULL THEN body END,
	rating, parent_event_id,
	CASE WHEN deleted_at IS NULL THEN metadata ELSE '{}'::jsonb END,
	created_at, edited_at, deleted_at`

// EventEdit is one change to a message's text.
type EventEdit struct {
	ThreadID string
	EventID  string
	Body     string
	// Metadata is the event's metadata after the edit, carrying the mentions
	// the new text delivers.
	Metadata json.RawMessage
	// EditorEmail is who made the change, kept beside the previous text.
	EditorEmail string
}

// EventEditor edits and deletes one message of a thread and lists the people
// a thread involves. The PostgreSQL thread store implements it; it is apart
// from ThreadStore so the stores that never change a message need not.
type EventEditor interface {
	// GetEvent returns one event of the thread, its text withheld when it
	// was deleted.
	GetEvent(ctx context.Context, threadID, eventID string) (*ThreadEvent, error)
	// EditEvent replaces a comment's text, keeping the previous text, and
	// returns the event as it now reads.
	EditEvent(ctx context.Context, e EventEdit) (*ThreadEvent, error)
	// DeleteEvent marks a comment deleted. When it is the thread's opening
	// message and nothing else was said, the thread is deleted with it and
	// threadDeleted is true.
	DeleteEvent(ctx context.Context, threadID, eventID, actorEmail string) (threadDeleted bool, err error)
	// Participants returns the lower-cased addresses of everyone who wrote a
	// message in the thread that is not deleted, or is mentioned in one.
	Participants(ctx context.Context, threadID string) ([]string, error)
}

var _ EventEditor = (*postgresThreadStore)(nil)

// GetEvent returns one event of the thread.
func (s *postgresThreadStore) GetEvent(ctx context.Context, threadID, eventID string) (*ThreadEvent, error) {
	// #nosec G202 -- eventSelectColumns is a fixed package-internal column list, not user input
	row := s.db.QueryRowContext(ctx, `SELECT `+eventSelectColumns+`
		FROM portal_thread_events WHERE thread_id = $1 AND id = $2`, threadID, eventID)
	e, err := scanThreadEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEventNotFound
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// lockEditable locks a message for change and refuses one that cannot be
// changed.
func lockEditable(ctx context.Context, tx *sql.Tx, threadID, eventID string) (body string, metadata []byte, err error) {
	var eventType string
	var bodyNull sql.NullString
	var deletedAt sql.NullTime
	err = tx.QueryRowContext(ctx,
		`SELECT event_type, body, metadata, deleted_at FROM portal_thread_events
		  WHERE thread_id = $1 AND id = $2 FOR UPDATE`, threadID, eventID,
	).Scan(&eventType, &bodyNull, &metadata, &deletedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil, ErrEventNotFound
	case err != nil:
		return "", nil, fmt.Errorf("loading thread event: %w", err)
	case eventType != EventTypeComment:
		return "", nil, ErrEventNotEditable
	case deletedAt.Valid:
		return "", nil, ErrEventDeleted
	}
	return bodyNull.String, metadata, nil
}

// EditEvent replaces a comment's text and keeps the text it had.
func (s *postgresThreadStore) EditEvent(ctx context.Context, e EventEdit) (*ThreadEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf(errBeginTx, err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	oldBody, oldMetadata, err := lockEditable(ctx, tx, e.ThreadID, e.EventID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO portal_thread_event_revisions (event_id, body, metadata, edited_by)
		 VALUES ($1, $2, $3, $4)`,
		e.EventID, oldBody, metadataOrEmpty(oldMetadata), e.EditorEmail); err != nil {
		return nil, fmt.Errorf("keeping the previous text: %w", err)
	}
	// #nosec G202 -- eventSelectColumns is a fixed package-internal column list, not user input
	row := tx.QueryRowContext(ctx,
		`UPDATE portal_thread_events SET body = $3, metadata = $4, edited_at = NOW()
		  WHERE thread_id = $1 AND id = $2
		  RETURNING `+eventSelectColumns,
		e.ThreadID, e.EventID, nullString(e.Body), metadataOrEmpty(e.Metadata))
	edited, err := scanThreadEvent(row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf(errCommitTx, err)
	}
	return &edited, nil
}

// DeleteEvent marks a comment deleted, and deletes the thread with it when it
// was the opening message and nothing else was said.
func (s *postgresThreadStore) DeleteEvent(ctx context.Context, threadID, eventID, actorEmail string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf(errBeginTx, err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	if _, _, err := lockEditable(ctx, tx, threadID, eventID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE portal_thread_events SET deleted_at = NOW(), deleted_by = $3
		  WHERE thread_id = $1 AND id = $2`, threadID, eventID, actorEmail); err != nil {
		return false, fmt.Errorf("deleting thread event: %w", err)
	}
	// The opening message is the thread's earliest event. A thread whose
	// opening message is gone and that holds nothing anyone else said or
	// decided is deleted with it; a status or visibility change is the
	// thread's own bookkeeping, not something said.
	var lone bool
	if err := tx.QueryRowContext(ctx, `
		SELECT (SELECT id FROM portal_thread_events WHERE thread_id = $1
		         ORDER BY created_at ASC, id ASC LIMIT 1) = $2
		   AND NOT EXISTS (SELECT 1 FROM portal_thread_events
		                    WHERE thread_id = $1 AND id <> $2 AND deleted_at IS NULL
		                      AND event_type NOT IN ($3, $4, $5))`,
		threadID, eventID, EventTypeStatusChange, EventTypeResolution, EventTypeVisibilityChange,
	).Scan(&lone); err != nil {
		return false, fmt.Errorf("checking for replies: %w", err)
	}
	if lone {
		if _, err := tx.ExecContext(ctx,
			`UPDATE portal_threads SET deleted_at = NOW(), updated_at = NOW()
			  WHERE id = $1 AND deleted_at IS NULL`, threadID); err != nil {
			return false, fmt.Errorf("deleting the thread: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf(errCommitTx, err)
	}
	return lone, nil
}

// participantsQuery is the authors of a thread's messages that are not
// deleted, and the addresses those messages mention.
const participantsQuery = `
	SELECT LOWER(author_email) FROM portal_thread_events
	 WHERE thread_id = $1 AND deleted_at IS NULL AND author_email <> ''
	 UNION
	SELECT LOWER(m.addr) FROM portal_thread_events e
	 CROSS JOIN LATERAL jsonb_array_elements_text(
	       CASE WHEN jsonb_typeof(e.metadata -> 'mentions') = 'array'
	            THEN e.metadata -> 'mentions' ELSE '[]'::jsonb END) AS m(addr)
	 WHERE e.thread_id = $1 AND e.deleted_at IS NULL`

// Participants returns everyone who wrote in the thread or is mentioned in it.
func (s *postgresThreadStore) Participants(ctx context.Context, threadID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, participantsQuery, threadID)
	if err != nil {
		return nil, fmt.Errorf("listing thread participants: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	out := []string{}
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return nil, fmt.Errorf("scanning thread participant: %w", err)
		}
		if addr = strings.TrimSpace(addr); addr != "" {
			out = append(out, addr)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating thread participants: %w", err)
	}
	return out, nil
}

// MessageNotice is what one message tells the people a thread involves: the
// message, who wrote it, and the people its text mentions (#2512).
type MessageNotice struct {
	Thread     *Thread
	EventID    string
	ActorEmail string
	Body       string
	// Mentioned are the people the message mentions who can open the target.
	Mentioned []string
	// MentionsOnly notifies the mentioned people and nobody else, for an
	// edit that newly names someone (#2514).
	MentionsOnly bool
	// Opened marks the message that opens the thread. A new General thread
	// is sent to the channels the General feedback list names and
	// acknowledged to its author, once (#2603).
	Opened bool
}
