// Package threads holds the portal feedback-thread data layer: the Thread types
// and constants, the ThreadStore interface, its PostgreSQL implementation, and
// the filter/query helpers. It was split out of the portal package (issue #594,
// package-size gate) so the bulk of the thread substrate lives in a cohesive,
// independently-reasoned package; the HTTP handlers that drive it remain in
// portal, which re-exports these symbols under their original names.
package threads

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// psq is the PostgreSQL statement builder with dollar placeholders (a local
// copy of the portal one, so this package has no dependency on portal).
var psq = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// Target-type discriminators for threads. Mirrors the portal share/thread
// polymorphism; exactly one object target is set on a row, none for standalone.
const (
	targetTypeAsset         = "asset"
	targetTypeCollection    = "collection"
	targetTypePrompt        = "prompt"
	targetTypeKnowledgePage = "knowledge_page"
	targetTypeStandalone    = "standalone"
)

// Thread kinds. A kind is the human-facing classification of a feedback thread;
// it is stored on the thread and also determines the event_type of the thread's
// first event (see DeriveFirstEventType).
const (
	ThreadKindComment    = "comment"
	ThreadKindQuestion   = "question"
	ThreadKindCorrection = "correction"
	ThreadKindRating     = "rating"
	ThreadKindApproval   = "approval"
	ThreadKindRejection  = "rejection"
	ThreadKindSuggestion = "suggestion"
)

// Thread statuses.
const (
	ThreadStatusOpen         = "open"
	ThreadStatusAnswered     = "answered"
	ThreadStatusResolved     = "resolved"
	ThreadStatusWontFix      = "wont_fix"
	ThreadStatusAcknowledged = "acknowledged"
)

// Thread event types. comment is one event type among many; status changes,
// resolutions, ratings, approvals, and the Phase 2 knowledge-link events all
// share the same timeline.
const (
	EventTypeComment           = "comment"
	EventTypeStatusChange      = "status_change"
	EventTypeResolution        = "resolution"
	EventTypeRating            = "rating"
	EventTypeApproval          = "approval"
	EventTypeRejection         = "rejection"
	EventTypeValidationRequest = "validation_request"
	EventTypeValidationResult  = "validation_result"
	EventTypeInsightLinked     = "insight_linked"
	EventTypeChangesetLinked   = "changeset_linked"
)

// ValidThreadKind reports whether kind is one of the seven authoring kinds.
func ValidThreadKind(kind string) bool {
	switch kind {
	case ThreadKindComment, ThreadKindQuestion, ThreadKindCorrection,
		ThreadKindRating, ThreadKindApproval, ThreadKindRejection, ThreadKindSuggestion:
		return true
	default:
		return false
	}
}

// ValidThreadStatus reports whether status is a recognized thread status.
func ValidThreadStatus(status string) bool {
	switch status {
	case ThreadStatusOpen, ThreadStatusAnswered, ThreadStatusResolved,
		ThreadStatusWontFix, ThreadStatusAcknowledged:
		return true
	default:
		return false
	}
}

// Thread validation states (validation/signoff lifecycle lands in Phase 3; the
// column and these values exist so the substrate is forward-compatible).
const (
	ValidationStateNone      = "none"
	ValidationStatePending   = "pending"
	ValidationStateValidated = "validated"
	ValidationStateDisputed  = "disputed"
)

// ValidThreadValidationState reports whether s is a recognized validation state.
func ValidThreadValidationState(s string) bool {
	switch s {
	case ValidationStateNone, ValidationStatePending, ValidationStateValidated, ValidationStateDisputed:
		return true
	default:
		return false
	}
}

// DeriveFirstEventType maps a thread kind to the event_type its initial event
// carries: rating/approval/rejection keep their own semantic; everything else
// (comment/question/correction/suggestion) is a plain comment event. Exported
// for the portal createThread handler, which owns thread creation.
func DeriveFirstEventType(kind string) string {
	switch kind {
	case ThreadKindRating:
		return EventTypeRating
	case ThreadKindApproval:
		return EventTypeApproval
	case ThreadKindRejection:
		return EventTypeRejection
	default:
		return EventTypeComment
	}
}

