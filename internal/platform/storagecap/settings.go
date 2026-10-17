// Package storagecap is the storage capacity an administrator reads and is
// alerted on (#2148): how much of each store is used and how much is free,
// the database against its limit, the object store cluster's disks, a bucket
// against its budget and each replica's webhook spool volume; the history of
// each, how fast it is growing, when it would fill at that rate, and leveled
// alerts on both. #1899 measured usage as metrics; this makes it a page and a
// notification, with capacity read from the stores where they say it.
package storagecap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyalert"
	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
)

// SettingsSection is the platform_settings section holding the settings. It
// is a stored key.
const SettingsSection = "storage"

// The defaults a deployment starts with before an administrator writes the
// section.
const (
	DefaultHistoryDays = 180
	maxHistoryDays     = 3650
	// maxPercent is the highest a used_percent threshold may be.
	maxPercent = 100
)

// DefaultUsedPercent and DefaultDaysLeft are the alert thresholds before an
// administrator sets any: percent of a store used, and days until it fills at
// its growth rate.
var (
	DefaultUsedPercent = levelalert.Thresholds{Warning: 75, Critical: 90}
	DefaultDaysLeft    = levelalert.Thresholds{Warning: 30, Critical: 7, Below: true}
)

// SeaweedFS names the cluster the platform reads disk capacity from. The S3
// gateway does not report it; the master lists the volume servers and each
// reports its disks.
type SeaweedFS struct {
	// MasterURL is the master's HTTP address, e.g. http://seaweedfs-master:9333.
	MasterURL string `json:"master_url"`
	// VolumeURLs, when set, are the volume servers read instead of the ones
	// the master lists: a master inside a cluster lists addresses the
	// platform may not reach.
	VolumeURLs []string `json:"volume_urls"`
}

// Settings is the stored configuration.
type Settings struct {
	Channels []string `json:"channels"`
	// DatabaseLimitBytes is the size the database may reach: its volume, or
	// the plan's quota. The platform cannot read the database server's disk,
	// so it is the operator's. Zero is unknown.
	DatabaseLimitBytes int64 `json:"database_limit_bytes"`
	// Budgets is the byte budget per bucket, for a store the platform cannot
	// read capacity from (cloud S3). It overrides MCP_PLATFORM_STORAGE_BUDGETS
	// for every bucket it names.
	Budgets     map[string]int64      `json:"budgets"`
	SeaweedFS   SeaweedFS             `json:"seaweedfs"`
	HistoryDays int                   `json:"history_days"`
	UsedPercent levelalert.Thresholds `json:"used_percent"`
	DaysLeft    levelalert.Thresholds `json:"days_left"`
}

// Defaults is the configuration before anything is written.
func Defaults() Settings {
	return Settings{
		Channels: []string{}, Budgets: map[string]int64{}, HistoryDays: DefaultHistoryDays,
		UsedPercent: DefaultUsedPercent, DaysLeft: DefaultDaysLeft,
	}
}

// normalized fills what a stored value leaves empty and marks the days-left
// thresholds as lower-is-worse, which JSON does not carry.
func (s Settings) normalized() Settings {
	if s.Channels == nil {
		s.Channels = []string{}
	}
	if s.Budgets == nil {
		s.Budgets = map[string]int64{}
	}
	if s.SeaweedFS.VolumeURLs == nil {
		s.SeaweedFS.VolumeURLs = []string{}
	}
	if s.HistoryDays <= 0 {
		s.HistoryDays = DefaultHistoryDays
	}
	s.DaysLeft.Below = true
	return s
}

// Input is the write shape. An absent field keeps the stored value.
type Input struct {
	Channels           *[]string              `json:"channels,omitempty"`
	DatabaseLimitBytes *int64                 `json:"database_limit_bytes,omitempty"`
	Budgets            *map[string]int64      `json:"budgets,omitempty"`
	SeaweedFS          *SeaweedFS             `json:"seaweedfs,omitempty"`
	HistoryDays        *int                   `json:"history_days,omitempty"`
	UsedPercent        *levelalert.Thresholds `json:"used_percent,omitempty"`
	DaysLeft           *levelalert.Thresholds `json:"days_left,omitempty"`
}

// Apply returns stored with in written over it, or the first reason in is
// invalid. Whether each channel exists is checked by the route.
func (in Input) Apply(stored Settings) (Settings, error) {
	out := stored.normalized()
	if in.Channels != nil {
		names := notifyalert.Normalize(*in.Channels)
		if len(names) > notifyalert.MaxChannels {
			return out, fmt.Errorf("at most %d channels may be configured", notifyalert.MaxChannels)
		}
		out.Channels = names
	}
	if err := in.applyStores(&out); err != nil {
		return out, err
	}
	if err := in.applyAlerting(&out); err != nil {
		return out, err
	}
	return out.normalized(), nil
}

