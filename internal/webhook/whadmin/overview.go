package whadmin

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// Health is a source's overall state on the overview of every source (#1979).
type Health string

// The states a source can be in. When more than one applies, the first in
// this order is reported: Disabled, Failing, Silent, Receiving. A disabled
// source receives nothing by choice, so neither its silence nor its windows
// are news; a failing source is losing data it did receive, which outranks
// silence; a source that is neither is receiving.
const (
	// HealthDisabled is a source an administrator turned off.
	HealthDisabled Health = "disabled"
	// HealthFailing is a source with one or more windows whose last
	// compaction failed.
	HealthFailing Health = "failing"
	// HealthSilent is an enabled source that received no event in the last
	// SilentAfter, including one that never received any. A sender that
	// stops produces no error on the receiving side, so this is how it shows.
	HealthSilent Health = "silent"
	// HealthReceiving is an enabled source that received an event within
	// SilentAfter and has no failing window.
	HealthReceiving Health = "receiving"
)

// SilentAfter is how long an enabled source may go without an event before
// it is reported silent.
const SilentAfter = 24 * time.Hour

// HealthOf is a source's health at now, from its summary.
func HealthOf(src whsource.Source, sum whstore.Summary, now time.Time) Health {
	switch {
	case !src.Enabled:
		return HealthDisabled
	case sum.Failing > 0:
		return HealthFailing
	case sum.LastSegmentAt == nil || now.Sub(*sum.LastSegmentAt) > SilentAfter:
		return HealthSilent
	default:
		return HealthReceiving
	}
}

// SourceOverview is one source on the overview: its settings, its health and
// its summary.
type SourceOverview struct {
	Source  whsource.Source
	Health  Health
	Summary whstore.Summary
}

// Overview is the status of every source at Now, the request series from
// Since in buckets of Step, and the newest rejections of every source.
type Overview struct {
	Now        time.Time
	Since      time.Time
	Step       time.Duration
	Sources    []SourceOverview
	Volume     []whstore.VolumePoint
	Rejections []whstore.Rejection
}

// Overview reads every source's status in one pass: the sources, ordered by
// name, each with its health; the request series over span before now in
// buckets of step; and the newest rejections across all of them.
func (s *Service) Overview(ctx context.Context, span, step time.Duration) (Overview, error) {
	list, err := s.List(ctx)
	if err != nil {
		return Overview{}, err
	}
	now := s.deps.Now().UTC()
	ov, err := s.deps.Windows.Overview(ctx, now, span, step)
	if err != nil {
		return Overview{}, fmt.Errorf("reading webhook overview: %w", err)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	out := Overview{
		Now: now, Since: ov.Since, Step: step,
		Sources:    make([]SourceOverview, 0, len(list)),
		Volume:     nonNil(ov.Volume),
		Rejections: nonNil(ov.Rejections),
	}
	for _, src := range list {
		sum, ok := ov.Summaries[src.Name]
		if !ok {
			sum = whstore.Summary{LastHour: map[string]int64{}, LastDay: map[string]int64{}}
		}
		out.Sources = append(out.Sources, SourceOverview{Source: src, Health: HealthOf(src, sum, now), Summary: sum})
	}
	return out, nil
}

// nonNil returns list, or an empty list in place of nil, so a response
// renders it as [].
func nonNil[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}
