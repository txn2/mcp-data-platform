// Package scriptbgdraft is the long draft run (#2117): an administrator can
// run_draft an unsaved script past the 60 seconds a draft is held to, up to
// the deployment's scripts.draft.max_seconds, for a flow a short draft cannot
// get through -- driving a slow web app, a long export. A draft longer than
// the inline wait runs in the background and answers with its run id at once;
// get_run reads its log and progress as it goes, and the draft's whole result
// once it ends. It still runs as the caller, writes only with allow_writes and
// is recorded for tests, exactly as an inline draft is.
//
// A background draft is kept in script_draft_runs, so get_run answers it on
// any replica. The replica executing it writes what it has reported every few
// seconds; a draft whose replica stopped reads as failed once its deadline
// has passed.
package scriptbgdraft

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
)

// DefaultSeconds is a draft's time limit when the deployment sets none, which
// is also the longest a draft is waited on inline.
const DefaultSeconds = int(scriptrun.DraftTimeout / time.Second)

// CeilingSeconds is the most scripts.draft.max_seconds may be.
const CeilingSeconds = 3600

// MaxSecondsKey names the setting in a refusal.
const MaxSecondsKey = "scripts.draft.max_seconds"

// Config is the scripts.draft section.
type Config struct {
	// MaxSeconds is the longest an administrator's run_draft may ask for with
	// timeout_seconds. Zero is DefaultSeconds, which no draft may pass.
	MaxSeconds int `yaml:"max_seconds,omitempty"`
}

// Validate refuses a ceiling a draft cannot be given.
func (c Config) Validate() error {
	if c.MaxSeconds != 0 && (c.MaxSeconds < DefaultSeconds || c.MaxSeconds > CeilingSeconds) {
		return fmt.Errorf("draft limit %s must be between %d and %d, not %d", MaxSecondsKey, DefaultSeconds, CeilingSeconds, c.MaxSeconds)
	}
	return nil
}

// Max is the longest a draft may run on this deployment.
func (c Config) Max() time.Duration {
	if c.MaxSeconds <= 0 {
		return scriptrun.DraftTimeout
	}
	return time.Duration(c.MaxSeconds) * time.Second
}

// ErrTooLong is a timeout_seconds the caller may not be given.
var ErrTooLong = errors.New("the draft time limit asked for is not available")

// Decide reads run_draft's timeout_seconds for a caller: the draft's time
// limit, and whether it runs in the background because it is longer than a
// draft is waited on. Zero is the default limit.
func (c Config) Decide(seconds int, admin bool) (limit time.Duration, background bool, err error) {
	if seconds <= 0 {
		return scriptrun.DraftTimeout, false, nil
	}
	limit = time.Duration(seconds) * time.Second
	if limit > scriptrun.DraftTimeout && !admin {
		return 0, false, fmt.Errorf("%w: a draft longer than %d seconds is an administrator's to run; "+
			"save the script and run it with run_script instead", ErrTooLong, DefaultSeconds)
	}
	if limit > c.Max() {
		return 0, false, fmt.Errorf("timeout_seconds is at most %d on this deployment, set by %s: %w",
			int(c.Max()/time.Second), MaxSecondsKey, ErrTooLong)
	}
	return limit, limit > scriptrun.DraftTimeout, nil
}

// Status values a background draft has.
const (
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// Run is one background draft as get_run reads it.
type Run struct {
	ID         string          `json:"run_id"`
	Owner      string          `json:"-"`
	Script     string          `json:"script"`
	Status     string          `json:"status"`
	Log        string          `json:"log"`
	Progress   json.RawMessage `json:"progress,omitempty"`
	Error      string          `json:"error,omitempty"`
	Draft      json.RawMessage `json:"draft,omitempty"`
	StartedAt  time.Time       `json:"started_at"`
	Deadline   time.Time       `json:"deadline"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	// ReportedAt is when the executing replica last wrote the draft.
	ReportedAt time.Time `json:"-"`
}

// ErrNotFound is a run id no background draft has.
var ErrNotFound = errors.New("no background draft has that id")

// Store keeps background drafts.
type Store struct{ db *sql.DB }

// NewStore returns the store over db, or nil without one.
func NewStore(db *sql.DB) *Store {
	if db == nil {
		return nil
	}
	return &Store{db: db}
}

// keep is how long a finished draft is answered for.
const keep = "7 days"

// start records a draft starting, and drops the ones finished long ago.
func (s *Store) start(ctx context.Context, r Run) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM script_draft_runs WHERE finished_at < NOW() - $1::interval`, keep); err != nil {
		return fmt.Errorf("scriptbgdraft: dropping old drafts: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO script_draft_runs (id, owner_email, script_name, status, deadline)
		VALUES ($1, $2, $3, $4, $5)`, r.ID, r.Owner, r.Script, StatusRunning, r.Deadline)
	if err != nil {
		return fmt.Errorf("scriptbgdraft: recording the draft: %w", err)
	}
	return nil
}

// report writes what the draft has reported so far.
func (s *Store) report(ctx context.Context, id, log string, progress json.RawMessage) error {
	_, err := s.db.ExecContext(ctx, `UPDATE script_draft_runs SET log = $2, progress = $3, reported_at = NOW()
		WHERE id = $1 AND status = 'running'`, id, log, nullJSON(progress))
	if err != nil {
		return fmt.Errorf("scriptbgdraft: recording progress: %w", err)
	}
	return nil
}

// finish records how the draft ended.
func (s *Store) finish(ctx context.Context, r Run) error {
	_, err := s.db.ExecContext(ctx, `UPDATE script_draft_runs
		SET status = $2, log = $3, progress = $4, error = $5, draft = $6, finished_at = NOW()
		WHERE id = $1`, r.ID, r.Status, r.Log, nullJSON(r.Progress), r.Error, nullJSON(r.Draft))
	if err != nil {
		return fmt.Errorf("scriptbgdraft: recording the end: %w", err)
	}
	return nil
}

// Get reads one background draft.
func (s *Store) Get(ctx context.Context, id string) (*Run, error) {
	var (
		r                 Run
		progress, outcome []byte
		finished          sql.NullTime
	)
	err := s.db.QueryRowContext(ctx, `SELECT id, owner_email, script_name, status, log, progress, error, draft,
		started_at, deadline, finished_at, reported_at FROM script_draft_runs WHERE id = $1`, id).
		Scan(&r.ID, &r.Owner, &r.Script, &r.Status, &r.Log, &progress, &r.Error, &outcome,
			&r.StartedAt, &r.Deadline, &finished, &r.ReportedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scriptbgdraft: reading the draft: %w", err)
	}
	r.Progress, r.Draft = progress, outcome
	if finished.Valid {
		r.FinishedAt = &finished.Time
	}
	return &r, nil
}

func nullJSON(v json.RawMessage) any {
	if len(v) == 0 {
		return nil
	}
	return []byte(v)
}
