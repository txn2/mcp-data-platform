package scriptrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sumRun executes source with no warehouse behind it, which is all a test of
// sum needs: it is a pure fold over values the script already holds.
func sumRun(t *testing.T, source string) (*Result, error) {
	t.Helper()
	return Run(context.Background(), Options{
		Source: source, Name: "test", RunID: "dpx_sum", FireTime: fireTime,
		Caller: &recordingCaller{},
	})
}

// TestSum_TotalsTheDocumentedDecimalIdiom is the reason sum exists: the
// contract prescribes it as the fix for a DECIMAL column arriving as a string,
// and before #1414 a script written from that prescription was refused at save.
func TestSum_TotalsTheDocumentedDecimalIdiom(t *testing.T) {
	result, err := sumRun(t, `rows = [{"total": "10.50"}, {"total": "4.25"}, {"total": "0.25"}]
print(sum([float(r["total"]) for r in rows]))
`)
	require.NoError(t, err)
	assert.Contains(t, result.Log, "15")
}

// TestSum_IsVisibleToTheValidator is the half that was broken: a name the
// engine can bind but the resolver refuses is a script that passes a dry run
// and is rejected at save. Both halves read PredeclaredNames.
func TestSum_IsVisibleToTheValidator(t *testing.T) {
	report := Validate("total = sum([1, 2])\n")
	assert.True(t, report.OK, "%+v", report.Findings)
}

// TestPredeclaredMatchesNames pins the binding table against the name list the
// resolver answers from. Drift between them is exactly the #1414 defect class:
// a global one half knows about and the other does not.
func TestPredeclaredMatchesNames(t *testing.T) {
	bound := predeclared(&hostState{opts: Options{RunID: "dpx_1", FireTime: fireTime}})
	names := make([]string, 0, len(bound))
	for name := range bound {
		names = append(names, name)
		assert.True(t, isPredeclaredName(name),
			"%q is bound at run time but the validator does not resolve it", name)
	}
	assert.ElementsMatch(t, PredeclaredNames, names)
}

// TestUndefinedNameHintListsTheEnvironment keeps the correction honest: it is
// the sentence an author reads after reaching for a name that is not there, so
// the set it names has to be the set that exists.
func TestUndefinedNameHintListsTheEnvironment(t *testing.T) {
	report := Validate("x = pandas\n")
	require.False(t, report.OK)
	require.NotEmpty(t, report.Findings)
	for _, name := range PredeclaredNames {
		assert.Contains(t, report.Findings[0].Hint, "`"+name+"`")
	}
}

// TestQuotedList covers the shapes the environment list can take as names are
// added or removed, since the hint is composed from it rather than written out.
func TestQuotedList(t *testing.T) {
	assert.Empty(t, quotedList(nil))
	assert.Equal(t, "`platform`", quotedList([]string{"platform"}))
	assert.Equal(t, "`platform` and `run`", quotedList([]string{"platform", "run"}))
	assert.Equal(t, "`a`, `b`, and `c`", quotedList([]string{"a", "b", "c"}))
}
