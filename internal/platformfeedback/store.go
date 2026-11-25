package platformfeedback

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// Statuses a post moves through.
const (
	StatusOpen     = "open"
	StatusPlanned  = "planned"
	StatusDone     = "done"
	StatusDeclined = "declined"
)

// validStatus reports a status a post may be set to.
func validStatus(s string) bool {
	return s == StatusOpen || s == StatusPlanned || s == StatusDone || s == StatusDeclined
}

// ErrNotFound is a post that does not exist.
var ErrNotFound = errors.New("platform feedback post not found")

// Report is one report of a post: who and in which session, and the calls it
// names.
type Report struct {
	Persona    string    `json:"persona"`
	UserID     string    `json:"-"`
	SessionID  string    `json:"-"`
	Calls      []string  `json:"calls"`
	ReportedAt time.Time `json:"reported_at"`
}

// Item is one post as the list shows it.
type Item struct {
	ID             string    `json:"id"`
	TryingTo       string    `json:"trying_to"`
	BlockedBy      string    `json:"blocked_by"`
	Tool           string    `json:"tool"`
	Suggestion     string    `json:"suggestion"`
	Status         string    `json:"status"`
	ReportCount    int       `json:"report_count"`
	CreatedAt      time.Time `json:"created_at"`
	LastReportedAt time.Time `json:"last_reported_at"`
}

// openPost is an open post's words and embedding, for folding a repeat.
type openPost struct {
	id        string
	text      string
	embedding []float32
}

// pgStore is the Postgres store.
type pgStore struct{ db *sql.DB }

// openPostsLimit bounds how many open posts a new one is compared against.
const openPostsLimit = 500

func (s pgStore) open(ctx context.Context) ([]openPost, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, trying_to || E'\n' || blocked_by || E'\n' || tool, embedding
		  FROM platform_feedback WHERE status = 'open'
		 ORDER BY last_reported_at DESC LIMIT $1`, openPostsLimit)
	if err != nil {
		return nil, fmt.Errorf("reading open platform feedback: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []openPost
	for rows.Next() {
		var (
			p   openPost
			emb pq.Float32Array
		)
		if err := rows.Scan(&p.id, &p.text, &emb); err != nil {
			return nil, fmt.Errorf("scanning open platform feedback: %w", err)
		}
		p.embedding = emb
		out = append(out, p)
	}
	return out, rows.Err() //nolint:wrapcheck // iteration error of the read above
}

func (s pgStore) create(ctx context.Context, id string, p Post, emb []float32, r Report) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning platform feedback write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var embArg any
	if len(emb) > 0 {
		embArg = pq.Float32Array(emb)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO platform_feedback (id, trying_to, blocked_by, tool, suggestion, embedding)
		VALUES ($1, $2, $3, $4, $5, $6)`, id, p.TryingTo, p.BlockedBy, p.Tool, p.Suggestion, embArg); err != nil {
		return fmt.Errorf("storing platform feedback: %w", err)
	}
	if err := insertReport(ctx, tx, id, r); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing platform feedback: %w", err)
	}
	return nil
}

func (s pgStore) addReport(ctx context.Context, id string, r Report) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("beginning platform feedback report: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var count int
	if err := tx.QueryRowContext(ctx, `
		UPDATE platform_feedback SET report_count = report_count + 1, last_reported_at = NOW()
		 WHERE id = $1 RETURNING report_count`, id).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting a platform feedback report: %w", err)
	}
	if err := insertReport(ctx, tx, id, r); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing platform feedback report: %w", err)
	}
	return count, nil
}

func insertReport(ctx context.Context, tx *sql.Tx, id string, r Report) error {
	calls := r.Calls
	if calls == nil {
		calls = []string{}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO platform_feedback_reports (feedback_id, persona, user_id, session_id, calls)
		VALUES ($1, $2, $3, $4, $5)`, id, r.Persona, r.UserID, r.SessionID, pq.StringArray(calls)); err != nil {
		return fmt.Errorf("storing a platform feedback report: %w", err)
	}
	return nil
}

// reportsSince counts reports by a session or a persona since a moment.
func (s pgStore) reportsSince(ctx context.Context, session, persona string, since time.Time) (bySession, byPersona int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FILTER (WHERE session_id = $1 AND $1 <> ''),
		       COUNT(*) FILTER (WHERE persona = $2 AND reported_at >= $3)
		  FROM platform_feedback_reports
		 WHERE (session_id = $1 AND $1 <> '') OR (persona = $2 AND reported_at >= $3)`,
		session, persona, since).Scan(&bySession, &byPersona)
	if err != nil {
		return 0, 0, fmt.Errorf("counting platform feedback reports: %w", err)
	}
	return bySession, byPersona, nil
}

