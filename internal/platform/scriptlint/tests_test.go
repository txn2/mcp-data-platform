package scriptlint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// rulesOf is the rules of every finding a new script's source carries.
func rulesOf(source string) []string {
	found := lint(source, true)
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.rule)
	}
	return out
}

// testing and assert belong to a test, and a test is the runner's to call
// (#1939).
func TestTestsStayApartFromWhatTheScriptRuns(t *testing.T) {
	clean := `def main():
    """Prints."""
    print(1)

def test_main():
    """Runs main."""
    testing.replay("run_1")
    main()
    assert.eq(1, 1)
`
	assert.NotContains(t, rulesOf(clean), RuleTestModuleOutsideTest)
	assert.NotContains(t, rulesOf(clean), RuleTestCalled)

	outside := `def main():
    """Asserts in a run."""
    assert.eq(1, 1)
`
	assert.Contains(t, rulesOf(outside), RuleTestModuleOutsideTest)

	called := `def main():
    """Calls its test."""
    test_main()

def test_main():
    """Runs main."""
    print(1)
`
	assert.Contains(t, rulesOf(called), RuleTestCalled)

	// A script's own binding of the name is shadowed-name's finding, not this
	// one's.
	own := `def main():
    """Its own assert."""
    assert = 1
    print(assert)
`
	rules := rulesOf(own)
	assert.Contains(t, rules, RuleShadowedName)
	assert.NotContains(t, rules, RuleTestModuleOutsideTest)
}

// An assertion over values the test itself wrote holds whatever the script
// does.
func TestAnAssertionOverConstantsIsAFinding(t *testing.T) {
	for _, body := range []string{
		"assert.eq(1, 1)", "assert.true(True)", "assert.ne([1, 2], [2])",
		`assert.contains({"a": 1}, "a")`, "assert.eq(-1, (1 - 2))",
	} {
		source := "def main():\n    \"\"\"Prints.\"\"\"\n    print(1)\n\ndef test_main():\n    \"\"\"Runs.\"\"\"\n    main()\n    " + body + "\n"
		assert.Contains(t, rulesOf(source), RuleConstantAssertion, body)
	}
	reading := "def main():\n    \"\"\"Prints.\"\"\"\n    print(1)\n\ndef test_main():\n    \"\"\"Runs.\"\"\"\n    main()\n    assert.eq(testing.outputs().log, \"1\\n\")\n    assert.fails(main)\n"
	assert.NotContains(t, rulesOf(reading), RuleConstantAssertion)
}

// A value the test computes is not written out, even inside a literal.
func TestAComputedValueIsNotAConstantAssertion(t *testing.T) {
	source := "def main():\n    \"\"\"Prints.\"\"\"\n    print(1)\n\ndef test_main():\n    \"\"\"Runs.\"\"\"\n    main()\n    assert.eq([1, len(testing.outputs().log)], [1, 2])\n"
	assert.NotContains(t, rulesOf(source), RuleConstantAssertion)
}
