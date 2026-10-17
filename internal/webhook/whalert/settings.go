// Package whalert routes the webhook receiver's conditions that end in lost or
// late data to notification channels, at a warning and a critical level
// (#2149): the spool filling or holding events too long, the drain failing, a
// replica holding events that stopped reporting, refusals, unauthorized
// requests, slow acknowledgements, compaction failing or behind, a source gone
// silent, and the spool volume running out of space. A sender does not wait,
// so these are told to someone as they happen rather than found on the page.
package whalert

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyalert"
	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
)

// SettingsSection is the platform_settings section holding the settings. It
// is a stored key.
const SettingsSection = "webhook_alert"

// The defaults a deployment starts with, before an administrator writes any.
const (
	// defaultSpoolFillWarning and defaultSpoolFillCritical are the spool's
	// fill, in percent of max_bytes.
	defaultSpoolFillWarning  = 50
	defaultSpoolFillCritical = 80
	// defaultSpoolAgeWarning and defaultSpoolAgeCritical are the oldest
	// event's age, in percent of max_age.
	defaultSpoolAgeWarning  = 25
	defaultSpoolAgeCritical = 60
	// defaultDrainFailuresWarning and defaultDrainFailuresCritical are drains
	// failed in a row.
	defaultDrainFailuresWarning  = 3
	defaultDrainFailuresCritical = 10
	// defaultDrainFailingMinutes is how long a failure streak lasts before it
	// is critical.
	defaultDrainFailingMinutes = 5
	// defaultNotReportingSeconds is how long a replica holding events may go
	// without reporting.
	defaultNotReportingSeconds = 120
	// defaultVolumeFreeWarning and defaultVolumeFreeCritical are the spool
	// volume's free space, in percent.
	defaultVolumeFreeWarning  = 25
	defaultVolumeFreeCritical = 10
	// defaultRefusalMinutes is the span refusals are judged over.
	defaultRefusalMinutes = 5
	// defaultUnauthorizedWarning is the unauthorized share, in percent.
	defaultUnauthorizedWarning = 10
	// defaultAckP99Warning and defaultAckP99Critical are the acknowledgement
	// p99, in milliseconds.
	defaultAckP99Warning  = 1000
	defaultAckP99Critical = 5000
	// defaultCompactionOverdueWarning and defaultCompactionOverdueCritical are
	// the minutes the oldest owed window has waited past its end and grace.
	defaultCompactionOverdueWarning  = 1
	defaultCompactionOverdueCritical = 30
	// maxRefusalMinutes is the longest span refusals may be judged over.
	maxRefusalMinutes = 60
	// noLimit is the bound of a threshold that has no upper limit.
	noLimit = 0
)

// Settings is the configuration: the channels, and each condition's
// thresholds. A zero threshold is off.
type Settings struct {
	Channels []string `json:"channels"`
	// SpoolFillPercent is a replica's spool bytes as a percent of its
	// max_bytes.
	SpoolFillPercent levelalert.Thresholds `json:"spool_fill_percent"`
	// SpoolAgePercent is the oldest event waiting as a percent of max_age.
	SpoolAgePercent levelalert.Thresholds `json:"spool_age_percent"`
	// DrainFailures is how many drains in a row have failed; a streak
	// longer than DrainFailingMinutes is critical however many it counts.
	DrainFailures       levelalert.Thresholds `json:"drain_failures"`
	DrainFailingMinutes float64               `json:"drain_failing_minutes"`
	// NotReportingSeconds is how long a replica whose spool holds events
	// may go without reporting before it is critical.
	NotReportingSeconds float64 `json:"not_reporting_seconds"`
	// SpoolVolumeFreePercent is the free space left on the volume a spool
	// is on.
	SpoolVolumeFreePercent levelalert.Thresholds `json:"spool_volume_free_percent"`
	// Sources holds the per-source conditions: the defaults every source is
	// judged by, and any source's own.
	Defaults SourceRules            `json:"defaults"`
	Sources  map[string]SourceRules `json:"sources"`
}

// SourceRules are the conditions judged per source.
type SourceRules struct {
	// RefusalMinutes is the span refusals (spool_full, write_failed,
	// buffer_full) are judged over: any in it is a warning, one in every
	// minute of it is critical. Zero is off.
	RefusalMinutes *int `json:"refusal_minutes,omitempty"`
	// UnauthorizedPercent is the share of the source's requests over the
	// last 15 minutes that were refused as unauthorized.
	UnauthorizedPercent *levelalert.Thresholds `json:"unauthorized_percent,omitempty"`
	// AckP99MS is the 99th percentile acknowledgement over the last five
	// minutes, in milliseconds.
	AckP99MS *levelalert.Thresholds `json:"ack_p99_ms,omitempty"`
	// CompactionOverdueMinutes is how long past its end and the compactor's
	// grace the oldest window owed a compaction has waited.
	CompactionOverdueMinutes *levelalert.Thresholds `json:"compaction_overdue_minutes,omitempty"`
	// SilentAfterMinutes, when set, warns once the source has written no
	// segment for that long. Off unless a source opts in.
	SilentAfterMinutes *int `json:"silent_after_minutes,omitempty"`
}