func (s pgStore) refusal(ctx context.Context, reason, persona string) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO platform_feedback_refusals (reason, persona) VALUES ($1, $2)`, reason, persona); err != nil {
		return fmt.Errorf("recording a platform feedback refusal: %w", err)
	}
	return nil
}

// The list's page size: the default, and the most a page may ask for.
const (
	defaultListLimit = 50
	maxListLimit     = 200
)

// ListFilter narrows the list.
type ListFilter struct {
	Status string
	Tool   string
	Query  string
	Limit  int
	Offset int
}

func (s pgStore) list(ctx context.Context, f ListFilter) ([]Item, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > maxListLimit {
		limit = defaultListLimit
	}
	like := ""
	if f.Query != "" {
		like = "%" + f.Query + "%"
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, trying_to, blocked_by, tool, suggestion, status, report_count, created_at, last_reported_at,
		       COUNT(*) OVER ()
		  FROM platform_feedback
		 WHERE ($1 = '' OR status = $1) AND ($2 = '' OR tool = $2)
		   AND ($3 = '' OR trying_to ILIKE $3 OR blocked_by ILIKE $3 OR suggestion ILIKE $3)
		 ORDER BY last_reported_at DESC
		 LIMIT $4 OFFSET $5`, f.Status, f.Tool, like, limit, max(f.Offset, 0))
	if err != nil {
		return nil, 0, fmt.Errorf("listing platform feedback: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out, total := []Item{}, 0
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.TryingTo, &it.BlockedBy, &it.Tool, &it.Suggestion, &it.Status,
			&it.ReportCount, &it.CreatedAt, &it.LastReportedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("scanning platform feedback: %w", err)
		}
		out = append(out, it)
	}
	return out, total, rows.Err() //nolint:wrapcheck // iteration error of the read above
}

func (s pgStore) get(ctx context.Context, id string) (Item, error) {
	var it Item
	err := s.db.QueryRowContext(ctx, `
		SELECT id, trying_to, blocked_by, tool, suggestion, status, report_count, created_at, last_reported_at
		  FROM platform_feedback WHERE id = $1`, id).Scan(&it.ID, &it.TryingTo, &it.BlockedBy, &it.Tool,
		&it.Suggestion, &it.Status, &it.ReportCount, &it.CreatedAt, &it.LastReportedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return it, ErrNotFound
	}
	if err != nil {
		return it, fmt.Errorf("reading platform feedback: %w", err)
	}
	return it, nil
}

// reportsLimit bounds the reports a post's page lists.
const reportsLimit = 100

func (s pgStore) reports(ctx context.Context, id string) ([]Report, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT persona, calls, reported_at FROM platform_feedback_reports
		 WHERE feedback_id = $1 ORDER BY reported_at DESC LIMIT $2`, id, reportsLimit)
	if err != nil {
		return nil, fmt.Errorf("reading platform feedback reports: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Report{}
	for rows.Next() {
		var (
			r     Report
			calls pq.StringArray
		)
		if err := rows.Scan(&r.Persona, &calls, &r.ReportedAt); err != nil {
			return nil, fmt.Errorf("scanning a platform feedback report: %w", err)
		}
		r.Calls = []string(calls)
		if r.Calls == nil {
			r.Calls = []string{}
		}
		out = append(out, r)
	}
	return out, rows.Err() //nolint:wrapcheck // iteration error of the read above
}

func (s pgStore) setStatus(ctx context.Context, id, status, by string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE platform_feedback SET status = $2, status_changed_by = $3, status_changed_at = NOW()
		 WHERE id = $1`, id, status, by)
	if err != nil {
		return fmt.Errorf("setting a platform feedback status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delivery is a post's send to one channel, as the notification queue holds
// it.
type Delivery struct {
	Channel   string     `json:"channel"`
	Status    string     `json:"status"`
	Attempts  int        `json:"attempts"`
	LastError string     `json:"last_error,omitempty"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
}

func (s pgStore) deliveries(ctx context.Context, key string) ([]Delivery, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(channel, ''), status, attempts, last_error, sent_at
		  FROM notifications
		 WHERE payload -> 'document' ->> 'key' = $1 OR payload -> 'document' ->> 'key' LIKE $1 || ':resend:%'
		 ORDER BY created_at`, key)
	if err != nil {
		return nil, fmt.Errorf("reading platform feedback deliveries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.Channel, &d.Status, &d.Attempts, &d.LastError, &d.SentAt); err != nil {
			return nil, fmt.Errorf("scanning a platform feedback delivery: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err() //nolint:wrapcheck // iteration error of the read above
}

// Stats are the list page's counts.
type Stats struct {
	Open             int `json:"open"`
	ReportsThisWeek  int `json:"reports_this_week"`
	RefusedThisWeek  int `json:"refused_this_week"`
	DeliveryFailures int `json:"delivery_failures"`
}

func (s pgStore) stats(ctx context.Context, since time.Time) (Stats, error) {
	var st Stats
	err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM platform_feedback WHERE status = 'open'),
		       (SELECT COUNT(*) FROM platform_feedback_reports WHERE reported_at >= $1),
		       (SELECT COUNT(*) FROM platform_feedback_refusals WHERE refused_at >= $1),
		       (SELECT COUNT(*) FROM notifications
		         WHERE status = 'failed' AND payload -> 'document' ->> 'key' LIKE 'platform_feedback:%')`,
		since).Scan(&st.Open, &st.ReportsThisWeek, &st.RefusedThisWeek, &st.DeliveryFailures)
	if err != nil {
		return st, fmt.Errorf("counting platform feedback: %w", err)
	}
	return st, nil
}
