// Package levelalert evaluates conditions that have a warning and a critical
// level and routes them to notification channels (#2148, #2149).
//
// An evaluator hands Apply every condition it found at warning or critical on
// this pass. Each is sent to the configured channels under a key carrying its
// level, every pass, so the channel's repeat window decides how often a
// standing condition is repeated and a move from warning to critical is a new
// key the warning's window cannot hold back. A condition that was alerting on
// the previous pass and is not in this one has cleared: a resolved
// notification is sent once, under its own key, and its record is removed.
// The records live in alert_conditions so a restart, or the next pass running
// on another replica, still knows what was alerting.
package levelalert

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/advisorylock"
	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// Record is a condition as the previous pass left it.
type Record struct {
	Key   string
	Level Level
	Title string
	Since time.Time
}

// Store keeps the conditions each family found on its last pass.
type Store interface {
	List(ctx context.Context, family string) ([]Record, error)
	Upsert(ctx context.Context, family string, f Finding, now time.Time) error
	Delete(ctx context.Context, key string) error
}

// Engine applies a family's findings.
type Engine struct {
	store  Store
	sender Sender
	now    func() time.Time
}

// New returns an engine, or nil when store is nil. A nil sender records
// conditions and sends nothing.
func New(store Store, sender Sender) *Engine {
	if store == nil {
		return nil
	}
	return &Engine{store: store, sender: sender, now: time.Now}
}

// decimal is the base an episode's start is written in.
const decimal = 10

// Result is what one Apply did.
type Result struct {
	Alerting int
	Resolved int
}

// Apply sends each finding at its level to channels, sends a resolved
// notification for each condition of family that was alerting and is not
// among findings, and records the result.
//
// unknown names key prefixes whose conditions this pass could not judge (a
// store that could not be read): what was recorded under them is left as it
// stands, neither resolved nor sent again, until a pass can judge them.
//
// A send's key carries the condition, its level and when that level began,
// so the channel's repeat window holds back repeats of one episode while a
// condition that clears and returns, or changes level, is posted at once.
func (e *Engine) Apply(ctx context.Context, family string, channels []string, findings []Finding, unknown ...string) (Result, error) {
	if e == nil {
		return Result{}, nil
	}
	previous, err := e.store.List(ctx, family)
	if err != nil {
		return Result{}, fmt.Errorf("reading %s alert conditions: %w", family, err)
	}
	seen, errs := e.alert(ctx, family, channels, findings, previous)
	res := Result{Alerting: len(findings)}
	for _, r := range previous {
		if seen[r.Key] || hasPrefix(r.Key, unknown) {
			continue
		}
		body := fmt.Sprintf("Was %s since %s.", r.Level, r.Since.UTC().Format(time.RFC3339))
		e.send(ctx, channels, episodeKey(r.Key, "resolved", r.Since), payloadOf("resolved", "Resolved: "+r.Title, body, ""))
		if err := e.store.Delete(ctx, r.Key); err != nil {
			errs = append(errs, err)
			continue
		}
		res.Resolved++
	}
	if err := errors.Join(errs...); err != nil {
		return res, fmt.Errorf("recording %s alert conditions: %w", family, err)
	}
	return res, nil
}

// alert sends and records each finding, carrying the time its level began
// from previous, and returns the keys it saw with the failures to record.
func (e *Engine) alert(ctx context.Context, family string, channels []string, findings []Finding, previous []Record) (map[string]bool, []error) {
	now := e.now().UTC()
	before := make(map[string]Record, len(previous))
	for _, r := range previous {
		before[r.Key] = r
	}
	seen := make(map[string]bool, len(findings))
	var errs []error
	for _, f := range findings {
		seen[f.Key] = true
		since := now
		if r, ok := before[f.Key]; ok && r.Level == f.Level {
			since = r.Since
		}
		e.send(ctx, channels, episodeKey(f.Key, string(f.Level), since), payloadOf(f.Level, f.Title, f.Body, f.Link))
		if err := e.store.Upsert(ctx, family, f, now); err != nil {
			errs = append(errs, err)
		}
	}
	return seen, errs
}

// episodeKey is a send's repeat key: the condition, the level or resolved,
// and when the level began.
func episodeKey(key, level string, since time.Time) string {
	return key + ":" + level + ":" + strconv.FormatInt(since.UTC().Unix(), decimal)
}

// hasPrefix reports whether key starts with any of prefixes.
func hasPrefix(key string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// send queues one notification when there is anywhere to send it.
func (e *Engine) send(ctx context.Context, channels []string, key string, p notification.Payload) {
	if e.sender == nil || len(channels) == 0 {
		return
	}
	e.sender.Send(ctx, channels, key, p)
}

// payloadOf is the channel document a finding is posted as. The title opens
// with the level so a channel reader sorts the posts at a glance.
func payloadOf(level Level, title, body, link string) notification.Payload {
	heading := strings.ToUpper(string(level)) + ": " + title
	if level == "resolved" {
		heading = title
	}
	return notification.Payload{
		Kind:      notification.KindChannel,
		ItemTitle: heading,
		Link:      link,
		Document:  &notification.Document{Title: heading, Body: body, Link: link},
	}
}

// PostgresStore is the alert_conditions table.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore returns the store over db.
func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

// List returns family's conditions.
func (s *PostgresStore) List(ctx context.Context, family string) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, level, title, since FROM alert_conditions WHERE family = $1 ORDER BY key`, family)
	if err != nil {
		return nil, fmt.Errorf("reading alert conditions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Record, 0)
	for rows.Next() {
		var r Record
		var level string
		if err := rows.Scan(&r.Key, &level, &r.Title, &r.Since); err != nil {
			return nil, fmt.Errorf("scanning alert condition: %w", err)
		}
		r.Level = Level(level)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating alert conditions: %w", err)
	}
	return out, nil
}

// Upsert records f at now. A condition already recorded keeps when it began,
// unless its level changed, which starts it again.
func (s *PostgresStore) Upsert(ctx context.Context, family string, f Finding, now time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO alert_conditions (key, family, level, title, since, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $5)
		 ON CONFLICT (key) DO UPDATE SET
		   since = CASE WHEN alert_conditions.level = EXCLUDED.level THEN alert_conditions.since ELSE EXCLUDED.since END,
		   level = EXCLUDED.level, title = EXCLUDED.title, family = EXCLUDED.family, updated_at = EXCLUDED.updated_at`,
		f.Key, family, string(f.Level), f.Title, now); err != nil {
		return fmt.Errorf("recording alert condition: %w", err)
	}
	return nil
}

// Delete removes a condition that cleared.
func (s *PostgresStore) Delete(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM alert_conditions WHERE key = $1`, key); err != nil {
		return fmt.Errorf("removing alert condition: %w", err)
	}
	return nil
}

// Locked runs fn while holding the advisory lock key on the process's lock
// session, so one replica evaluates a family at a time. fn runs on the pool,
// never on the lock's session (#2169). It returns bgloop.ErrSkipped when
// another replica holds the lock.
func Locked(ctx context.Context, db *sql.DB, key int64, fn func(ctx context.Context) error) error {
	release, got, err := advisorylock.Try(ctx, db, advisorylock.ID(key))
	if err != nil {
		return err //nolint:wrapcheck // the lock session names the statement
	}
	if !got {
		return bgloop.ErrSkipped
	}
	defer release()
	return fn(ctx)
}

// Verify interface compliance.
var _ Store = (*PostgresStore)(nil)