// applyStores writes in's database limit, bucket budgets and cluster over
// out, refusing any that is invalid.
func (in Input) applyStores(out *Settings) error {
	if in.DatabaseLimitBytes != nil {
		if *in.DatabaseLimitBytes < 0 {
			return errors.New("database_limit_bytes cannot be negative")
		}
		out.DatabaseLimitBytes = *in.DatabaseLimitBytes
	}
	if in.Budgets != nil {
		budgets, err := validBudgets(*in.Budgets)
		if err != nil {
			return err
		}
		out.Budgets = budgets
	}
	if in.SeaweedFS != nil {
		sw, err := validSeaweedFS(*in.SeaweedFS)
		if err != nil {
			return err
		}
		out.SeaweedFS = sw
	}
	return nil
}

// applyAlerting writes in's history length and thresholds over out, refusing
// any that is invalid.
func (in Input) applyAlerting(out *Settings) error {
	if in.HistoryDays != nil {
		if *in.HistoryDays < 1 || *in.HistoryDays > maxHistoryDays {
			return fmt.Errorf("history_days must be between 1 and %d", maxHistoryDays)
		}
		out.HistoryDays = *in.HistoryDays
	}
	if in.UsedPercent != nil {
		if err := validPercent(*in.UsedPercent); err != nil {
			return err
		}
		out.UsedPercent = *in.UsedPercent
	}
	if in.DaysLeft != nil {
		if err := validDaysLeft(*in.DaysLeft); err != nil {
			return err
		}
		out.DaysLeft = *in.DaysLeft
	}
	return nil
}

func validBudgets(in map[string]int64) (map[string]int64, error) {
	out := make(map[string]int64, len(in))
	for bucket, size := range in {
		name := strings.TrimSpace(bucket)
		if name == "" {
			return nil, errors.New("a budget names no bucket")
		}
		if size <= 0 {
			return nil, fmt.Errorf("the budget for bucket %q must be a positive number of bytes", name)
		}
		out[name] = size
	}
	return out, nil
}

func validSeaweedFS(in SeaweedFS) (SeaweedFS, error) {
	out := SeaweedFS{MasterURL: strings.TrimRight(strings.TrimSpace(in.MasterURL), "/"), VolumeURLs: []string{}}
	if out.MasterURL != "" {
		if err := httpURL(out.MasterURL); err != nil {
			return out, fmt.Errorf("seaweedfs.master_url: %w", err)
		}
	}
	for _, v := range in.VolumeURLs {
		v = strings.TrimRight(strings.TrimSpace(v), "/")
		if v == "" {
			continue
		}
		if err := httpURL(v); err != nil {
			return out, fmt.Errorf("seaweedfs.volume_urls: %w", err)
		}
		out.VolumeURLs = append(out.VolumeURLs, v)
	}
	if out.MasterURL == "" && len(out.VolumeURLs) > 0 {
		return out, errors.New("seaweedfs.volume_urls needs seaweedfs.master_url")
	}
	sort.Strings(out.VolumeURLs)
	return out, nil
}

// httpURL refuses anything but an absolute http or https address.
func httpURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("not an http or https address: %q", raw)
	}
	return nil
}

func validPercent(t levelalert.Thresholds) error {
	for _, v := range []float64{t.Warning, t.Critical} {
		if v < 0 || v > maxPercent {
			return errors.New("used_percent thresholds are percentages between 0 and 100 (0 is off)")
		}
	}
	if t.Warning > 0 && t.Critical > 0 && t.Warning >= t.Critical {
		return errors.New("the used_percent warning must be below its critical")
	}
	return nil
}

func validDaysLeft(t levelalert.Thresholds) error {
	if t.Warning < 0 || t.Critical < 0 {
		return errors.New("days_left thresholds cannot be negative (0 is off)")
	}
	if t.Warning > 0 && t.Critical > 0 && t.Warning <= t.Critical {
		return errors.New("the days_left warning must be more days than its critical")
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
		return View{}, fmt.Errorf("reading storage settings: %w", err)
	}
	v := View{Settings: got.Value.normalized(), UpdatedBy: got.UpdatedBy}
	if !got.UpdatedAt.IsZero() {
		v.UpdatedAt = got.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return v, nil
}

// BudgetsFrom reads the bucket budgets the settings hold, for the capacity
// gauges (#1899) to report them over the environment's.
func BudgetsFrom(store SettingsStore) func(ctx context.Context) (map[string]int64, error) {
	return func(ctx context.Context) (map[string]int64, error) {
		v, err := Read(ctx, store)
		if err != nil {
			return nil, err
		}
		return v.Budgets, nil
	}
}
