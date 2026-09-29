package scriptlint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
)

// clean is a script that passes every rule, the base each case below breaks.
const clean = `"""A daily report."""

CONNECTION = "primary"

def fetch(day):
    """Reads one day's orders."""
    return platform.query(
        "SELECT region, total FROM orders WHERE day = DATE :day",
        connection = CONNECTION,
        params = {"day": day},
    )["rows"]

def main():
    """Exports yesterday's orders."""
    since = run.state.get("since", "2026-01-01")
    rows = fetch(since)
    platform.export(name = "orders", rows = rows, format = "csv")
    platform.save_state({"since": since})
`

func rules(res Result) []string {
	out := make([]string, 0, len(res.Findings))
	for _, f := range res.Findings {
		out = append(out, f.Rule)
	}
	return out
}

func TestCheck_CleanScriptPasses(t *testing.T) {
	res := Check(clean)
	assert.Empty(t, res.Findings)
	assert.Empty(t, res.Refused)
	assert.Equal(t, clean, res.Source, "already formatted source is stored byte for byte")
}

// Each rule, one script that breaks it: refused with the rule, a line and a
// hint, on a script created since the gates.
func TestCheck_EachRuleRefusesANewScript(t *testing.T) {
	deep := "def main():\n    \"\"\"Nests.\"\"\"\n    for a in [1]:\n        for b in [a]:\n            if b:\n                if a:\n                    if b > 1:\n                        print(a, b)\n"
	lines := make([]string, 0, 25)
	lines = append(lines, "def main():", `    """Branches."""`, `    x = run.params["x"]`)
	for i := range 11 {
		lines = append(lines, fmt.Sprintf("    if x == %d:", i), fmt.Sprintf("        print(%d)", i))
	}
	branches := strings.Join(lines, "\n") + "\n"
	long := "def main():\n    \"\"\"Is long.\"\"\"\n" + strings.Repeat("    print(1)\n", MaxStatements+1)
	cognitiveSrc := "def main():\n    \"\"\"Nests conditions.\"\"\"\n    for a in run.params[\"a\"]:\n        if a and a > 1 or a < 0:\n            for b in range(a):\n                if b:\n                    print(1 if b else 2)\n                elif a:\n                    print(3)\n                else:\n                    print(4)\n"
	cases := map[string]struct {
		src  string
		rule string
		line int
	}{
		"library names platform":           {"\"\"\"Doc.\"\"\"\n\ndef rows():\n    \"\"\"Doc.\"\"\"\n    return platform.query(\"SELECT 1\")\n", RuleLibraryEffect, 5},
		"library names run":                {"def day():\n    \"\"\"Doc.\"\"\"\n    return run.params[\"day\"]\n", RuleLibraryEffect, 3},
		"main takes params":                {"def main(x):\n    \"\"\"Doc.\"\"\"\n    print(x)\n", RuleEntryPoint, 1},
		"work at top level":                {clean + "\nprint(\"hi\")\n", RuleTopLevelWork, 20},
		"top-level call value":             {"X = run.params[\"a\"]\n\n" + clean, RuleTopLevelWork, 1},
		"cyclomatic":                       {branches, RuleCyclomatic, 1},
		"cognitive":                        {cognitiveSrc, RuleCognitive, 1},
		"function length":                  {long, RuleFunctionLength, 1},
		"nesting":                          {deep, RuleNestingDepth, 7},
		"unused variable":                  {"def main():\n    \"\"\"Doc.\"\"\"\n    x = 1\n", RuleUnusedVariable, 3},
		"unused parameter":                 {strings.Replace(clean, "def fetch(day):", "def fetch(day, limit):", 1), RuleUnusedParameter, 5},
		"shadowed predeclared":             {"def main():\n    \"\"\"Doc.\"\"\"\n    json = 1\n    print(json)\n", RuleShadowedName, 3},
		"missing docstring":                {strings.Replace(clean, "    \"\"\"Reads one day's orders.\"\"\"\n", "", 1), RuleMissingDocstring, 5},
		"sql from values":                  {"def main():\n    \"\"\"Doc.\"\"\"\n    day = run.params[\"day\"]\n    platform.query(\"SELECT 1 WHERE d = '\" + day + \"'\")\n", RuleSQLFromValues, 4},
		"sql via a variable":               {"def main():\n    \"\"\"Doc.\"\"\"\n    sql = \"SELECT %s\" % run.params[\"c\"]\n    platform.query(sql)\n", RuleSQLFromValues, 4},
		"sql .format":                      {"def main():\n    \"\"\"Doc.\"\"\"\n    platform.query(sql = \"SELECT {}\".format(run.params[\"c\"]))\n", RuleSQLFromValues, 3},
		"sql into execute":                 {"def main():\n    \"\"\"Doc.\"\"\"\n    platform.execute(\"DELETE FROM t WHERE id = \" + run.params[\"id\"])\n", RuleSQLFromValues, 3},
		"sql into trino_query":             {"def main():\n    \"\"\"Doc.\"\"\"\n    platform.call(\"trino_query\", {\"sql\": \"SELECT \" + run.params[\"c\"]})\n", RuleSQLFromValues, 3},
		"sql into trino_execute via names": {"def main():\n    \"\"\"Doc.\"\"\"\n    sql = \"DELETE FROM t WHERE id = %s\" % run.params[\"id\"]\n    args = {\"connection\": \"acme\", \"sql\": sql}\n    platform.call(tool = \"trino_execute\", args = args)\n", RuleSQLFromValues, 5},
		"call in loop":                     {"def main():\n    \"\"\"Doc.\"\"\"\n    for r in run.params[\"ids\"]:\n        platform.call(\"x\", {\"id\": r})\n", RuleCallInLoop, 4},
		"call in comprehension":            {"def main():\n    \"\"\"Doc.\"\"\"\n    print([platform.query(\"SELECT :i\", params = {\"i\": i}) for i in run.params[\"ids\"]])\n", RuleCallInLoop, 3},
		"save without read":                {"def main():\n    \"\"\"Doc.\"\"\"\n    platform.save_state({\"a\": 1})\n", RuleStateWithoutRead, 3},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			require.True(t, scriptrun.Validate(tc.src).OK, "the case must be a runnable script: %+v", scriptrun.Validate(tc.src).Findings)
			res := Check(tc.src)
			var hit *scriptrun.Finding
			for i, f := range res.Refused {
				if f.Rule == tc.rule {
					hit = &res.Refused[i]
				}
			}
			require.NotNil(t, hit, "want %s refused, got %v", tc.rule, rules(res))
			assert.Equal(t, tc.line, hit.Line)
			assert.NotEmpty(t, hit.Hint)
			assert.Equal(t, scriptrun.SeverityError, hit.Severity)
		})
	}
}