// Defaults is the configuration before an administrator writes any.
func Defaults() Settings {
	refusal, silent := defaultRefusalMinutes, 0
	return Settings{
		Channels:               []string{},
		SpoolFillPercent:       levelalert.Thresholds{Warning: defaultSpoolFillWarning, Critical: defaultSpoolFillCritical},
		SpoolAgePercent:        levelalert.Thresholds{Warning: defaultSpoolAgeWarning, Critical: defaultSpoolAgeCritical},
		DrainFailures:          levelalert.Thresholds{Warning: defaultDrainFailuresWarning, Critical: defaultDrainFailuresCritical},
		DrainFailingMinutes:    defaultDrainFailingMinutes,
		NotReportingSeconds:    defaultNotReportingSeconds,
		SpoolVolumeFreePercent: levelalert.Thresholds{Warning: defaultVolumeFreeWarning, Critical: defaultVolumeFreeCritical, Below: true},
		Defaults: SourceRules{
			RefusalMinutes:           &refusal,
			UnauthorizedPercent:      &levelalert.Thresholds{Warning: defaultUnauthorizedWarning},
			AckP99MS:                 &levelalert.Thresholds{Warning: defaultAckP99Warning, Critical: defaultAckP99Critical},
			CompactionOverdueMinutes: &levelalert.Thresholds{Warning: defaultCompactionOverdueWarning, Critical: defaultCompactionOverdueCritical},
			SilentAfterMinutes:       &silent,
		},
		Sources: map[string]SourceRules{},
	}
}

// normalized fills what a stored value leaves out from the defaults and marks
// the free-space thresholds lower-is-worse.
func (s Settings) normalized() Settings {
	d := Defaults()
	if s.Channels == nil {
		s.Channels = []string{}
	}
	if s.Sources == nil {
		s.Sources = map[string]SourceRules{}
	}
	s.Defaults = s.Defaults.over(d.Defaults)
	s.SpoolVolumeFreePercent.Below = true
	return s
}

// over is r with each rule it leaves unset taken from base.
func (r SourceRules) over(base SourceRules) SourceRules {
	if r.RefusalMinutes == nil {
		r.RefusalMinutes = base.RefusalMinutes
	}
	if r.UnauthorizedPercent == nil {
		r.UnauthorizedPercent = base.UnauthorizedPercent
	}
	if r.AckP99MS == nil {
		r.AckP99MS = base.AckP99MS
	}
	if r.CompactionOverdueMinutes == nil {
		r.CompactionOverdueMinutes = base.CompactionOverdueMinutes
	}
	if r.SilentAfterMinutes == nil {
		r.SilentAfterMinutes = base.SilentAfterMinutes
	}
	return r
}

// RulesFor is the rules source is judged by: its own over the defaults.
func (s Settings) RulesFor(source string) SourceRules {
	n := s.normalized()
	return n.Sources[source].over(n.Defaults)
}

// Input is the write shape. An absent field keeps the stored value; Sources,
// when present, replaces every source's rules.
type Input struct {
	Channels               *[]string               `json:"channels,omitempty"`
	SpoolFillPercent       *levelalert.Thresholds  `json:"spool_fill_percent,omitempty"`
	SpoolAgePercent        *levelalert.Thresholds  `json:"spool_age_percent,omitempty"`
	DrainFailures          *levelalert.Thresholds  `json:"drain_failures,omitempty"`
	DrainFailingMinutes    *float64                `json:"drain_failing_minutes,omitempty"`
	NotReportingSeconds    *float64                `json:"not_reporting_seconds,omitempty"`
	SpoolVolumeFreePercent *levelalert.Thresholds  `json:"spool_volume_free_percent,omitempty"`
	Defaults               *SourceRules            `json:"defaults,omitempty"`
	Sources                *map[string]SourceRules `json:"sources,omitempty"`
}

// Apply returns stored with in written over it, or why in is invalid.
func (in Input) Apply(stored Settings) (Settings, error) {
	out := stored.normalized()
	if in.Channels != nil {
		names := notifyalert.Normalize(*in.Channels)
		if len(names) > notifyalert.MaxChannels {
			return out, fmt.Errorf("at most %d channels may be configured", notifyalert.MaxChannels)
		}
		out.Channels = names
	}
	if err := applyThresholds(in, &out); err != nil {
		return out, err
	}
	if err := applyDurations(in, &out); err != nil {
		return out, err
	}
	if in.Defaults != nil {
		if err := in.Defaults.validate("defaults"); err != nil {
			return out, err
		}
		out.Defaults = *in.Defaults
	}
	if in.Sources != nil {
		sources, err := validSources(*in.Sources)
		if err != nil {
			return out, err
		}
		out.Sources = sources
	}
	return out.normalized(), nil
}

