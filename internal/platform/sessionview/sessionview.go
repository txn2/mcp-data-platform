// Package sessionview is the read model for a platform session: the thing an
// operator lists, opens, and reads after the fact.
//
// A session is derived from the audit log rather than stored. The session rows
// the platform keeps (pkg/session) are working state with a TTL — Cleanup
// deletes them on expiry — so they cannot be the record of what a session did.
// The audit rows can: every tool call already carries the session id that made
// it, and audit retention is the deployment's stated history window. What the
// live session row still holds while it exists (the persona the handle was
// minted under) is read as an overlay, never as the source of the session's
// existence.
//
// Nothing here writes. The store owns aggregate reads over audit_logs plus the
// two tables that record what a session left behind: the assets it saved and
// the insights it captured.
package sessionview

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/session"
)

// Kind classifies a session id by the origin that minted it. It is read off
// the id's prefix, which is the only classification the platform has: the ids
// of isolated runs are never persisted as session rows.
type Kind string

const (
	// KindAgent is a platform_info-minted handle threaded by an agent
	// across calls (session.HandlePrefix).
	KindAgent Kind = "agent"

	// KindPortal is one portal-initiated tool run, isolated to a single
	// HTTP request (session.PortalSessionPrefix).
	KindPortal Kind = "portal"

	// KindScript is one managed-script run (session.ScriptSessionPrefix).
	KindScript Kind = "script"

	// KindTransport is everything else: a transport-derived session id, or
	// a row written before explicit handles existed. Bare hex, no prefix.
	KindTransport Kind = "transport"
)

// KindOf classifies a session id. An id that carries none of the platform's
// prefixes is KindTransport rather than an error: the audit log holds rows from
// before handles existed, and they are still sessions.
func KindOf(id string) Kind {
	switch {
	case strings.HasPrefix(id, session.HandlePrefix):
		return KindAgent
	case strings.HasPrefix(id, session.PortalSessionPrefix):
		return KindPortal
	case strings.HasPrefix(id, session.ScriptSessionPrefix):
		return KindScript
	default:
		return KindTransport
	}
}

// prefixForKind returns the id prefix a kind is recognized by, and whether the
// kind has one at all. KindTransport is defined by carrying none of them, so it
// reports false and is matched by exclusion.
func prefixForKind(k Kind) (string, bool) {
	switch k {
	case KindAgent:
		return session.HandlePrefix, true
	case KindPortal:
		return session.PortalSessionPrefix, true
	case KindScript:
		return session.ScriptSessionPrefix, true
	case KindTransport:
		return "", false
	default:
		return "", false
	}
}

// Summary is one session as the list shows it: who ran it, over what window,
// how much it did, and what it left behind.
type Summary struct {
	SessionID string `json:"session_id" example:"dps_9f2c1a4b8e7d6c5a4b3e2d1c0f9e8a7b"`
	// Kind is the id's origin (agent handle, portal run, script run,
	// transport). Derived from the id, so it is never absent.
	Kind Kind `json:"kind" example:"agent"`
	// UserID and UserEmail come from the session's first event. A session
	// belongs to one caller; the audit rows repeat that caller per row.
	UserID    string `json:"user_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	UserEmail string `json:"user_email,omitempty" example:"marcus.johnson@example.com"`
	// Persona is the live session row's minted persona when the row still
	// exists, and the first event's persona once it has expired.
	Persona      string    `json:"persona,omitempty" example:"data-engineer"`
	StartedAt    time.Time `json:"started_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	CallCount    int       `json:"call_count" example:"5"`
	FailureCount int       `json:"failure_count" example:"1"`
	// Tools and Connections are the distinct names the session touched,
	// sorted. Empty slices, never null, so the UI need not model both.
	Tools        []string `json:"tools"`
	Connections  []string `json:"connections"`
	AssetCount   int      `json:"asset_count" example:"1"`
	InsightCount int      `json:"insight_count" example:"0"`
}

// Detail is one session opened: its summary, what it produced, and the ordered
// record of its calls.
type Detail struct {
	Summary
	Assets   []AssetRef      `json:"assets"`
	Insights []InsightRef    `json:"insights"`
	Timeline []TimelineEntry `json:"timeline"`
	// TimelineTotal is the session's full call count, which exceeds
	// len(Timeline) when the timeline is paged.
	TimelineTotal int `json:"timeline_total" example:"5"`
}

