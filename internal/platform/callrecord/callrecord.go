// Package callrecord is the catalog of what the platform's data calls actually
// did: every SQL query and API invocation as a record with a purpose, a target,
// and a fate (issue #1321).
//
// A record is not an audit row. The audit log answers "who called what, when",
// keeps its rows for a fixed retention window, and may drop or redact the
// arguments a call carried. A record answers "is this query worth running
// again", and its statement is the whole point of keeping it. The two are
// joined by event_id, which is the id a call already hands back to its caller
// as mcp:call:<event_id> (#1320).
//
// # Outcome is derived, never stored
//
// A record's outcome is computed on read from three facts that live elsewhere:
// whether the call itself failed, whether anything later cited it as a source,
// and whether the same session ran a better version of it afterwards. Storing
// the outcome would mean recomputing it every time an asset is saved, an
// insight is captured, or a query is re-run — and being wrong in between.
// Deriving it means a record's fate is always the current answer, and that the
// rule can be read in one place (the SQL in postgres.go) rather than
// reconstructed from the write paths that would have maintained it.
//
//nolint:revive // max-public-structs: this package's exported surface is one cohesive catalog (the record and its artifact, the filter/scope/fetcher a read is bounded by, the two decisions a review makes, the search query and its hit, plus the store, recorder and promoter contracts), not a heap of unrelated types.
package callrecord

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
)

// ErrNotFound is returned when no record matches the scope. A record belonging
// to another caller and a record that never existed produce the same error, so
// a caller cannot probe for the existence of someone else's query.
var ErrNotFound = errors.New("call record not found")

// Call kinds. One record shape serves both; each kind fills the columns it has
// and leaves the other kind's empty.
const (
	// KindSQL is a query against a query engine (trino_query, trino_execute,
	// trino_export).
	KindSQL = "sql"
	// KindAPI is an HTTP invocation through the API gateway
	// (api_invoke_endpoint, api_export).
	KindAPI = "api"
)

// Outcomes, in the order they are decided. A call that failed is failed
// whatever else happened; a call something was built from is satisfied even if
// the session later ran a better one; a call replaced by a later one over the
// same targets is superseded; anything else simply ran.
const (
	// OutcomeFailed means the call itself returned an error.
	OutcomeFailed = "failed"
	// OutcomeSatisfied means an asset, an export, or a captured insight named
	// this call as a source. It is the only outcome that says the call
	// answered something.
	OutcomeSatisfied = "satisfied"
	// OutcomeSuperseded means a later successful call in the same session
	// addressed the same targets over the same connection, and nothing was
	// ever built from this one: a draft the agent corrected.
	OutcomeSuperseded = "superseded"
	// OutcomeRan means the call succeeded and nothing has come of it yet.
	OutcomeRan = "ran"
)

// Outcomes lists every outcome a record can read, for validating a filter.
var Outcomes = []string{OutcomeFailed, OutcomeSatisfied, OutcomeSuperseded, OutcomeRan}

// How a record came to be satisfied. The reviewer sees this at promotion
// because the routes differ in what they cost the agent: an asset or an export
// is a by-product of work the agent was doing anyway, while a capture is the
// agent stating, in its own words and at the price of writing a description,
// that this query answered the question.
const (
	// SatisfiedByAsset means a saved asset cites the call.
	SatisfiedByAsset = "asset"
	// SatisfiedByExport means an export (trino_export, api_export) cites it.
	SatisfiedByExport = "export"
	// SatisfiedByCapture means a memory_capture insight names the call's
	// mcp:call:<event_id> reference in its sources.
	SatisfiedByCapture = "capture"
)

