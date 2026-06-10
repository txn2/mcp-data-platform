package portal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
)

// Thread kinds. A kind is the human-facing classification of a feedback thread;
// it is stored on the thread and also determines the event_type of the thread's
// first event (see deriveFirstEventType).
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

// deriveFirstEventType maps a thread kind to the event_type its initial event
// carries: rating/approval/rejection keep their own semantic; everything else
// (comment/question/correction/suggestion) is a plain comment event.
func deriveFirstEventType(kind string) string {
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
	TargetType   string
	AssetID      string
	CollectionID string
	PromptID     string
	Kind         string
	Status       string
	Limit        int
	Offset       int
}

const (
	defaultThreadLimit = 50
	maxThreadLimit     = 200
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
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	insertThread := `
		INSERT INTO portal_threads
		(id, kind, target_type, asset_id, collection_id, prompt_id, anchor, target_version,
		 title, author_id, author_email, status, requires_resolution, validation_state)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING created_at, updated_at
	`
	if err := tx.QueryRowContext(ctx, insertThread,
		t.ID, t.Kind, t.TargetType,
		nullString(t.AssetID), nullString(t.CollectionID), nullString(t.PromptID),
		nullJSON(t.Anchor), nullInt(t.TargetVersion),
		t.Title, t.AuthorID, t.AuthorEmail, statusOrDefault(t.Status), t.RequiresResolution, validationOrDefault(t.ValidationState),
	).Scan(&t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, fmt.Errorf("inserting thread: %w", err)
	}

	if err := insertEventTx(ctx, tx, first); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
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
		Limit(uint64(filter.EffectiveLimit())) //nolint:gosec // bounded by maxThreadLimit

	if filter.Offset > 0 {
		qb = qb.Offset(uint64(filter.Offset)) //nolint:gosec // non-negative
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
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	if err := insertEventTx(ctx, tx, e); err != nil {
		return nil, err
	}
	if err := touchThreadTx(ctx, tx, e.ThreadID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	// created_at was assigned by the DB default; re-read is unnecessary for the
	// caller, which already holds the event it submitted.
	return &e, nil
}

func (s *postgresThreadStore) UpdateThread(ctx context.Context, id string, u ThreadUpdate, actorID, actorEmail string) error { //nolint:revive // interface impl
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
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
			ID:          newThreadID("evt"),
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
		return fmt.Errorf("commit: %w", err)
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
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("thread not found or already deleted: %s", id)
	}
	return nil
}

// --- helpers ---

// applyThreadFilter appends the equality conditions common to list and count.
func applyThreadFilter(qb sq.SelectBuilder, f ThreadFilter) sq.SelectBuilder {
	if f.TargetType != "" {
		qb = qb.Where(sq.Eq{"t.target_type": f.TargetType})
	}
	if f.AssetID != "" {
		qb = qb.Where(sq.Eq{"t.asset_id": f.AssetID})
	}
	if f.CollectionID != "" {
		qb = qb.Where(sq.Eq{"t.collection_id": f.CollectionID})
	}
	if f.PromptID != "" {
		qb = qb.Where(sq.Eq{"t.prompt_id": f.PromptID})
	}
	if f.Kind != "" {
		qb = qb.Where(sq.Eq{"t.kind": f.Kind})
	}
	if f.Status != "" {
		qb = qb.Where(sq.Eq{"t.status": f.Status})
	}
	return qb
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
	var assetID, collectionID, promptID, insightID sql.NullString
	var anchor []byte
	var targetVersion sql.NullInt64
	var deletedAt sql.NullTime
	if err := row.Scan(
		&t.ID, &t.Kind, &t.TargetType, &assetID, &collectionID, &promptID, &anchor, &targetVersion,
		&t.Title, &t.AuthorID, &t.AuthorEmail, &t.Status, &t.RequiresResolution, &t.ValidationState, &insightID,
		&t.CreatedAt, &t.UpdatedAt, &deletedAt,
	); err != nil {
		return fmt.Errorf("scanning thread row: %w", err)
	}
	t.AssetID = assetID.String
	t.CollectionID = collectionID.String
	t.PromptID = promptID.String
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
	var assetID, collectionID, promptID, insightID sql.NullString
	var anchor []byte
	var targetVersion sql.NullInt64
	var deletedAt sql.NullTime
	if err := rows.Scan(
		&tm.ID, &tm.Kind, &tm.TargetType, &assetID, &collectionID, &promptID, &anchor, &targetVersion,
		&tm.Title, &tm.AuthorID, &tm.AuthorEmail, &tm.Status, &tm.RequiresResolution, &tm.ValidationState, &insightID,
		&tm.CreatedAt, &tm.UpdatedAt, &deletedAt,
		&tm.EventCount, &tm.LastEventAt, &tm.LastEventType,
	); err != nil {
		return fmt.Errorf("scanning thread row: %w", err)
	}
	tm.AssetID = assetID.String
	tm.CollectionID = collectionID.String
	tm.PromptID = promptID.String
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
	"id", "kind", "target_type", "asset_id", "collection_id", "prompt_id", "anchor", "target_version",
	"title", "author_id", "author_email", "status", "requires_resolution", "validation_state", "insight_id",
	"created_at", "updated_at", "deleted_at",
}

var threadSelectColumns = strings.Join(threadColumnNames, ", ")

// newThreadID returns a prefixed unique id (e.g. "thr_<uuid>", "evt_<uuid>").
func newThreadID(prefix string) string {
	return prefix + "_" + uuid.New().String()
}

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
