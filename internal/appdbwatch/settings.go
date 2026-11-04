package appdbwatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
)

// SettingsSection is the platform_settings section the health settings are
// kept in.
const SettingsSection = "appdb_health"

// Defaults.
const (
	DefaultIntervalSeconds    = 300
	DefaultHistoryDays        = 30
	DefaultLongRunningSeconds = 60
	// MinIntervalSeconds is the shortest interval a database is sampled at.
	MinIntervalSeconds = 10
	// MaxHistoryDays bounds how long samples are kept.
	MaxHistoryDays = 3650
)

// The default thresholds of each condition, a warning and a critical level.
var (
	DefaultTransactionIDAge   = levelalert.Thresholds{Warning: 500_000_000, Critical: 1_000_000_000}
	DefaultIdleInTransaction  = levelalert.Thresholds{Warning: 300, Critical: 1800}
	DefaultConnectionsPercent = levelalert.Thresholds{Warning: 80, Critical: 95}
	DefaultInvalidIndexes     = levelalert.Thresholds{Warning: 1}
	DefaultDeadTuplePercent   = levelalert.Thresholds{Warning: 20, Critical: 50}
	DefaultDriftObjects       = levelalert.Thresholds{Warning: 1}
)

// Settings is how application databases are sampled and what raises an alert
// (#2409). A threshold of 0 is off.
type Settings struct {
	// Channels are the notification channels the alerts are sent to.
	Channels []string `json:"channels"`
	// IntervalSeconds is how often each application database is sampled.
	IntervalSeconds int `json:"interval_seconds"`
	HistoryDays     int `json:"history_days"`
	// LongRunningSeconds is how long a statement runs before its session is
	// listed as long-running.
	LongRunningSeconds int `json:"long_running_seconds"`
	// TransactionIDAge is the age of the oldest unfrozen transaction id.
	TransactionIDAge levelalert.Thresholds `json:"transaction_id_age"`
	// IdleInTransactionSeconds is how long the longest session has been idle
	// in a transaction.
	IdleInTransactionSeconds levelalert.Thresholds `json:"idle_in_transaction_seconds"`
	// ConnectionsPercent is the share of max_connections in use.
	ConnectionsPercent levelalert.Thresholds `json:"connections_percent"`
	InvalidIndexes     levelalert.Thresholds `json:"invalid_indexes"`
	// DeadTuplePercent is the highest dead tuple share of a table holding
	// 1,000 rows or more.
	DeadTuplePercent levelalert.Thresholds `json:"dead_tuple_percent"`
	// DriftObjects is how many objects differ from what the applied
	// migrations build.
	DriftObjects levelalert.Thresholds `json:"drift_objects"`
}

// Defaults is the configuration before anything is written.
func Defaults() Settings {
	return Settings{
		Channels: []string{}, IntervalSeconds: DefaultIntervalSeconds, HistoryDays: DefaultHistoryDays,
		LongRunningSeconds: DefaultLongRunningSeconds, TransactionIDAge: DefaultTransactionIDAge,
		IdleInTransactionSeconds: DefaultIdleInTransaction, ConnectionsPercent: DefaultConnectionsPercent,
		InvalidIndexes: DefaultInvalidIndexes, DeadTuplePercent: DefaultDeadTuplePercent, DriftObjects: DefaultDriftObjects,
	}
}

// normalized fills what a stored value leaves empty.
func (s Settings) normalized() Settings {
	d := Defaults()
	if s.Channels == nil {
		s.Channels = []string{}
	}
	if s.IntervalSeconds <= 0 {
		s.IntervalSeconds = d.IntervalSeconds
	}
	if s.HistoryDays <= 0 {
		s.HistoryDays = d.HistoryDays
	}
	if s.LongRunningSeconds <= 0 {
		s.LongRunningSeconds = d.LongRunningSeconds
	}
	return s
}

// Interval is how often a database is sampled.
func (s Settings) Interval() time.Duration { return time.Duration(s.IntervalSeconds) * time.Second }

// Validate refuses a setting the loop cannot run with.
func (s Settings) Validate() error {
	for _, c := range s.Channels {
		if strings.TrimSpace(c) == "" {
			return errors.New("a channel name is empty")
		}
	}
	switch {
	case s.IntervalSeconds != 0 && s.IntervalSeconds < MinIntervalSeconds:
		return fmt.Errorf("interval_seconds must be at least %d", MinIntervalSeconds)
	case s.HistoryDays < 0 || s.HistoryDays > MaxHistoryDays:
		return fmt.Errorf("history_days must be between 1 and %d", MaxHistoryDays)
	case s.LongRunningSeconds < 0:
		return errors.New("long_running_seconds cannot be negative")
	}
	return s.validThresholds()
}

// validThresholds refuses a negative threshold and a critical one below its
// warning.
func (s Settings) validThresholds() error {
	for name, t := range map[string]levelalert.Thresholds{
		"transaction_id_age": s.TransactionIDAge, "idle_in_transaction_seconds": s.IdleInTransactionSeconds,
		"connections_percent": s.ConnectionsPercent, "invalid_indexes": s.InvalidIndexes,
		"dead_tuple_percent": s.DeadTuplePercent, "drift_objects": s.DriftObjects,
	} {
		if t.Warning < 0 || t.Critical < 0 {
			return fmt.Errorf("the %s thresholds cannot be negative; 0 is off", name)
		}
		if t.Warning > 0 && t.Critical > 0 && t.Critical < t.Warning {
			return fmt.Errorf("the %s critical threshold must not be below its warning", name)
		}
	}
	return nil
}

// SettingsStore reads and writes the section.
type SettingsStore interface {
	Get(ctx context.Context) (settingsjson.Stored[Settings], error)
	Set(ctx context.Context, v Settings, author string) error
}

// NewSettingsStore returns the section over db.
func NewSettingsStore(db *sql.DB) SettingsStore {
	return settingsjson.New[Settings](db, SettingsSection)
}

// View is the read shape: the settings with who wrote them last.
type View struct {
	Settings
	UpdatedBy string `json:"updated_by,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Read returns the stored settings, or the defaults before any are written.
func Read(ctx context.Context, store SettingsStore) (View, error) {
	got, err := store.Get(ctx)
	if errors.Is(err, settingsjson.ErrNotFound) {
		return View{Settings: Defaults()}, nil
	}
	if err != nil {
		return View{}, fmt.Errorf("reading application database health settings: %w", err)
	}
	v := View{Settings: got.Value.normalized(), UpdatedBy: got.UpdatedBy}
	if !got.UpdatedAt.IsZero() {
		v.UpdatedAt = got.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return v, nil
}
