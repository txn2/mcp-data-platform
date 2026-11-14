package approval

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

// defaultLimit and maxLimit bound a listing.
const (
	defaultLimit = 100
	maxLimit     = 500
)

// uniqueViolation is PostgreSQL's SQLSTATE for a unique index refusing a row.
const uniqueViolation = "23505"

// Filter narrows a listing.
type Filter struct {
	// Status is one status, empty for every one.
	Status string
	// Requester is one requester's requests, empty for everyone's.
	Requester string
	// ScriptID is one script's requests, empty for every script's.
	ScriptID string
	// SiteReleaseID is one site release's requests, empty for every one's.
	SiteReleaseID string
	// SiteID is one site's environment requests, empty for every site's.
	SiteID string
	// Approver and ApproverPersona widen a requester's listing to the call
	// requests whose policies name them, by address or persona.
	Approver        string
	ApproverPersona string
	// Kind is one kind of request (call, handler_version, form, site),
	// empty for every kind; Purpose one purpose.
	Kind    string
	Purpose string
	Limit   int
}

// Decision is what an administrator decided about a request.
type Decision struct {
	Status       string
	By           string
	Comment      string
	SelfApproved bool
	At           time.Time
	// Roles are the approver's roles, recorded for a handler version.
	Roles []string
	// CallsApproved and Until are an allowance's count and deadline.
	CallsApproved int
	Until         *time.Time
}

// PostgresStore keeps requests in approval_requests.
type PostgresStore struct{ db *sql.DB }

// NewPostgresStore builds the store over db.
func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

const requestColumns = `r.id, r.subject_kind, r.purpose, COALESCE(r.script_id::text, ''), COALESCE(s.name, ''), r.version, r.source_sha256,
	r.requester_email, r.requester_persona, r.reason, r.changes, r.status, r.decided_by, r.decided_at,
	r.decision_comment, r.self_approved, r.created_at, COALESCE(r.site_release_id, ''), r.target, COALESCE(st.name, ''), COALESCE(r.site_id, ''),
	r.decided_roles, r.call_tool, r.call_connection, r.call_operation, r.call_arguments, r.call_rendering, r.call_handlers,
	r.approvers, r.calls_asked, r.calls_approved, r.calls_used, r.allowance_until`

const requestFrom = ` FROM approval_requests r LEFT JOIN scripts s ON s.id = r.script_id
	LEFT JOIN site_releases sr ON sr.id = r.site_release_id LEFT JOIN sites st ON st.id = COALESCE(sr.site_id, r.site_id)`

// Insert stores a new pending request.
func (p *PostgresStore) Insert(ctx context.Context, r *Request) error {
	changes, err := json.Marshal(r.Changes)
	if err != nil {
		return fmt.Errorf("encoding the request's changes: %w", err)
	}
	args, handlers, err := encodeCall(r.CallSubject)
	if err != nil {
		return err
	}
	err = p.db.QueryRowContext(ctx, `
		INSERT INTO approval_requests (id, subject_kind, purpose, script_id, version, source_sha256,
			requester_email, requester_persona, reason, changes, site_release_id, target, site_id,
			call_tool, call_connection, call_operation, call_arguments, call_rendering, call_handlers, approvers, calls_asked)
		VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5, $6, $7, $8, $9, $10, NULLIF($11, ''), $12, NULLIF($13, ''),
			$14, $15, $16, $17, $18, $19, $20, $21)
		RETURNING status, created_at`,
		r.ID, r.SubjectKind, r.Purpose, r.ScriptID, r.Version, r.SourceSHA256,
		r.RequesterEmail, r.RequesterPersona, r.Reason, changes, r.SiteReleaseID, r.Target, r.SiteID,
		r.CallTool, r.CallConnection, r.CallOperation, args, r.Rendering, handlers, pq.Array(nonNil(r.Approvers)), r.CallsAsked,
	).Scan(&r.Status, &r.CreatedAt)
	r.Kind = kindOf(r.SubjectKind, r.Purpose)
	if isUnique(err) {
		return ErrPending
	}
	if err != nil {
		return fmt.Errorf("storing the request: %w", err)
	}
	return nil
}

