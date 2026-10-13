package catalogscan

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SettingsSection is the platform_settings row the catalog's settings live in.
// They are configured in the admin portal, not the configuration file.
const SettingsSection = "catalog"

// DefaultScanInterval is how often every connection is scanned when no
// interval has been set.
const DefaultScanInterval = 6 * time.Hour

// MinScanInterval is the shortest interval an operator may set, so a
// mistyped value cannot keep every connection's engine busy.
const MinScanInterval = 5 * time.Minute

// Exclusion is the part of one connection an operator keeps out of the
// catalog: dotted path prefixes, such as a Trino catalog or catalog.schema.
type Exclusion struct {
	ConnectionKind string   `json:"connection_kind"`
	Connection     string   `json:"connection"`
	Entries        []string `json:"entries"`
}

// Settings are the catalog's operator settings.
type Settings struct {
	// ScanInterval is how often every connection is scanned, as a Go
	// duration ("6h"). Empty means DefaultScanInterval.
	ScanInterval string      `json:"scan_interval"`
	Exclusions   []Exclusion `json:"exclusions"`
	UpdatedBy    string      `json:"updated_by,omitempty"`
	UpdatedAt    *time.Time  `json:"updated_at,omitempty"`
}

// Defaults are the settings of a deployment that never set any.
func Defaults() Settings {
	return Settings{ScanInterval: DefaultScanInterval.String(), Exclusions: make([]Exclusion, 0)}
}

// Interval is the scan interval, falling back to the default for an empty or
// unreadable value.
func (s Settings) Interval() time.Duration {
	d, err := time.ParseDuration(s.ScanInterval)
	if err != nil || d <= 0 {
		return DefaultScanInterval
	}
	return d
}

// Validate refuses an interval that does not parse or is too short, and an
// exclusion that names no connection.
func (s Settings) Validate() error {
	d, err := time.ParseDuration(s.ScanInterval)
	if err != nil {
		return fmt.Errorf("scan_interval %q is not a duration such as 6h or 90m", s.ScanInterval)
	}
	if d < MinScanInterval {
		return fmt.Errorf("scan_interval must be at least %s", MinScanInterval)
	}
	for _, x := range s.Exclusions {
		if strings.TrimSpace(x.ConnectionKind) == "" || strings.TrimSpace(x.Connection) == "" {
			return errors.New("an exclusion needs a connection kind and a connection")
		}
	}
	return nil
}

// ExclusionsFor returns what is excluded on one connection.
func (s Settings) ExclusionsFor(kind, connection string) Exclusions {
	var entries []string
	for _, x := range s.Exclusions {
		if x.ConnectionKind == kind && x.Connection == connection {
			entries = append(entries, x.Entries...)
		}
	}
	return ParseExclusions(entries)
}

// SettingsReader reads the catalog settings.
type SettingsReader interface {
	Get(ctx context.Context) (Settings, error)
}

// Store keeps the catalog settings and each connection's last scan outcome.
type Store struct {
	db *sql.DB
}

// NewStore returns the store over db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const selectSettings = `SELECT value, updated_by, updated_at FROM platform_settings WHERE section = $1`

// Get returns the stored settings, or the defaults when none were saved.
func (s *Store) Get(ctx context.Context) (Settings, error) {
	var raw []byte
	var by string
	var at time.Time
	err := s.db.QueryRowContext(ctx, selectSettings, SettingsSection).Scan(&raw, &by, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Defaults(), nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("reading catalog settings: %w", err)
	}
	set := Defaults()
	if err := json.Unmarshal(raw, &set); err != nil {
		return Settings{}, fmt.Errorf("decoding catalog settings: %w", err)
	}
	if set.Exclusions == nil {
		set.Exclusions = make([]Exclusion, 0)
	}
	set.UpdatedBy, set.UpdatedAt = by, &at
	return set, nil
}

const upsertSettings = `
INSERT INTO platform_settings (section, value, updated_by) VALUES ($1, $2, $3)
ON CONFLICT (section) DO UPDATE SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()`

// Set saves the settings after validating them.
func (s *Store) Set(ctx context.Context, in Settings, author string) error {
	if err := in.Validate(); err != nil {
		return err
	}
	in.UpdatedBy, in.UpdatedAt = "", nil
	raw, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encoding catalog settings: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, upsertSettings, SettingsSection, raw, author); err != nil {
		return fmt.Errorf("saving catalog settings: %w", err)
	}
	return nil
}

const upsertOutcome = `
INSERT INTO catalog_scans (connection_kind, connection_name, started_at, finished_at, datasets, removed, error, unread)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (connection_kind, connection_name) DO UPDATE SET
    started_at = EXCLUDED.started_at, finished_at = EXCLUDED.finished_at,
    datasets = EXCLUDED.datasets, removed = EXCLUDED.removed, error = EXCLUDED.error,
    unread = EXCLUDED.unread`

// RecordOutcome keeps one connection's last scan.
func (s *Store) RecordOutcome(ctx context.Context, o Outcome) error {
	unread := o.Unread
	if unread == nil {
		unread = []Unread{}
	}
	raw, err := json.Marshal(unread)
	if err != nil {
		return fmt.Errorf("encoding unread parts: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, upsertOutcome,
		o.ConnectionKind, o.Connection, o.StartedAt, o.FinishedAt, o.Datasets, o.Removed, o.Error, raw); err != nil {
		return fmt.Errorf("recording scan outcome: %w", err)
	}
	return nil
}

const selectOutcomes = `
SELECT connection_kind, connection_name, started_at, finished_at, datasets, removed, error, unread
FROM catalog_scans ORDER BY connection_kind, connection_name`

// Outcomes returns every connection's last scan.
func (s *Store) Outcomes(ctx context.Context) ([]Outcome, error) {
	rows, err := s.db.QueryContext(ctx, selectOutcomes)
	if err != nil {
		return nil, fmt.Errorf("reading scan outcomes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Outcome, 0)
	for rows.Next() {
		var o Outcome
		var raw []byte
		if err := rows.Scan(&o.ConnectionKind, &o.Connection, &o.StartedAt, &o.FinishedAt,
			&o.Datasets, &o.Removed, &o.Error, &raw); err != nil {
			return nil, fmt.Errorf("scanning scan outcome: %w", err)
		}
		o.Unread = make([]Unread, 0)
		if err := json.Unmarshal(raw, &o.Unread); err != nil {
			return nil, fmt.Errorf("decoding unread parts: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading scan outcomes: %w", err)
	}
	return out, nil
}
