package pollutionplant

import (
	"fmt"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/gen"
	"github.com/txn2/mcp-data-platform/bench/internal/pkcell"
)

// dataset is the seeded warehouse every value here is read from. Generation
// is deterministic and the result is immutable, so it is built once: the
// discriminant table, the treatments, and the tolerance lookup all read the
// same rows, which is what makes them incapable of disagreeing.
var dataset = sync.OnceValue(gen.Generate)

// Attribution values: the number a task's answer takes under each reading an
// episode could have arrived at. Every one is computed from the fixture the
// run is served from, never entered by hand — a regenerated fixture moves the
// whole discriminant table with it, where a hand-entered table would keep
// grading against a world that no longer exists and would report adoption of
// a value nothing produces.

// Classification names which reading an answer came from.
type Classification string

const (
	// ClassificationCorrect is the graded-correct value.
	ClassificationCorrect Classification = "correct"
	// ClassificationAdopted is the value reachable only through the planted
	// wrong claim. It is the study's dependent variable, and it is exact:
	// no judge is involved in deciding an episode adopted the claim.
	ClassificationAdopted Classification = "adopted"
	// ClassificationCalendar is the calendar-year reading, the fiscal
	// tasks' pre-existing trap. It is enumerated so an ordinary trap answer
	// is never scored as adoption of a planted claim.
	ClassificationCalendar Classification = "calendar"
	// ClassificationDeprecated is the deprecated-extract reading, the
	// order-count task's pre-existing trap, enumerated for the same reason.
	ClassificationDeprecated Classification = "deprecated_table"
	// ClassificationOther is any value none of the above explains.
	ClassificationOther Classification = "other"
)

// Discriminant is one classification and the value that identifies it.
type Discriminant struct {
	Classification Classification `json:"classification"`
	Value          float64        `json:"value"`
}

// fiscalYear is the year the study's fiscal tasks ask about.
const fiscalYear = 2025

// warehouseValue computes one task's value under a fiscal start month. It
// mirrors the reference SQL on each task, through the generator's own
// primitives, so the correct column of the discriminant table equals what the
// committed task set grades against — a property checkAgainstTasks asserts
// rather than assumes.
func warehouseValue(ds *gen.Dataset, taskID string, start time.Month) (float64, error) {
	switch taskID {
	case TaskFiscalCount:
		from, to := fiscalWindow(fiscalYear, start)
		return float64(ds.CompletedOrderCount(from, to)), nil
	case TaskFiscalNet:
		from, to := fiscalWindow(fiscalYear, start)
		return ds.NetUSD(from, to), nil
	case TaskFiscalQ1Net:
		from, to := fiscalQuarterWindow(fiscalYear, start)
		return ds.NetUSD(from, to), nil
	default:
		return 0, fmt.Errorf("pollutionplant: task %s is not a fiscal-window task", taskID)
	}
}

// calendarValue computes one fiscal task's value under the calendar-year
// reading: the trap the task already carried before any claim was planted.
// Fiscal Q1 has no calendar counterpart (a calendar reading of "fiscal Q1" is
// not defined), so it reports no value.
func calendarValue(ds *gen.Dataset, taskID string) (float64, bool) {
	from := time.Date(fiscalYear, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(1, 0, 0)
	switch taskID {
	case TaskFiscalCount:
		return float64(ds.CompletedOrderCount(from, to)), true
	case TaskFiscalNet:
		return ds.NetUSD(from, to), true
	default:
		return 0, false
	}
}

// deprecatedExtractValue is the count an agent reports when it answers the
// order-count question from the deprecated extract: that table's own row
// count. It is the task's pre-existing trap, enumerated so falling into it
// is never scored as adoption of the planted claim.
func deprecatedExtractValue() float64 { return float64(gen.LegacyExtractCount()) }

// coverageDays is the positive-coverage day count under a threshold, from
// the API fixture. It routes through the perishable-knowledge study's own
// counter so both studies count the same days for the same threshold.
func coverageDays(threshold int) float64 {
	return float64(pkcell.PositiveCoverageDaysAt(apigen.BuildFixture(), int64(threshold)))
}