// What the rules deliberately leave alone.
func TestCheck_WhatIsNotAFinding(t *testing.T) {
	for name, src := range map[string]string{
		"a page loop over range": "def main():\n    \"\"\"Pages.\"\"\"\n    for _ in range(5):\n        page = platform.call(\"x\", {})\n        if not page:\n            break\n",
		"a library":              "\"\"\"Doc.\"\"\"\n\nX = 1\n\ndef double(n):\n    \"\"\"Doubles n.\"\"\"\n    return n * 2\n",
		"sql to another tool":    "def main():\n    \"\"\"Doc.\"\"\"\n    platform.call(\"api_invoke_endpoint\", {\"sql\": \"a\" + run.params[\"c\"]})\n",
		"computed tool name":     "def main():\n    \"\"\"Doc.\"\"\"\n    platform.call(run.params[\"t\"], {\"sql\": \"a\" + run.params[\"c\"]})\n",
		"execute from constants": "TABLE = \"sales.orders\"\n\ndef main():\n    \"\"\"Doc.\"\"\"\n    platform.execute(\"DELETE FROM \" + TABLE)\n",
		"sql from constants":     "TABLE = \"sales.orders\"\n\ndef main():\n    \"\"\"Doc.\"\"\"\n    platform.query(\"SELECT * FROM \" + TABLE)\n",
		"underscore names":       "def helper(_unused):\n    \"\"\"Doc.\"\"\"\n    return 1\n\ndef main():\n    \"\"\"Doc.\"\"\"\n    for _ in [1]:\n        print(helper(2))\n",
		"augmented assignment":   "def main():\n    \"\"\"Doc.\"\"\"\n    n = 0\n    n += 1\n",
		"closure use":            "def main():\n    \"\"\"Doc.\"\"\"\n    n = 2\n    f = lambda x: x * n\n    print(f(1))\n",
		"constants and load":     "\"\"\"Doc.\"\"\"\n\nA = [1, -2, {\"k\": (3, 4)}]\nB = A[0] + 2 if True else 3\nC = lambda x: x\n\ndef main():\n    \"\"\"Doc.\"\"\"\n    print(A, B, C(1))\n",
	} {
		t.Run(name, func(t *testing.T) {
			res := Check(src)
			assert.Empty(t, res.Findings, "%v", res.Findings)
		})
	}
}

// A source written before the gates (work at the top level, a host call in a
// loop) is held to every rule: each finding is an error and refuses the save.
func TestCheck_EveryFindingRefuses(t *testing.T) {
	src := "rows = platform.query(\"SELECT 1\")[\"rows\"]\nfor r in rows:\n    platform.call(\"x\", {\"id\": r})\n"
	res := Check(src)
	require.NotEmpty(t, res.Findings)
	assert.Equal(t, res.Findings, res.Refused)
	rules := map[string]bool{}
	for _, f := range res.Findings {
		assert.Equal(t, scriptrun.SeverityError, f.Severity)
		rules[f.Rule] = true
	}
	assert.True(t, rules[RuleTopLevelWork], "%v", res.Findings)
	assert.True(t, rules[RuleCallInLoop], "%v", res.Findings)
}

// Check stores the formatted source and lints what it stores.
func TestCheck_FormatsBeforeLinting(t *testing.T) {
	src := "def main():\n  '''Doc.'''\n  platform.export(name='x',rows=[1,2],format='csv')\n"
	res := Check(src)
	assert.Equal(t, "def main():\n    \"\"\"Doc.\"\"\"\n    platform.export(name = \"x\", rows = [1, 2], format = \"csv\")\n", res.Source)
	assert.Empty(t, res.Findings)
}

func TestCheck_UnparseableSourceHasNoLintFindings(t *testing.T) {
	assert.Empty(t, Check("def main(:\n").Findings)
	assert.Empty(t, Check("   ").Findings)
}
