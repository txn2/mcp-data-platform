package scriptlint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// rulesOf is the rules of every finding a new script's source carries.
func rulesOf(source string) []string {
	found := lint(source)
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

// TestAHelperOnlyTestsReachMayUseTesting is #2116: a shared helper that
// declares a flow's answers is a test's helper when no run can reach it, and
// the save still refuses testing anywhere a run could.
func TestAHelperOnlyTestsReachMayUseTesting(t *testing.T) {
	helper := `def main():
    """Logs in and reads the list."""
    platform.call("api_invoke_endpoint", {"path": "/login"})

def fake_login(times = 1):
    """Declares the login the flow makes."""
    testing.answer("api_invoke_endpoint", {"path": "/login"}, {"status": 200, "body": {}}, times = times)

def answers_for(n):
    """A second helper, reached only through the first's callers."""
    fake_login(n)

def test_main():
    """Runs main."""
    answers_for(1)
    main()
    assert.eq(len(testing.outputs().calls), 1)
`
	rules := rulesOf(helper)
	assert.NotContains(t, rules, RuleTestModuleOutsideTest, "a helper only tests reach may declare answers")

	for name, source := range map[string]string{
		"main calls it": `def main():
    """Calls the helper."""
    fake_login()

def fake_login():
    """Declares the login."""
    testing.answer("api_invoke_endpoint", {"path": "/login"}, {"status": 200})
`,
		"a run reaches it through another function": `def main():
    """Calls a step."""
    step()

def step():
    """The step."""
    fake_login()

def fake_login():
    """Declares the login."""
    testing.answer("x", {}, {})
`,
		"passed as a value": `def main():
    """Maps over it."""
    print([f() for f in [fake]])

def fake():
    """Asserts."""
    assert.true(True)
`,
		"top-level code reaches it": `def fake():
    """Asserts."""
    assert.true(True)

FIRST = fake

def main():
    """Prints."""
    print(1)
`,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Contains(t, rulesOf(source), RuleTestModuleOutsideTest)
		})
	}
}

// A helper may declare answers, but a test names the recording it replays
// itself, which is how a save knows which recordings to keep.
func TestAHelperMayNotNameARecording(t *testing.T) {
	source := `def main():
    """Prints."""
    print(1)

def replayed():
    """Replays a recording."""
    testing.replay("run_1")

def test_main():
    """Runs main."""
    replayed()
    main()
    assert.eq(testing.outputs().log, "1\n")
`
	found := lint(source)
	var messages []string
	for _, f := range found {
		if f.rule == RuleTestModuleOutsideTest {
			messages = append(messages, f.message)
		}
	}
	assert.Equal(t, []string{"`testing.replay` is called in replayed, which is not a test"}, messages)

	calls := `def main():
    """Prints."""
    print(1)

def helper():
    """Calls a test."""
    test_main()

def test_main():
    """Runs main."""
    main()
    assert.eq(testing.outputs().log, "1\n")
`
	assert.Contains(t, rulesOf(calls), RuleTestCalled)
}

// A statement run through a transaction's tx, and each statement of a batch,
// are held to the rule platform.query is (#2402).
func TestSQLFromValuesCoversTransactionsAndBatches(t *testing.T) {
	for name, src := range map[string]string{
		"tx.execute":          "def main():\n    \"\"\"Doc.\"\"\"\n    def place(tx):\n        \"\"\"Doc.\"\"\"\n        tx.execute(\"DELETE FROM t WHERE id = \" + run.params[\"id\"])\n    platform.transaction(\"shop\", place)\n",
		"tx.batch":            "def main():\n    \"\"\"Doc.\"\"\"\n    def place(tx):\n        \"\"\"Doc.\"\"\"\n        tx.batch([\"DELETE FROM t WHERE id = %s\" % run.params[\"id\"]])\n    platform.transaction(\"shop\", place)\n",
		"batch dict":          "def main():\n    \"\"\"Doc.\"\"\"\n    platform.batch(\"shop\", [{\"sql\": \"DELETE FROM t WHERE id = {}\".format(run.params[\"id\"])}])\n",
		"batch comprehension": "def main():\n    \"\"\"Doc.\"\"\"\n    platform.batch(\"shop\", [\"DELETE FROM t WHERE id = \" + i for i in run.params[\"ids\"]])\n",
	} {
		assert.Contains(t, rulesOf(src), RuleSQLFromValues, name)
	}
	bound := "def main():\n    \"\"\"Doc.\"\"\"\n    def place(tx):\n        \"\"\"Doc.\"\"\"\n        tx.execute(\"DELETE FROM t WHERE id = :id\", params = {\"id\": run.params[\"id\"]})\n        tx.batch([{\"sql\": \"DELETE FROM u WHERE id = :id\", \"params\": {\"id\": 1}}, \"SELECT 1\"])\n    platform.transaction(\"shop\", place)\n    platform.batch(\"shop\", [\"SELECT 1\"])\n"
	assert.NotContains(t, rulesOf(bound), RuleSQLFromValues)
}