// Get reads one request.
func (p *PostgresStore) Get(ctx context.Context, id string) (*Request, error) {
	r, err := scanRequest(p.db.QueryRowContext(ctx, `SELECT `+requestColumns+requestFrom+` WHERE r.id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// List reads requests newest first.
func (p *PostgresStore) List(ctx context.Context, f Filter) ([]Request, error) {
	q, args := listQuery(f)
	rows, err := p.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing requests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Request{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing requests: %w", err)
	}
	return out, nil
}

// listQuery is the listing's statement for f and its arguments.
func listQuery(f Filter) (q string, args []any) {
	var where []string
	add := func(clause, v string) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(clause, "?", "$"+strconv.Itoa(len(args))))
	}
	if f.Status != "" {
		add("r.status = ?", f.Status)
	}
	switch {
	case f.Requester != "" && f.Approver != "":
		args = append(args, f.Requester, f.Approver, f.ApproverPersona)
		n := len(args)
		where = append(where, fmt.Sprintf("(r.requester_email = $%d OR (r.subject_kind = 'call' AND (lower($%d) = ANY(SELECT lower(a) FROM unnest(r.approvers) a) OR ($%d <> '' AND $%d = ANY(r.approvers)))))", n-2, n-1, n, n))
	case f.Requester != "":
		add("r.requester_email = ?", f.Requester)
	}
	if f.ScriptID != "" {
		add("r.script_id::text = ?", f.ScriptID)
	}
	if f.SiteReleaseID != "" {
		add("r.site_release_id = ?", f.SiteReleaseID)
	}
	if f.SiteID != "" {
		add("r.site_id = ?", f.SiteID)
	}
	if f.Purpose != "" {
		add("r.purpose = ?", f.Purpose)
	}
	if clause, ok := kindClauses[f.Kind]; ok {
		where = append(where, clause)
	}
	q = `SELECT ` + requestColumns + requestFrom
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit <= 0 || limit > maxLimit {
		limit = defaultLimit
	}
	return q + ` ORDER BY r.created_at DESC, r.id LIMIT ` + strconv.Itoa(limit), args
}

// kindClauses are the conditions each kind a list filters by reads.
var kindClauses = map[string]string{
	KindCall:           "r.subject_kind = 'call'",
	KindHandlerVersion: "r.purpose = 'call_handler'",
	KindForm:           "r.purpose = 'form_public'",
	KindSite:           "r.subject_kind IN ('site_release', 'site_environment')",
}

// Decide records a pending request's decision. A call request's frozen
// arguments are deleted with it (#2437): the approver read them to decide,
// the allowance is spent by connection and operation, and the platform's
// rendering stays as the record of what was asked.
func (p *PostgresStore) Decide(ctx context.Context, id string, d Decision) (*Request, error) {
	res, err := p.db.ExecContext(ctx, `
		UPDATE approval_requests
		SET status = $2, decided_by = $3, decision_comment = $4, self_approved = $5, decided_at = $6,
		    decided_roles = $7, calls_approved = $8, allowance_until = $9,
		    call_arguments = CASE WHEN subject_kind = 'call' THEN NULL ELSE call_arguments END
		WHERE id = $1 AND status = 'pending'`, id, d.Status, d.By, d.Comment, d.SelfApproved, d.At,
		pq.Array(nonNil(d.Roles)), d.CallsApproved, d.Until)
	if err != nil {
		return nil, fmt.Errorf("deciding the request: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := p.Get(ctx, id); err != nil {
			return nil, err
		}
		return nil, ErrDecided
	}
	return p.Get(ctx, id)
}

// LatestApproved is the highest version approved for a script and purpose.
func (p *PostgresStore) LatestApproved(ctx context.Context, scriptID, purpose string) (*Request, error) {
	r, err := scanRequest(p.db.QueryRowContext(ctx, `SELECT `+requestColumns+requestFrom+`
		WHERE r.script_id = $1 AND r.purpose = $2 AND r.status = 'approved'
		ORDER BY r.version DESC, r.decided_at DESC LIMIT 1`, scriptID, purpose))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// ReleaseApproved reports whether a site release is approved for a target
// at the digest the approver was shown.
func (p *PostgresStore) ReleaseApproved(ctx context.Context, releaseID, target, digest string) (bool, error) {
	var found bool
	err := p.db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM approval_requests
		                WHERE site_release_id = $1 AND target = $2 AND source_sha256 = $3 AND status = 'approved')`,
		releaseID, target, digest).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("reading the release's approval: %w", err)
	}
	return found, nil
}