// Thread is a tracked feedback work item with a target, a lifecycle, and a
// typed event timeline. Exactly one of AssetID/CollectionID/PromptID is set for
// asset/collection/prompt target types; all are empty for standalone.
type Thread struct {
	ID                 string          `json:"id" example:"thr_01HK7R8Z"`
	Kind               string          `json:"kind" example:"correction"`
	TargetType         string          `json:"target_type" example:"asset"`
	AssetID            string          `json:"asset_id,omitempty"`
	CollectionID       string          `json:"collection_id,omitempty"`
	PromptID           string          `json:"prompt_id,omitempty"`
	KnowledgePageID    string          `json:"knowledge_page_id,omitempty"`
	Anchor             json.RawMessage `json:"anchor,omitempty" swaggertype:"object"`
	TargetVersion      int             `json:"target_version,omitempty"`
	Title              string          `json:"title,omitempty"`
	AuthorID           string          `json:"author_id"`
	AuthorEmail        string          `json:"author_email" example:"sme@example.com"`
	Status             string          `json:"status" example:"open"`
	RequiresResolution bool            `json:"requires_resolution"`
	ValidationState    string          `json:"validation_state" example:"none"`
	InsightID          string          `json:"insight_id,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	DeletedAt          *time.Time      `json:"deleted_at,omitempty"`
}

// ThreadEvent is one entry in a thread's timeline.
type ThreadEvent struct {
	ID            string          `json:"id" example:"evt_01HK7R8Z"`
	ThreadID      string          `json:"thread_id"`
	EventType     string          `json:"event_type" example:"comment"`
	AuthorID      string          `json:"author_id"`
	AuthorEmail   string          `json:"author_email"`
	Body          string          `json:"body,omitempty"`
	Rating        *int            `json:"rating,omitempty"`
	ParentEventID string          `json:"parent_event_id,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty" swaggertype:"object"`
	CreatedAt     time.Time       `json:"created_at"`
}

// ThreadWithMeta is a thread list row enriched with timeline aggregates so the
// feedback panel can render activity without an N+1 fan-out over events.
type ThreadWithMeta struct {
	Thread
	EventCount    int       `json:"event_count"`
	LastEventAt   time.Time `json:"last_event_at"`
	LastEventType string    `json:"last_event_type,omitempty"`
}

// ThreadFilter selects threads for listing.
type ThreadFilter struct {
	TargetType         string
	AssetID            string
	CollectionID       string
	PromptID           string
	KnowledgePageID    string
	Kind               string
	Status             string
	RequiresResolution *bool
	ValidationState    string
	// AuthorID / AuthorEmail restrict to threads opened by this user (used by the
	// SME "awaiting my validation" worklist). When both are set the match is an
	// OR (id or case-insensitive email), matching how respond-permission resolves
	// the author, so a thread answerable by the caller cannot be missing from
	// their worklist.
	AuthorID    string
	AuthorEmail string
	// TargetAssetIDs / TargetCollectionIDs / TargetPromptIDs restrict to threads
	// on any of the given assets, collections, OR prompts. The practitioner
	// worklist sets assets+collections (artifacts the caller owns or can edit);
	// the feedback activity feed sets all three (every artifact the caller can
	// view). When any is set the match is an OR across the populated target
	// types.
	TargetAssetIDs      []string
	TargetCollectionIDs []string
	TargetPromptIDs     []string
	// IncludeStandalone adds standalone-channel threads to the target-id OR group
	// (used by the agent feedback feed, which spans the caller's artifacts AND the
	// shared general channel). On its own it matches all standalone threads.
	IncludeStandalone bool
	// Unresolved restricts to threads in a non-terminal status (anything other
	// than resolved or wont_fix), i.e. feedback that still needs attention.
	Unresolved bool
	// ExcludeAuthorID / ExcludeAuthorEmail drop threads opened by this user, so a
	// caller's own threads are not surfaced as feedback awaiting their action.
	ExcludeAuthorID    string
	ExcludeAuthorEmail string
	Limit              int
	Offset             int
}

const (
	defaultThreadLimit = 50
	maxThreadLimit     = 200
)

// Shared literals (kept as constants to satisfy the no-magic-string lint).
const (
	errBeginTx      = "begin tx: %w"
	errCommitTx     = "commit: %w"
	errRowsAffected = "checking rows affected: %w"
	eventIDKind     = "evt" // NewThreadID prefix for event ids
)

// EffectiveLimit returns the clamped page size, applying the default when unset.
func (f *ThreadFilter) EffectiveLimit() int {
	if f.Limit <= 0 {
		return defaultThreadLimit
	}
	if f.Limit > maxThreadLimit {
		return maxThreadLimit
	}
	return f.Limit
}

// ThreadUpdate carries optional thread mutations. A nil field is left unchanged.
type ThreadUpdate struct {
	Status             *string
	RequiresResolution *bool
	ValidationState    *string
}

// ThreadStore persists and queries feedback threads and their event timelines.
type ThreadStore interface {
	// CreateThread inserts a thread and its first event atomically and returns
	// the stored thread with server-assigned timestamps.
	CreateThread(ctx context.Context, t Thread, first ThreadEvent) (*Thread, error)
	// ListThreads returns matching threads (with timeline aggregates) plus the
	// total count for pagination.
	ListThreads(ctx context.Context, filter ThreadFilter) ([]ThreadWithMeta, int, error)
	// GetThread returns a single non-deleted thread by id.
	GetThread(ctx context.Context, id string) (*Thread, error)
	// ListEvents returns a thread's events oldest-first.
	ListEvents(ctx context.Context, threadID string) ([]ThreadEvent, error)
	// AppendEvent inserts an event and bumps the thread's updated_at.
	AppendEvent(ctx context.Context, e ThreadEvent) (*ThreadEvent, error)
	// UpdateThread applies the update and, when the status changes, records a
	// status_change/resolution event in the same transaction.
	UpdateThread(ctx context.Context, id string, u ThreadUpdate, actorID, actorEmail string) error
	// SoftDeleteThread marks a thread deleted.
	SoftDeleteThread(ctx context.Context, id string) error
	// LinkInsight links one or more threads to a captured insight: it sets
	// insight_id, appends an insight_linked event, and transitions the thread to
	// resolved, atomically per thread. Threads that are missing or already
	// deleted are skipped. It returns the IDs of the threads that were actually
	// linked, so the caller can detect (and report) thread_ids that matched
	// nothing. This is the bridge from feedback into the knowledge loop
	// (Phase 2 / #602).
	LinkInsight(ctx context.Context, threadIDs []string, insightID, actorID, actorEmail string) ([]string, error)
	// RequestValidation moves a thread to validation_state=pending and records a
	// validation_request event, atomically.
	RequestValidation(ctx context.Context, id, actorID, actorEmail string) error
	// RespondValidation records an SME's validation outcome (validated/disputed),
	// appends a validation_result event, and re-opens the thread when disputed.
	RespondValidation(ctx context.Context, id string, resp ValidationResponse, actorID, actorEmail string) error
	// CountOpenByTargets returns, per target id, the number of open (non-deleted)
	// threads. targetType must be asset/collection/prompt. Callers are
	// responsible for restricting ids to those the requester may see.
	CountOpenByTargets(ctx context.Context, targetType string, ids []string) (map[string]int, error)
	// CountSignoffs returns the number of distinct users who left an approval
	// (signoff) event on any thread targeting the given asset/collection.
	CountSignoffs(ctx context.Context, targetType, targetID string) (int, error)
}

// --- PostgreSQL ThreadStore ---

type postgresThreadStore struct {
	db *sql.DB
}

// NewPostgresThreadStore creates a PostgreSQL-backed thread store.
func NewPostgresThreadStore(db *sql.DB) ThreadStore {
	return &postgresThreadStore{db: db}
}

func (s *postgresThreadStore) CreateThread(ctx context.Context, t Thread, first ThreadEvent) (*Thread, error) { //nolint:revive // interface impl
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf(errBeginTx, err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	insertThread := `
		INSERT INTO portal_threads
		(id, kind, target_type, asset_id, collection_id, prompt_id, knowledge_page_id, anchor, target_version,
		 title, author_id, author_email, status, requires_resolution, validation_state)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING created_at, updated_at
	`
	if err := tx.QueryRowContext(ctx, insertThread,
		t.ID, t.Kind, t.TargetType,
		nullString(t.AssetID), nullString(t.CollectionID), nullString(t.PromptID), nullString(t.KnowledgePageID),
		nullJSON(t.Anchor), nullInt(t.TargetVersion),
		t.Title, t.AuthorID, t.AuthorEmail, statusOrDefault(t.Status), t.RequiresResolution, validationOrDefault(t.ValidationState),
	).Scan(&t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, fmt.Errorf("inserting thread: %w", err)
	}

	if err := insertEventTx(ctx, tx, first); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf(errCommitTx, err)
	}
	return &t, nil
}

func (s *postgresThreadStore) ListThreads(ctx context.Context, filter ThreadFilter) ([]ThreadWithMeta, int, error) { //nolint:revive // interface impl
	total, err := s.countThreads(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	qb := applyThreadFilter(psq.Select(
		aliasedThreadColumns("t"),
		"COALESCE(e.event_count, 0)",
		"COALESCE(e.last_event_at, t.created_at)",
		"COALESCE(e.last_event_type, '')",
	).From("portal_threads t").
		JoinClause(`LEFT JOIN LATERAL (
			SELECT COUNT(*) AS event_count,
			       MAX(created_at) AS last_event_at,
			       (SELECT event_type FROM portal_thread_events
			          WHERE thread_id = t.id ORDER BY created_at DESC LIMIT 1) AS last_event_type
			FROM portal_thread_events WHERE thread_id = t.id
		) e ON TRUE`), filter).
		Where(sq.Eq{"t.deleted_at": nil}).
		OrderBy("t.updated_at DESC").
		Limit(uint64(filter.EffectiveLimit())) // #nosec G115 -- EffectiveLimit is clamped to [defaultThreadLimit, maxThreadLimit], always positive

	if filter.Offset > 0 {
		qb = qb.Offset(uint64(filter.Offset)) // #nosec G115 -- guarded by Offset > 0
	}

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("building thread query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("querying threads: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var out []ThreadWithMeta
	for rows.Next() {
		var tm ThreadWithMeta
		if err := scanThreadWithMeta(rows, &tm); err != nil {
			return nil, 0, err
		}
		out = append(out, tm)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating thread rows: %w", err)
	}
	return out, total, nil
}

func (s *postgresThreadStore) countThreads(ctx context.Context, filter ThreadFilter) (int, error) {
	qb := applyThreadFilter(psq.Select("COUNT(*)").From("portal_threads t"), filter).
		Where(sq.Eq{"t.deleted_at": nil})
	query, args, err := qb.ToSql()
	if err != nil {
		return 0, fmt.Errorf("building count query: %w", err)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("counting threads: %w", err)
	}
	return total, nil
}

func (s *postgresThreadStore) GetThread(ctx context.Context, id string) (*Thread, error) { //nolint:revive // interface impl
	// #nosec G202 -- threadSelectColumns is a fixed package-internal column list, not user input
	query := `SELECT ` + threadSelectColumns + ` FROM portal_threads WHERE id = $1 AND deleted_at IS NULL`
	row := s.db.QueryRowContext(ctx, query, id)
	var t Thread
	if err := scanThread(row, &t); err != nil {
		return nil, fmt.Errorf("querying thread: %w", err)
	}
	return &t, nil
}

// defaultThreadSearchLimit bounds SearchThreads when the caller passes no limit.
const defaultThreadSearchLimit = 10

// SearchThreads returns the owner's feedback threads whose title or any event
// body matches the intent, newest-first. It is lexical (threads carry no
// embedding) and owner-scoped by author email, so it never surfaces another
// user's feedback. This is a separate capability used only by the unified-search
// feedback provider, so it is not part of the ThreadStore interface.
func (s *postgresThreadStore) SearchThreads(ctx context.Context, ownerEmail, intent string, limit int) ([]Thread, error) {
	if ownerEmail == "" || strings.TrimSpace(intent) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultThreadSearchLimit
	}
	pattern := "%" + intent + "%"
	// #nosec G202 -- threadSelectColumns is a fixed package-internal column list, not user input
	query := `SELECT ` + threadSelectColumns + `
		FROM portal_threads
		WHERE deleted_at IS NULL
		  AND lower(author_email) = lower($1)
		  AND (title ILIKE $2 OR EXISTS (
		        SELECT 1 FROM portal_thread_events e
		        WHERE e.thread_id = portal_threads.id AND e.body ILIKE $2))
		ORDER BY updated_at DESC
		LIMIT $3`
	rows, err := s.db.QueryContext(ctx, query, ownerEmail, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("searching threads: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var out []Thread
	for rows.Next() {
		var t Thread
		if scanErr := scanThread(rows, &t); scanErr != nil {
			return nil, scanErr
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating thread rows: %w", err)
	}
	return out, nil
}

func (s *postgresThreadStore) ListEvents(ctx context.Context, threadID string) ([]ThreadEvent, error) { //nolint:revive // interface impl
	query := `
		SELECT id, thread_id, event_type, author_id, author_email, body, rating, parent_event_id, metadata, created_at
		FROM portal_thread_events WHERE thread_id = $1 ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, threadID)
	if err != nil {
		return nil, fmt.Errorf("querying thread events: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var events []ThreadEvent
	for rows.Next() {
		e, scanErr := scanThreadEvent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating event rows: %w", err)
	}
	return events, nil
}

func (s *postgresThreadStore) AppendEvent(ctx context.Context, e ThreadEvent) (*ThreadEvent, error) { //nolint:revive // interface impl
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf(errBeginTx, err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	if err := insertEventTx(ctx, tx, e); err != nil {
		return nil, err
	}
	if err := touchThreadTx(ctx, tx, e.ThreadID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf(errCommitTx, err)
	}
	// created_at was assigned by the DB default; re-read is unnecessary for the
	// caller, which already holds the event it submitted.
	return &e, nil
}

func (s *postgresThreadStore) UpdateThread(ctx context.Context, id string, u ThreadUpdate, actorID, actorEmail string) error { //nolint:revive // interface impl
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf(errBeginTx, err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	var oldStatus string
	if err := tx.QueryRowContext(ctx,
		`SELECT status FROM portal_threads WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, id,
	).Scan(&oldStatus); err != nil {
		return fmt.Errorf("loading thread for update: %w", err)
	}

	query, args, err := psq.Update("portal_threads").SetMap(threadUpdateSetMap(u)).Where(sq.Eq{"id": id}).ToSql()
	if err != nil {
		return fmt.Errorf("building thread update: %w", err)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("updating thread: %w", err)
	}

	// Record a timeline event when the status actually changed, so the detail
	// view never shows a status with no corresponding event.
	if u.Status != nil && *u.Status != oldStatus {
		evt := ThreadEvent{
			ID:          NewThreadID(eventIDKind),
			ThreadID:    id,
			EventType:   statusChangeEventType(*u.Status),
			AuthorID:    actorID,
			AuthorEmail: actorEmail,
			Metadata:    statusChangeMetadata(oldStatus, *u.Status),
		}
		if err := insertEventTx(ctx, tx, evt); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf(errCommitTx, err)
	}
	return nil
}

func (s *postgresThreadStore) SoftDeleteThread(ctx context.Context, id string) error { //nolint:revive // interface impl
	res, err := s.db.ExecContext(ctx,
		`UPDATE portal_threads SET deleted_at = $1, updated_at = $1 WHERE id = $2 AND deleted_at IS NULL`,
		time.Now(), id)
	if err != nil {
		return fmt.Errorf("soft-deleting thread: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf(errRowsAffected, err)
	}
	if affected == 0 {
		return fmt.Errorf("thread not found or already deleted: %s", id)
	}
	return nil
}

func (s *postgresThreadStore) LinkInsight(ctx context.Context, threadIDs []string, insightID, actorID, actorEmail string) ([]string, error) { //nolint:revive // interface impl
	if len(threadIDs) == 0 || insightID == "" {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf(errBeginTx, err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	linked := make([]string, 0, len(threadIDs))
	seen := make(map[string]struct{}, len(threadIDs))
	for _, id := range threadIDs {
		if _, dup := seen[id]; dup {
			continue // de-dupe: a repeated id must not double-link or double-count
		}
		seen[id] = struct{}{}
		ok, err := linkInsightToThreadTx(ctx, tx, id, insightID, threadActor{actorID, actorEmail})
		if err != nil {
			return nil, err
		}
		if ok {
			linked = append(linked, id)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf(errCommitTx, err)
	}
	return linked, nil
}

// threadActor identifies who performed a thread mutation.
type threadActor struct {
	id    string
	email string
}

// linkInsightToThreadTx links one thread to the insight inside tx, returning
// false (and no error) when the thread is missing or already deleted.
func linkInsightToThreadTx(ctx context.Context, tx *sql.Tx, id, insightID string, actor threadActor) (bool, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE portal_threads SET insight_id = $1, status = $2, updated_at = NOW()
		 WHERE id = $3 AND deleted_at IS NULL`,
		insightID, ThreadStatusResolved, id)
	if err != nil {
		return false, fmt.Errorf("linking insight to thread %s: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf(errRowsAffected, err)
	}
	if affected == 0 {
		return false, nil // missing or deleted thread: skip
	}
	evt := ThreadEvent{
		ID:          NewThreadID(eventIDKind),
		ThreadID:    id,
		EventType:   EventTypeInsightLinked,
		AuthorID:    actor.id,
		AuthorEmail: actor.email,
		Metadata:    insightLinkedMetadata(insightID),
	}
	if err := insertEventTx(ctx, tx, evt); err != nil {
		return false, err
	}
	return true, nil
}

func (s *postgresThreadStore) RequestValidation(ctx context.Context, id, actorID, actorEmail string) error { //nolint:revive // interface impl
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf(errBeginTx, err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	res, err := tx.ExecContext(ctx,
		`UPDATE portal_threads SET validation_state = $1, updated_at = NOW()
		 WHERE id = $2 AND deleted_at IS NULL`,
		ValidationStatePending, id)
	if err != nil {
		return fmt.Errorf("requesting validation: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf(errRowsAffected, err)
	}
	if affected == 0 {
		return fmt.Errorf("thread not found or already deleted: %s", id)
	}

	evt := ThreadEvent{
		ID:          NewThreadID(eventIDKind),
		ThreadID:    id,
		EventType:   EventTypeValidationRequest,
		AuthorID:    actorID,
		AuthorEmail: actorEmail,
	}
	if err := insertEventTx(ctx, tx, evt); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf(errCommitTx, err)
	}
	return nil
}

// ValidationResponse is an SME's answer to a validation request (Phase 3 / #603).
type ValidationResponse struct {
	Result string // ValidationStateValidated or ValidationStateDisputed
	Reason string // optional; recorded on the validation_result event
}

// RespondValidation records an SME's validation outcome on a thread: it sets
// validation_state to validated/disputed, appends a validation_result event
// (carrying the result and reason), and — when disputed — re-opens the thread so
// it returns to the practitioner's worklist. All in one transaction.
func (s *postgresThreadStore) RespondValidation(ctx context.Context, id string, resp ValidationResponse, actorID, actorEmail string) error { //nolint:revive // interface impl
	// Defense in depth: the column has no DB CHECK, so reject any result other
	// than the two terminal outcomes before it reaches validation_state.
	if resp.Result != ValidationStateValidated && resp.Result != ValidationStateDisputed {
		return fmt.Errorf("invalid validation result: %q", resp.Result)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf(errBeginTx, err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	// The thread must be awaiting validation: a response only answers a prior
	// request, so the state machine is request(pending) -> respond. The
	// validation_state = 'pending' precondition enforces it atomically (so an
	// author cannot "dispute" — and thereby re-open — a thread no one asked to
	// validate). Disputing re-opens the thread (#603); validating leaves status.
	query := `UPDATE portal_threads SET validation_state = $1, updated_at = NOW()
	          WHERE id = $2 AND deleted_at IS NULL AND validation_state = $3`
	args := []any{resp.Result, id, ValidationStatePending}
	if resp.Result == ValidationStateDisputed {
		query = `UPDATE portal_threads SET validation_state = $1, status = $2, updated_at = NOW()
		         WHERE id = $3 AND deleted_at IS NULL AND validation_state = $4`
		args = []any{resp.Result, ThreadStatusOpen, id, ValidationStatePending}
	}
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("responding to validation: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf(errRowsAffected, err)
	}
	if affected == 0 {
		return fmt.Errorf("thread not found or not awaiting validation: %s", id)
	}

	evt := ThreadEvent{
		ID:          NewThreadID(eventIDKind),
		ThreadID:    id,
		EventType:   EventTypeValidationResult,
		AuthorID:    actorID,
		AuthorEmail: actorEmail,
		Metadata:    validationResultMetadata(resp),
	}
	if err := insertEventTx(ctx, tx, evt); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf(errCommitTx, err)
	}
	return nil
}

// validationResultMetadata builds the JSON metadata for a validation_result event.
func validationResultMetadata(resp ValidationResponse) json.RawMessage {
	m := map[string]string{"result": resp.Result}
	if resp.Reason != "" {
		m["reason"] = resp.Reason
	}
	b, _ := json.Marshal(m) //nolint:errcheck // map of strings cannot fail
	return b
}

// insightLinkedMetadata builds the JSON metadata for an insight_linked event.
func insightLinkedMetadata(insightID string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"insight_id": insightID}) //nolint:errcheck // map of strings cannot fail
	return b
}

func (s *postgresThreadStore) CountOpenByTargets(ctx context.Context, targetType string, ids []string) (map[string]int, error) { //nolint:revive // interface impl
	counts := make(map[string]int)
	if len(ids) == 0 {
		return counts, nil
	}
	column, err := targetColumn(targetType)
	if err != nil {
		return nil, err
	}

	// #nosec G201 -- column is from targetColumn(), a fixed asset_id/collection_id/prompt_id allowlist, never user input
	query := fmt.Sprintf(`
		SELECT %s, COUNT(*) FROM portal_threads
		WHERE %s = ANY($1) AND status = $2 AND deleted_at IS NULL
		GROUP BY %s
	`, column, column, column)

	rows, err := s.db.QueryContext(ctx, query, pq.Array(ids), ThreadStatusOpen)
	if err != nil {
		return nil, fmt.Errorf("counting open threads: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("scanning count row: %w", err)
		}
		counts[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating count rows: %w", err)
	}
	return counts, nil
}

// CountSignoffs returns the number of distinct users who left an approval
// (signoff) event on any thread targeting the given asset/collection (Phase 3 /
// #603). This is the "N" in "signed off by N of M stakeholders".
func (s *postgresThreadStore) CountSignoffs(ctx context.Context, targetType, targetID string) (int, error) { //nolint:revive // interface impl
	column, err := targetColumn(targetType)
	if err != nil {
		return 0, err
	}
	// #nosec G201 -- column is from targetColumn(), a fixed asset_id/collection_id/prompt_id allowlist, never user input
	query := fmt.Sprintf(`
		SELECT COUNT(DISTINCT e.author_id)
		FROM portal_thread_events e
		JOIN portal_threads t ON t.id = e.thread_id
		WHERE t.%s = $1 AND t.deleted_at IS NULL AND e.event_type = $2
	`, column)
	var n int
	if err := s.db.QueryRowContext(ctx, query, targetID, EventTypeApproval).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting signoffs: %w", err)
	}
	return n, nil
}

// --- helpers ---

func targetColumn(targetType string) (string, error) {
	switch targetType {
	case targetTypeAsset:
		return "asset_id", nil
	case targetTypeCollection:
		return "collection_id", nil
	case targetTypePrompt:
		return "prompt_id", nil
	case targetTypeKnowledgePage:
		return "knowledge_page_id", nil
	default:
		return "", fmt.Errorf("unsupported target type for counts: %q", targetType)
	}
}

// applyThreadTargetEq appends the single-target equality conditions (target type
// plus the one object id that is set), factored out of applyThreadFilter to keep
// its complexity bounded.
func applyThreadTargetEq(qb sq.SelectBuilder, f ThreadFilter) sq.SelectBuilder {
	// Ordered so the generated SQL is deterministic.
	cols := []struct{ col, val string }{
		{"t.target_type", f.TargetType},
		{"t.asset_id", f.AssetID},
		{"t.collection_id", f.CollectionID},
		{"t.prompt_id", f.PromptID},
		{"t.knowledge_page_id", f.KnowledgePageID},
	}
	for _, c := range cols {
		if c.val != "" {
			qb = qb.Where(sq.Eq{c.col: c.val})
		}
	}
	return qb
}

// applyThreadFilter appends the equality conditions common to list and count.
func applyThreadFilter(qb sq.SelectBuilder, f ThreadFilter) sq.SelectBuilder {
	qb = applyThreadTargetEq(qb, f)
	if f.Kind != "" {
		qb = qb.Where(sq.Eq{"t.kind": f.Kind})
	}
	if f.Status != "" {
		qb = qb.Where(sq.Eq{"t.status": f.Status})
	}
	if f.RequiresResolution != nil {
		qb = qb.Where(sq.Eq{"t.requires_resolution": *f.RequiresResolution})
	}
	if f.ValidationState != "" {
		qb = qb.Where(sq.Eq{"t.validation_state": f.ValidationState})
	}
	if f.Unresolved {
		qb = qb.Where(sq.NotEq{"t.status": []string{ThreadStatusResolved, ThreadStatusWontFix}})
	}
	qb = applyThreadAuthorFilter(qb, f)
	qb = applyThreadAuthorExcludeFilter(qb, f)
	if or := threadTargetIDsCond(f); or != nil {
		qb = qb.Where(or)
	}
	return qb
}

// applyThreadAuthorExcludeFilter drops threads opened by the excluded user (by
// id or case-insensitive email), so the agent feed never lists the caller's own
// threads as feedback awaiting their action.
func applyThreadAuthorExcludeFilter(qb sq.SelectBuilder, f ThreadFilter) sq.SelectBuilder {
	if f.ExcludeAuthorID != "" {
		qb = qb.Where(sq.NotEq{"t.author_id": f.ExcludeAuthorID})
	}
	if f.ExcludeAuthorEmail != "" {
		qb = qb.Where(sq.Expr("LOWER(t.author_email) <> LOWER(?)", f.ExcludeAuthorEmail))
	}
	return qb
}

// applyThreadAuthorFilter restricts to threads opened by the caller. When both
// id and email are set the match is an OR (id or case-insensitive email),
// matching how respond-permission resolves the author.
func applyThreadAuthorFilter(qb sq.SelectBuilder, f ThreadFilter) sq.SelectBuilder {
	switch {
	case f.AuthorID != "" && f.AuthorEmail != "":
		return qb.Where(sq.Or{
			sq.Eq{"t.author_id": f.AuthorID},
			sq.Expr("LOWER(t.author_email) = LOWER(?)", f.AuthorEmail),
		})
	case f.AuthorID != "":
		return qb.Where(sq.Eq{"t.author_id": f.AuthorID})
	case f.AuthorEmail != "":
		return qb.Where(sq.Expr("LOWER(t.author_email) = LOWER(?)", f.AuthorEmail))
	default:
		return qb
	}
}

// threadTargetIDsCond builds the OR condition matching threads on any of the
// given asset, collection, or prompt ids (used by the worklist and the activity
// feed, which span many targets). Returns nil when no id set is populated.
func threadTargetIDsCond(f ThreadFilter) sq.Sqlizer {
	if len(f.TargetAssetIDs) == 0 && len(f.TargetCollectionIDs) == 0 &&
		len(f.TargetPromptIDs) == 0 && !f.IncludeStandalone {
		return nil
	}
	or := sq.Or{}
	if len(f.TargetAssetIDs) > 0 {
		or = append(or, sq.Eq{"t.asset_id": f.TargetAssetIDs})
	}
	if len(f.TargetCollectionIDs) > 0 {
		or = append(or, sq.Eq{"t.collection_id": f.TargetCollectionIDs})
	}
	if len(f.TargetPromptIDs) > 0 {
		or = append(or, sq.Eq{"t.prompt_id": f.TargetPromptIDs})
	}
	if f.IncludeStandalone {
		or = append(or, sq.Eq{"t.target_type": targetTypeStandalone})
	}
	return or
}

// threadUpdateSetMap builds the squirrel SET map for an UpdateThread, always
// bumping updated_at and including only the fields the caller set.
func threadUpdateSetMap(u ThreadUpdate) map[string]any {
	setMap := map[string]any{"updated_at": time.Now()}
	if u.Status != nil {
		setMap["status"] = *u.Status
	}
	if u.RequiresResolution != nil {
		setMap["requires_resolution"] = *u.RequiresResolution
	}
	if u.ValidationState != nil {
		setMap["validation_state"] = *u.ValidationState
	}
	return setMap
}

// insertEventTx inserts one event row inside an existing transaction.
func insertEventTx(ctx context.Context, tx *sql.Tx, e ThreadEvent) error {
	query := `
		INSERT INTO portal_thread_events
		(id, thread_id, event_type, author_id, author_email, body, rating, parent_event_id, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := tx.ExecContext(ctx, query,
		e.ID, e.ThreadID, e.EventType, e.AuthorID, e.AuthorEmail,
		nullString(e.Body), nullIntPtr(e.Rating), nullString(e.ParentEventID), metadataOrEmpty(e.Metadata),
	)
	if err != nil {
		return fmt.Errorf("inserting thread event: %w", err)
	}
	return nil
}

// touchThreadTx bumps a thread's updated_at within a transaction.
func touchThreadTx(ctx context.Context, tx *sql.Tx, threadID string) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE portal_threads SET updated_at = $1 WHERE id = $2`, time.Now(), threadID); err != nil {
		return fmt.Errorf("touching thread: %w", err)
	}
	return nil
}

func scanThread(row interface{ Scan(...any) error }, t *Thread) error {
	var assetID, collectionID, promptID, knowledgePageID, insightID sql.NullString
	var anchor []byte
	var targetVersion sql.NullInt64
	var deletedAt sql.NullTime
	if err := row.Scan(
		&t.ID, &t.Kind, &t.TargetType, &assetID, &collectionID, &promptID, &knowledgePageID, &anchor, &targetVersion,
		&t.Title, &t.AuthorID, &t.AuthorEmail, &t.Status, &t.RequiresResolution, &t.ValidationState, &insightID,
		&t.CreatedAt, &t.UpdatedAt, &deletedAt,
	); err != nil {
		return fmt.Errorf("scanning thread row: %w", err)
	}
	t.AssetID = assetID.String
	t.CollectionID = collectionID.String
	t.PromptID = promptID.String
	t.KnowledgePageID = knowledgePageID.String
	t.InsightID = insightID.String
	if len(anchor) > 0 {
		t.Anchor = anchor
	}
	if targetVersion.Valid {
		t.TargetVersion = int(targetVersion.Int64)
	}
	if deletedAt.Valid {
		t.DeletedAt = &deletedAt.Time
	}
	return nil
}

func scanThreadWithMeta(rows *sql.Rows, tm *ThreadWithMeta) error {
	var assetID, collectionID, promptID, knowledgePageID, insightID sql.NullString
	var anchor []byte
	var targetVersion sql.NullInt64
	var deletedAt sql.NullTime
	if err := rows.Scan(
		&tm.ID, &tm.Kind, &tm.TargetType, &assetID, &collectionID, &promptID, &knowledgePageID, &anchor, &targetVersion,
		&tm.Title, &tm.AuthorID, &tm.AuthorEmail, &tm.Status, &tm.RequiresResolution, &tm.ValidationState, &insightID,
		&tm.CreatedAt, &tm.UpdatedAt, &deletedAt,
		&tm.EventCount, &tm.LastEventAt, &tm.LastEventType,
	); err != nil {
		return fmt.Errorf("scanning thread row: %w", err)
	}
	tm.AssetID = assetID.String
	tm.CollectionID = collectionID.String
	tm.PromptID = promptID.String
	tm.KnowledgePageID = knowledgePageID.String
	tm.InsightID = insightID.String
	if len(anchor) > 0 {
		tm.Anchor = anchor
	}
	if targetVersion.Valid {
		tm.TargetVersion = int(targetVersion.Int64)
	}
	if deletedAt.Valid {
		tm.DeletedAt = &deletedAt.Time
	}
	return nil
}

func scanThreadEvent(rows *sql.Rows) (ThreadEvent, error) {
	var e ThreadEvent
	var body, parentID sql.NullString
	var rating sql.NullInt64
	var metadata []byte
	if err := rows.Scan(
		&e.ID, &e.ThreadID, &e.EventType, &e.AuthorID, &e.AuthorEmail,
		&body, &rating, &parentID, &metadata, &e.CreatedAt,
	); err != nil {
		return e, fmt.Errorf("scanning thread event row: %w", err)
	}
	e.Body = body.String
	e.ParentEventID = parentID.String
	if rating.Valid {
		r := int(rating.Int64)
		e.Rating = &r
	}
	if len(metadata) > 0 {
		e.Metadata = metadata
	}
	return e, nil
}

// statusChangeEventType returns resolution for terminal statuses, else status_change.
func statusChangeEventType(newStatus string) string {
	if newStatus == ThreadStatusResolved || newStatus == ThreadStatusWontFix {
		return EventTypeResolution
	}
	return EventTypeStatusChange
}

func statusChangeMetadata(oldStatus, newStatus string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"old_status": oldStatus, "new_status": newStatus}) //nolint:errcheck // map of strings cannot fail
	return b
}

// aliasedThreadColumns returns the canonical thread column list with each name
// prefixed by the table alias, as a single comma-joined expression squirrel can
// take as one Select column.
func aliasedThreadColumns(alias string) string {
	names := make([]string, len(threadColumnNames))
	for i, name := range threadColumnNames {
		names[i] = alias + "." + name
	}
	return strings.Join(names, ", ")
}

// threadColumnNames is the canonical portal_threads column list, in the order
// scanThread/scanThreadWithMeta expect. threadSelectColumns is the same list as
// a comma-joined SELECT expression; aliasedThreadColumns prefixes a table alias.
var threadColumnNames = []string{
	"id", "kind", "target_type", "asset_id", "collection_id", "prompt_id", "knowledge_page_id", "anchor", "target_version",
	"title", "author_id", "author_email", "status", "requires_resolution", "validation_state", "insight_id",
	"created_at", "updated_at", "deleted_at",
}

var threadSelectColumns = strings.Join(threadColumnNames, ", ")

// NewThreadID returns a prefixed unique id (e.g. "thr_<uuid>", "evt_<uuid>").
func NewThreadID(prefix string) string {
	return prefix + "_" + uuid.New().String()
}

// NewThreadEventID returns a unique id for a thread event. Exported so callers
// outside the package (the portal toolkit) can mint event ids for AppendEvent.
func NewThreadEventID() string {
	return NewThreadID(eventIDKind)
}

// DefaultThreadLimit and MaxThreadLimit are exported for the portal list
// handlers, which parse and clamp the page size before calling the store.
const (
	DefaultThreadLimit = defaultThreadLimit
	MaxThreadLimit     = maxThreadLimit
)

var _ ThreadStore = (*postgresThreadStore)(nil)

func statusOrDefault(status string) string {
	if status == "" {
		return ThreadStatusOpen
	}
	return status
}

func validationOrDefault(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullInt(n int) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(n), Valid: n != 0}
}

func nullIntPtr(n *int) sql.NullInt64 {
	if n == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*n), Valid: true}
}

func nullJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

func metadataOrEmpty(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}