// TimelineEntry is one call the session made, in the order it was made.
type TimelineEntry struct {
	// EventID is the audit event's id, which the admin events surface
	// addresses a single row by.
	EventID   string    `json:"event_id" example:"evt_a1b2c3d4e5f6"`
	Timestamp time.Time `json:"timestamp"`
	ToolName  string    `json:"tool_name" example:"trino_query"`
	// Purpose is the reason the agent stated for this call (#1317). Empty
	// on a call the platform does not gate and on rows older than it.
	Purpose      string `json:"purpose,omitempty" example:"Sizing Q3 revenue by region for the board deck."`
	ToolkitKind  string `json:"toolkit_kind,omitempty" example:"trino"`
	Connection   string `json:"connection,omitempty" example:"acme-warehouse"`
	Success      bool   `json:"success" example:"true"`
	ErrorMessage string `json:"error_message,omitempty"`
	DurationMS   int64  `json:"duration_ms" example:"143"`
}

// AssetRef is an asset the session saved, identified well enough to link to.
type AssetRef struct {
	ID          string    `json:"id" example:"ast_7c1e"`
	Name        string    `json:"name" example:"Q3 revenue by region"`
	ContentType string    `json:"content_type" example:"text/csv"`
	CreatedAt   time.Time `json:"created_at"`
}

// InsightRef is an insight the session captured.
type InsightRef struct {
	ID        string    `json:"id" example:"ins_3a9f"`
	Category  string    `json:"category" example:"data_quality"`
	Text      string    `json:"text" example:"orders.amount is null for canceled rows."`
	Status    string    `json:"status" example:"pending"`
	CreatedAt time.Time `json:"created_at"`
}

// Filter selects sessions for the list.
type Filter struct {
	// SessionID selects exactly one session. Set by Get; the list leaves
	// it empty.
	SessionID string
	UserID    string
	// Kind restricts to one id origin. The zero value matches every kind.
	Kind Kind
	// StartTime and EndTime bound the session's events, so a session is
	// listed when any of its calls falls in the window.
	StartTime *time.Time
	EndTime   *time.Time
	// HasAssets keeps only sessions that saved at least one asset.
	HasAssets bool
	// HasFailures keeps only sessions with at least one failed call.
	HasFailures bool
	Limit       int
	Offset      int
}

// Scope names one session and, optionally, the only caller allowed to read it.
//
// A user-facing surface sets UserID; the operator surface leaves it empty and
// is unrestricted. The restriction is a predicate on the audit rows themselves
// rather than a comparison the handler makes after reading, so a session that
// belongs to someone else groups to no rows at all and is indistinguishable
// from one that never existed.
type Scope struct {
	SessionID string
	// UserID restricts the read to the calls that caller made. Empty is
	// unrestricted.
	UserID string
	// Limit and Offset page the timeline. They do not affect Get.
	Limit  int
	Offset int
}

// Store reads sessions. Implemented over PostgreSQL by PostgresStore; a
// deployment with no database has no audit history and so registers no
// session surface at all.
type Store interface {
	// List returns sessions matching the filter, most recently active
	// first.
	List(ctx context.Context, filter Filter) ([]Summary, error)
	// Count returns how many sessions match the filter, ignoring its
	// limit and offset.
	Count(ctx context.Context, filter Filter) (int, error)
	// Get returns one session, or ErrNotFound when the audit log holds no
	// call the scope admits — an unknown id and another caller's id are the
	// same answer.
	Get(ctx context.Context, scope Scope) (*Summary, error)
	// Timeline returns the session's calls in the order they were made,
	// with the session's total call count, both narrowed by the scope.
	Timeline(ctx context.Context, scope Scope) ([]TimelineEntry, int, error)
	// Assets returns the assets the session saved, oldest first. It takes
	// no scope: it is reached only through Load, after the scoped Get has
	// established that the caller may read this session.
	Assets(ctx context.Context, sessionID string) ([]AssetRef, error)
	// Insights returns the insights the session captured, oldest first.
	// Scoped the same way Assets is.
	Insights(ctx context.Context, sessionID string) ([]InsightRef, error)
}

// Reader is the whole read model an agent-facing consumer needs: the operator
// reads of Store, plus relevance search over the caller's own sessions. The
// discovery path takes this rather than Store because recalling a session has
// two halves — finding it, then opening it — and a consumer that could only
// open one would need the id it was trying to find.
type Reader interface {
	Store
	// Search ranks the caller's own sessions against a query.
	Search(ctx context.Context, q SearchQuery) ([]Match, error)
}

// ReaderFor returns the session read model over db, or nil when there is no
// database. The nil is the gate: sessions are read from the audit log, so a
// deployment without one has no session to recall, and its consumers register
// no session surface at all rather than one that answers nothing.
func ReaderFor(db *sql.DB) Reader {
	if db == nil {
		return nil
	}
	return NewPostgresStore(db)
}