// EnvironmentApproved reports whether making a site environment public is
// approved for the access stamp the approver was shown.
func (p *PostgresStore) EnvironmentApproved(ctx context.Context, siteID, target, stamp string) (bool, error) {
	var found bool
	err := p.db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM approval_requests
		                WHERE subject_kind = 'site_environment' AND site_id = $1 AND target = $2
		                  AND source_sha256 = $3 AND status = 'approved')`,
		siteID, target, stamp).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("reading the environment's approval: %w", err)
	}
	return found, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanRequest(row rowScanner) (*Request, error) {
	var (
		r                    Request
		changes, args, asked []byte
		decided, until       sql.NullTime
	)
	err := row.Scan(&r.ID, &r.SubjectKind, &r.Purpose, &r.ScriptID, &r.ScriptName, &r.Version, &r.SourceSHA256,
		&r.RequesterEmail, &r.RequesterPersona, &r.Reason, &changes, &r.Status, &r.DecidedBy, &decided,
		&r.DecisionComment, &r.SelfApproved, &r.CreatedAt, &r.SiteReleaseID, &r.Target, &r.SiteName, &r.SiteID,
		pq.Array(&r.DecidedRoles), &r.CallTool, &r.CallConnection, &r.CallOperation, &args, &r.Rendering, &asked,
		pq.Array(&r.Approvers), &r.CallsAsked, &r.CallsApproved, &r.CallsUsed, &until)
	if err != nil {
		return nil, fmt.Errorf("reading a request: %w", err)
	}
	r.Kind = kindOf(r.SubjectKind, r.Purpose)
	if until.Valid {
		t := until.Time
		r.Until = &t
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &r.CallArguments)
	}
	if len(asked) > 0 {
		_ = json.Unmarshal(asked, &r.Handlers)
	}
	if decided.Valid {
		t := decided.Time
		r.DecidedAt = &t
	}
	if len(changes) > 0 {
		if err := json.Unmarshal(changes, &r.Changes); err != nil {
			return nil, fmt.Errorf("decoding a request's changes: %w", err)
		}
	}
	return &r, nil
}

// isUnique reports a unique index refusing the row.
func isUnique(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == uniqueViolation
}

// encodeCall is a call subject's arguments and handlers as stored; a request
// that is not a call stores no arguments.
func encodeCall(c CallSubject) (args, handlers []byte, err error) {
	if c.CallArguments != nil {
		if args, err = json.Marshal(c.CallArguments); err != nil {
			return nil, nil, fmt.Errorf("encoding the call's arguments: %w", err)
		}
	}
	asked := c.Handlers
	if asked == nil {
		asked = []HandlerAsk{}
	}
	if handlers, err = json.Marshal(asked); err != nil {
		return nil, nil, fmt.Errorf("encoding the call's handlers: %w", err)
	}
	return args, handlers, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// QueueRollup is the approvals queue as its staleness alert reads it: every
// pending request, those filed before a time, and when the oldest was filed.
type QueueRollup struct {
	Pending, Stale int
	Oldest         *time.Time
}

// PendingRollup is the approvals queue's rollup (reviewalert.ApprovalSource),
// staleBefore deciding which requests are stale.
func (p *PostgresStore) PendingRollup(ctx context.Context, staleBefore time.Time) (QueueRollup, error) {
	var (
		q     QueueRollup
		first sql.NullTime
	)
	err := p.db.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE created_at < $1), MIN(created_at)
		FROM approval_requests WHERE status = 'pending'`, staleBefore).Scan(&q.Pending, &q.Stale, &first)
	if err != nil {
		return QueueRollup{}, fmt.Errorf("reading the approvals queue: %w", err)
	}
	if first.Valid {
		q.Oldest = &first.Time
	}
	return q, nil
}