// applyThresholds writes in's replica thresholds over out, refusing any that
// cannot be judged.
func applyThresholds(in Input, out *Settings) error {
	checks := []struct {
		name string
		in   *levelalert.Thresholds
		dst  *levelalert.Thresholds
		max  float64
		down bool
	}{
		{"spool_fill_percent", in.SpoolFillPercent, &out.SpoolFillPercent, percent, false},
		{"spool_age_percent", in.SpoolAgePercent, &out.SpoolAgePercent, percent, false},
		{"drain_failures", in.DrainFailures, &out.DrainFailures, noLimit, false},
		{"spool_volume_free_percent", in.SpoolVolumeFreePercent, &out.SpoolVolumeFreePercent, percent, true},
	}
	for _, c := range checks {
		if c.in == nil {
			continue
		}
		if err := validThresholds(c.name, *c.in, c.max, c.down); err != nil {
			return err
		}
		*c.dst = *c.in
	}
	return nil
}

// validSources is the per-source rules with their names trimmed, or why one
// cannot be judged.
func validSources(in map[string]SourceRules) (map[string]SourceRules, error) {
	sources := make(map[string]SourceRules, len(in))
	for name, r := range in {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, errors.New("a source's rules name no source")
		}
		if err := r.validate("sources." + name); err != nil {
			return nil, err
		}
		sources[name] = r
	}
	return sources, nil
}

// applyDurations writes in's durations over out, refusing a negative one.
func applyDurations(in Input, out *Settings) error {
	if in.DrainFailingMinutes != nil {
		if *in.DrainFailingMinutes < 0 {
			return errors.New("drain_failing_minutes cannot be negative (0 is off)")
		}
		out.DrainFailingMinutes = *in.DrainFailingMinutes
	}
	if in.NotReportingSeconds != nil {
		if *in.NotReportingSeconds < 0 {
			return errors.New("not_reporting_seconds cannot be negative (0 is off)")
		}
		out.NotReportingSeconds = *in.NotReportingSeconds
	}
	return nil
}

// validate refuses a source rule set that cannot be judged.
func (r SourceRules) validate(where string) error {
	if r.RefusalMinutes != nil && (*r.RefusalMinutes < 0 || *r.RefusalMinutes > maxRefusalMinutes) {
		return fmt.Errorf("invalid %s.refusal_minutes: must be 0 to %d (0 is off)", where, maxRefusalMinutes)
	}
	if r.SilentAfterMinutes != nil && *r.SilentAfterMinutes < 0 {
		return fmt.Errorf("invalid %s.silent_after_minutes: cannot be negative (0 is off)", where)
	}
	for name, t := range map[string]*levelalert.Thresholds{
		"unauthorized_percent": r.UnauthorizedPercent, "ack_p99_ms": r.AckP99MS, "compaction_overdue_minutes": r.CompactionOverdueMinutes,
	} {
		if t == nil {
			continue
		}
		limit := float64(noLimit)
		if name == "unauthorized_percent" {
			limit = percent
		}
		if err := validThresholds(where+"."+name, *t, limit, false); err != nil {
			return err
		}
	}
	return nil
}

// validThresholds refuses a negative threshold, one over limit (when limit is
// set), and a warning that is not short of its critical.
func validThresholds(name string, t levelalert.Thresholds, limit float64, down bool) error {
	if err := inRange(name, t, limit); err != nil {
		return err
	}
	if t.Warning == 0 || t.Critical == 0 {
		return nil
	}
	if !down && t.Warning >= t.Critical {
		return fmt.Errorf("the %s warning must be below its critical", name)
	}
	if down && t.Warning <= t.Critical {
		return fmt.Errorf("the %s warning must be above its critical", name)
	}
	return nil
}

// inRange refuses a negative threshold, and one over limit when limit is set.
func inRange(name string, t levelalert.Thresholds, limit float64) error {
	for _, v := range []float64{t.Warning, t.Critical} {
		if v < 0 || (limit > 0 && v > limit) {
			if limit > 0 {
				return fmt.Errorf("invalid %s thresholds: each is 0 to %.0f (0 is off)", name, limit)
			}
			return fmt.Errorf("invalid %s thresholds: none may be negative (0 is off)", name)
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

// View is the read shape.
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
		return View{}, fmt.Errorf("reading webhook alert settings: %w", err)
	}
	v := View{Settings: got.Value.normalized(), UpdatedBy: got.UpdatedBy}
	if !got.UpdatedAt.IsZero() {
		v.UpdatedAt = got.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return v, nil
}