// Record is one data-access call, cataloged.
type Record struct {
	ID string `json:"id" example:"9b1c0f26-1a3e-4c5f-9d0b-2f7a6e5c4d31"`
	// EventID is the audit event this call was recorded under and the key of
	// its mcp:call:<event_id> reference.
	EventID string `json:"event_id" example:"a1b2c3d4e5f6g7h8"`
	// Reference is EventID in the form an agent cites.
	Reference string `json:"reference" example:"mcp:call:a1b2c3d4e5f6g7h8"`
	Kind      string `json:"kind" example:"sql"`
	ToolName  string `json:"tool_name" example:"trino_query"`
	// Connection is the named connection the call went through. It is part of
	// a record's identity: the same statement against two warehouses is two
	// records, and reuse never crosses connections.
	Connection string `json:"connection,omitempty" example:"acme-warehouse"`

	// Statement is the SQL text, on a sql record.
	Statement string `json:"statement,omitempty"`
	// Method, Path and OperationID are the request line, on an api record.
	Method      string `json:"method,omitempty" example:"GET"`
	Path        string `json:"path,omitempty" example:"/v1/orders"`
	OperationID string `json:"operation_id,omitempty" example:"listOrders"`

	// Targets are what the call addressed: DataHub dataset URNs parsed from
	// the SQL, or the endpoint identity for an API call. Sorted and
	// deduplicated, so two records over the same tables compare equal.
	Targets []string `json:"targets"`

	// Purpose is the reason the caller stated for making the call (#1317).
	Purpose   string `json:"purpose,omitempty" example:"Sizing Q3 revenue by region for the board deck."`
	UserID    string `json:"user_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
	UserEmail string `json:"user_email,omitempty" example:"marcus.johnson@example.com"`
	SessionID string `json:"session_id,omitempty" example:"dps_9f2c1a4b8e7d6c5a"`
	Persona   string `json:"persona,omitempty" example:"data-engineer"`

	Success       bool   `json:"success" example:"true"`
	ErrorMessage  string `json:"error_message,omitempty"`
	DurationMS    int64  `json:"duration_ms" example:"143"`
	ResponseChars int    `json:"response_chars" example:"2450"`

	// Outcome is derived on every read; see the package comment.
	Outcome string `json:"outcome" example:"satisfied"`
	// SatisfiedBy names the route that satisfied the record (asset, export,
	// capture). Empty on every other outcome.
	SatisfiedBy string `json:"satisfied_by,omitempty" example:"capture"`
	// Artifacts are what was built from this call: the assets, exports and
	// captured insights that cite it.
	Artifacts []Artifact `json:"artifacts,omitempty"`
	// ReuseCount is how many later sessions fetched this record and then ran
	// what it holds. It is the only signal on a record that a stranger, and
	// not its author, found it worth running.
	ReuseCount int `json:"reuse_count" example:"2"`

	PromotedURN   string     `json:"promoted_urn,omitempty" example:"urn:li:query:abc123"`
	PromotedAt    *time.Time `json:"promoted_at,omitempty"`
	PromotedBy    string     `json:"promoted_by,omitempty" example:"marcus.johnson@example.com"`
	RejectedAt    *time.Time `json:"rejected_at,omitempty"`
	RejectedBy    string     `json:"rejected_by,omitempty" example:"marcus.johnson@example.com"`
	RejectionNote string     `json:"rejection_note,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// Artifact is one thing built from a call: an asset, an export, or a captured
// insight that named the call as a source.
type Artifact struct {
	// Kind is one of the SatisfiedBy values.
	Kind string `json:"kind" example:"asset"`
	ID   string `json:"id" example:"ast_7c1e"`
	Name string `json:"name" example:"Q3 revenue by region"`
}

// Promotable reports whether this record may be promoted: it must have
// answered something, and it must not already have been promoted or rejected.
func (r Record) Promotable() bool {
	return r.Outcome == OutcomeSatisfied && r.PromotedURN == "" && r.RejectedAt == nil
}

// Filter selects records for a list. UserID is deliberately not read from any
// query string (see FilterFromQuery); each surface assigns it itself.
type Filter struct {
	// UserID restricts the list to one caller's records. Empty is
	// unrestricted, which only the operator surface may ask for.
	UserID string
	// Kind, Connection and Outcome are exact-match facets.
	Kind       string
	Connection string
	Outcome    string
	// Target keeps records addressing one dataset URN.
	Target string
	// SessionID keeps the calls one session made.
	SessionID string
	// EventIDs keeps the records of named audit events. It is how a caller
	// that already holds a page of events — a session's timeline — reads the
	// records for exactly those events, rather than reading a session's whole
	// history and discarding most of it. Empty states no restriction.
	EventIDs []string
	// Search matches the purpose and the statement.
	Search string
	// PromotableOnly keeps the records a reviewer can act on: satisfied, not
	// yet promoted, not rejected. It is the review queue.
	PromotableOnly bool
	Limit          int
	Offset         int
}

// Scope names one record and, optionally, the only caller allowed to read it.
// The restriction is a predicate inside the query rather than a comparison the
// handler makes afterwards, so another caller's record id is answered
// not-found — the same answer an id that was never used gets.
type Scope struct {
	ID string
	// UserID restricts the read to that caller's own records. Empty is
	// unrestricted.
	UserID string
}

// Fetcher identifies the session dereferencing a record. Reuse is credited to
// a session, not to a person: the question a reuse count answers is how many
// separate pieces of work this query was found and used by.
type Fetcher struct {
	SessionID string
	UserID    string
}

// Promotion records what a record became.
type Promotion struct {
	URN   string
	Actor string
}

// Rejection records that a record was reviewed and declined, so the queue does
// not offer it again.
type Rejection struct {
	Actor string
	Note  string
}

// Store reads and writes call records. Implemented over PostgreSQL by
// PostgresStore; a deployment with no database keeps no catalog, and every
// surface that needs one stays unregistered.
type Store interface {
	// Insert records one call. It is idempotent on event id: the same call
	// recorded twice yields one record.
	Insert(ctx context.Context, r Record) error
	// List returns records matching the filter, newest first, with their
	// outcomes derived.
	List(ctx context.Context, f Filter) ([]Record, error)
	// Count returns how many records match the filter, ignoring its paging.
	Count(ctx context.Context, f Filter) (int, error)
	// Get returns one record with its outcome, artifacts and reuse count, or
	// ErrNotFound when the scope admits none.
	Get(ctx context.Context, scope Scope) (*Record, error)
	// GetByEventID returns the record for one audit event id, scoped the same
	// way Get is. It is how an mcp:call:<event_id> reference resolves.
	GetByEventID(ctx context.Context, eventID, userID string) (*Record, error)
	// RecordFetch notes that a session dereferenced this record, which is the
	// first half of reuse.
	RecordFetch(ctx context.Context, recordID string, by Fetcher) error
	// CreditReuse credits every earlier record that the given call re-ran:
	// one this session had fetched, produced by a different session, with the
	// same kind, connection and statement (or operation). Returns how many
	// records were credited.
	CreditReuse(ctx context.Context, r Record) (int, error)
	// ForTargets returns satisfied records addressing any of the given
	// dataset URNs, most reused first. It is what the enrichment path shows
	// beside a table.
	ForTargets(ctx context.Context, urns []string, userID string, limit int) ([]Record, error)
	// Promote stores what the record became.
	Promote(ctx context.Context, id string, p Promotion) error
	// Reject records that the record was declined.
	Reject(ctx context.Context, id string, r Rejection) error
}

// NormalizeStatement collapses a statement to the form reuse matching compares:
// whitespace runs become single spaces, and the whole is trimmed and lowercased.
// Two agents that indent the same query differently have run the same query.
func NormalizeStatement(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// ValidOutcome reports whether s names an outcome. Used to drop an unknown
// facet rather than pass it into the query as a value nothing matches.
func ValidOutcome(s string) bool { return slices.Contains(Outcomes, s) }
