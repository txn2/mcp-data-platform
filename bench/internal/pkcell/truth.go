package pkcell

import (
	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/pkseed"
)

// Ground truths, computed from the same fixture data the service serves.
// A hand-typed expected value would be a second source of truth that could
// drift from the first; these cannot, because they read the fixture.

// mainProfileIndex is the owned profile the questions mean by "main". The
// fixture exposes no primary flag, so the intended reading is the one every
// capture episode reached independently: the earliest-connected profile
// whose name matches the company.
const mainProfileIndex = 0

// GroundTruth returns the numeric answer a question has in a world, and
// whether it has one at all. A question that is not answerable in the world
// has no ground truth, which is the same condition that makes refusing
// correct.
func (q Question) GroundTruth(w apigen.World) (float64, bool) {
	if !q.AnswerableIn(w) {
		return 0, false
	}
	f := apigen.BuildFixture()
	switch q.ID {
	case "trend-volume":
		return trendTotal(f, w, func(p apigen.TrendPoint) int64 { return p.Volume }, sum), true
	case "trend-sentiment":
		return trendTotal(f, w, func(p apigen.TrendPoint) int64 { return p.SentimentScore }, maxOf), true
	case "monitor-count":
		return float64(w.Monitors), true
	case "positive-coverage-days", "positive-coverage-days-directive":
		return float64(positiveCoverageDays(f)), true
	case "weekly-impressions":
		return float64(weekOneImpressions(f)), true
	case "unique-reach":
		return float64(periodUniqueReach(f)), true
	default:
		return 0, false
	}
}

// combine folds a running total with the next value. Both foldings the
// questions need (a sum across everything, a maximum across everything)
// have the same shape.
type combine func(acc, v int64) int64

func sum(acc, v int64) int64 { return acc + v }

func maxOf(acc, v int64) int64 { return max(acc, v) }

// trendTotal folds one field of the trend series across every provisioned
// monitor. It walks the same monitors the service would serve, so a world
// with three monitors is scored on three.
func trendTotal(f *apigen.Fixture, w apigen.World, field func(apigen.TrendPoint) int64, fold combine) float64 {
	var acc int64
	for i, m := range f.Monitors {
		if i >= w.Monitors {
			break
		}
		for _, p := range f.Trend[m.ID] {
			acc = fold(acc, field(p))
		}
	}
	return float64(acc)
}

// positiveCoverageDays counts the Brand mentions monitor's days at or
// above the reporting convention's threshold. The threshold constant is
// shared with the delivered belief text, so the convention the agent
// reads and the answer the grader expects cannot disagree.
func positiveCoverageDays(f *apigen.Fixture) int64 {
	return PositiveCoverageDaysAt(f, pkseed.PositiveCoverageThreshold)
}

// PositiveCoverageDaysAt counts the Brand mentions monitor's days at or
// above an arbitrary threshold. The threshold is a parameter for the
// knowledge-pollution study, which plants a WRONG one and needs the day
// count it implies computed from the same fixture the correct count is —
// a second implementation of the count could drift and would silently
// mislabel which threshold an answer came from.
func PositiveCoverageDaysAt(f *apigen.Fixture, threshold int64) int64 {
	var days int64
	for _, p := range f.Trend[f.Monitors[0].ID] {
		if p.SentimentScore >= threshold {
			days++
		}
	}
	return days
}

// weekOneImpressions totals the main profile's impressions over the first
// seven days of the series, which is the window the durable question asks
// about. The total is the same under both contract versions: the release
// changes how the figure can be requested, not what it is.
func weekOneImpressions(f *apigen.Fixture) int64 {
	series := f.Metrics[f.Profiles[mainProfileIndex].ID]
	var total int64
	for i, p := range series {
		if i >= apigen.WeekBucketDays {
			break
		}
		total += p.Impressions
	}
	return total
}

// periodUniqueReach is the main profile's deduplicated reach over the whole
// series: the answer the aggregate endpoint gives, and the one summing
// daily uniques does not.
func periodUniqueReach(f *apigen.Fixture) int64 {
	series := f.Metrics[f.Profiles[mainProfileIndex].ID]
	daily := make([]int64, 0, len(series))
	for _, p := range series {
		daily = append(daily, p.UniqueReach)
	}
	return apigen.DedupUnique(daily)
}

// SummedDailyUniqueReach is the wrong answer the eternal question invites:
// the sum of daily uniques, which double-counts every account seen on more
// than one day. Exported so an analysis can tell an agent that fell into
// the trap from one that was merely wrong.
func SummedDailyUniqueReach() float64 {
	f := apigen.BuildFixture()
	var total int64
	for _, p := range f.Metrics[f.Profiles[mainProfileIndex].ID] {
		total += p.UniqueReach
	}
	return float64(total)
}
