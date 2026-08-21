package script

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIndexTextComposesTheDescriptionCard pins the document a script is
// embedded on and shown as. The indexjobs consumer and the discovery source
// both read this one function, so a change here moves both together or neither.
func TestIndexTextComposesTheDescriptionCard(t *testing.T) {
	sc := &Script{
		Name: "daily-sales", DisplayName: "Daily Sales Report",
		Description: "Summarize yesterday's sales by region",
		Params: []Param{
			{Name: "report_date", Required: true},
			{Name: "region"},
		},
		Tags:    []string{"revenue", "scheduled"},
		Source:  "trino.query('SELECT * FROM sales.orders')",
		Enabled: true, Status: StatusActive,
	}

	got := IndexText(sc)

	assert.Equal(t, "Daily Sales Report\n"+
		"Summarize yesterday's sales by region\n"+
		"parameters: report_date (required), region\n"+
		"revenue scheduled\n"+
		"Call run_script to execute it.", got)
	assert.NotContains(t, got, "sales.orders",
		"the source is never part of the document: it is admitted to a narrower audience than the contract")
}

// TestIndexTextSkipsEmptyParts keeps a sparse script from padding its document
// with blank lines, which would dilute both the vector and the snippet.
func TestIndexTextSkipsEmptyParts(t *testing.T) {
	got := IndexText(&Script{Name: "daily-sales", Enabled: true, Status: StatusActive})

	assert.Equal(t, "daily-sales\nCall run_script to execute it.", got)
}

// TestIndexTextTracksTheRunGate is why the store re-hashes on a lifecycle
// change: taking a script out of service rewrites the last line, so the vector
// built before no longer describes the script.
func TestIndexTextTracksTheRunGate(t *testing.T) {
	sc := &Script{Name: "daily-sales", Description: "Summarize sales", Enabled: true, Status: StatusActive}
	before := IndexText(sc)

	sc.Status = StatusDeprecated

	assert.NotEqual(t, before, IndexText(sc))
}

// TestTitleFallsBackToTheCallableName keeps a script with no display name
// findable by the identifier an agent actually calls it by.
func TestTitleFallsBackToTheCallableName(t *testing.T) {
	assert.Equal(t, "Daily Sales", Title(&Script{Name: "daily-sales", DisplayName: "Daily Sales"}))
	assert.Equal(t, "daily-sales", Title(&Script{Name: "daily-sales"}))
}

// TestExecutionNoteStatesWhatTheHitIsFor: an in-service script is something to
// run and a retired one is not, and a reader must not have to guess which.
func TestExecutionNoteStatesWhatTheHitIsFor(t *testing.T) {
	assert.Contains(t, ExecutionNote(&Script{Enabled: true, Status: StatusActive}), "run_script")
	assert.Contains(t, ExecutionNote(&Script{Enabled: false, Status: StatusActive}), "Nothing will execute this script")
}

// TestEffectiveLimitClampsIntoRange bounds one ranked query, so a caller that
// forgets a limit and one that asks for the whole table get the same answer.
func TestEffectiveLimitClampsIntoRange(t *testing.T) {
	assert.Equal(t, DefaultSearchLimit, SearchQuery{}.EffectiveLimit())
	assert.Equal(t, DefaultSearchLimit, SearchQuery{Limit: -1}.EffectiveLimit())
	assert.Equal(t, DefaultSearchLimit, SearchQuery{Limit: maxSearchLimit + 1}.EffectiveLimit())
	assert.Equal(t, 5, SearchQuery{Limit: 5}.EffectiveLimit())
	assert.Equal(t, maxSearchLimit, SearchQuery{Limit: maxSearchLimit}.EffectiveLimit())
}
