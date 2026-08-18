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
		Tags:              []string{"revenue", "scheduled"},
		Source:            "trino.query('SELECT * FROM sales.orders')",
		ApprovedVersionID: "sver_3",
	}

	got := IndexText(sc)

	assert.Equal(t, "Daily Sales Report\n"+
		"Summarize yesterday's sales by region\n"+
		"parameters: report_date (required), region\n"+
		"revenue scheduled\n"+
		"An approved version exists; call run_script to execute it.", got)
	assert.NotContains(t, got, "sales.orders",
		"the source is never part of the document: it is admitted to a narrower audience than the contract")
}

// TestIndexTextSkipsEmptyParts keeps a sparse script from padding its document
// with blank lines, which would dilute both the vector and the snippet.
func TestIndexTextSkipsEmptyParts(t *testing.T) {
	got := IndexText(&Script{Name: "daily-sales"})

	assert.Equal(t, "daily-sales\nNo version of this script is approved, so nothing will execute it.", got)
}

// TestIndexTextTracksTheExecutionPointer is why the store re-hashes on
// approval: making a script executable rewrites the last line, so the vector
// built before approval no longer describes the script.
func TestIndexTextTracksTheExecutionPointer(t *testing.T) {
	sc := &Script{Name: "daily-sales", Description: "Summarize sales"}
	before := IndexText(sc)

	sc.ApprovedVersionID = "sver_1"

	assert.NotEqual(t, before, IndexText(sc))
}

// TestTitleFallsBackToTheCallableName keeps a script with no display name
// findable by the identifier an agent actually calls it by.
func TestTitleFallsBackToTheCallableName(t *testing.T) {
	assert.Equal(t, "Daily Sales", Title(&Script{Name: "daily-sales", DisplayName: "Daily Sales"}))
	assert.Equal(t, "daily-sales", Title(&Script{Name: "daily-sales"}))
}

// TestExecutionNoteStatesWhatTheHitIsFor: an approved script is something to
// run and an unapproved one is something to ask a reviewer about, and a reader
// must not have to guess which.
func TestExecutionNoteStatesWhatTheHitIsFor(t *testing.T) {
	assert.Contains(t, ExecutionNote(&Script{ApprovedVersionID: "sver_1"}), "call run_script")
	assert.Contains(t, ExecutionNote(&Script{}), "nothing will execute it")
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
