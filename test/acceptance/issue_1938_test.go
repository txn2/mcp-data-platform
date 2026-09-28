//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1938: structural lint and limits over the syntax tree, reported by
// validate and enforced on save. These criteria save one script per rule
// through manage_script on the running platform, then the corrected script.
//
// Wire forms: manage_script's command, name, description and source are
// typed strings, so each admits one JSON form and is sent as a literal
// tools/call parameter of it.

// rule1938 is one rule's case: a script that breaks it, the line the finding
// names, and the script corrected.
type rule1938 struct {
	rule      string
	line      float64
	broken    string
	corrected string
}

func branches1938(n int) string {
	var b strings.Builder
	b.WriteString("def main():\n    \"\"\"Prints the band.\"\"\"\n    x = run.params.get(\"x\", 0)\n")
	for i := range n {
		fmt.Fprintf(&b, "    if x == %d:\n        print(%d)\n", i, i)
	}
	return b.String()
}

// long1938 is main with its docstring and n statements after it: n+1 in all.
func long1938(n int) string {
	return "def main():\n    \"\"\"Prints.\"\"\"\n" + strings.Repeat("    print(1)\n", n)
}

var rules1938 = []rule1938{
	{"cyclomatic-complexity", 1, branches1938(11), branches1938(9)},
	{"cognitive-complexity", 1,
		"def main():\n    \"\"\"Nests.\"\"\"\n    for a in [1, 2]:\n        if a and a > 1 or a < 0:\n            for b in range(a):\n                if b:\n                    print(1 if b else 2)\n                elif a:\n                    print(3)\n                else:\n                    print(4)\n",
		"def band(a, b):\n    \"\"\"Prints one band.\"\"\"\n    if b:\n        print(1)\n    elif a:\n        print(3)\n\ndef main():\n    \"\"\"Nests less.\"\"\"\n    for a in [1, 2]:\n        for b in range(a):\n            band(a, b)\n"},
	{"function-length", 1, long1938(40), long1938(39)},
	{"nesting-depth", 7,
		"def main():\n    \"\"\"Nests.\"\"\"\n    for a in [1]:\n        for b in [a]:\n            if b:\n                if a:\n                    if b > 1:\n                        print(a, b)\n",
		"def main():\n    \"\"\"Nests less.\"\"\"\n    for a in [1]:\n        for b in [a]:\n            if b and a and b > 1:\n                print(a, b)\n"},
	{"unused-variable", 3, "def main():\n    \"\"\"Doc.\"\"\"\n    x = 1\n    print(2)\n", "def main():\n    \"\"\"Doc.\"\"\"\n    print(2)\n"},
	{"unused-parameter", 1,
		"def show(a, b):\n    \"\"\"Shows a.\"\"\"\n    print(a)\n\ndef main():\n    \"\"\"Doc.\"\"\"\n    show(1, 2)\n",
		"def show(a):\n    \"\"\"Shows a.\"\"\"\n    print(a)\n\ndef main():\n    \"\"\"Doc.\"\"\"\n    show(1)\n"},
	{"shadowed-name", 3, "def main():\n    \"\"\"Doc.\"\"\"\n    date = \"2026-01-01\"\n    print(date)\n", "def main():\n    \"\"\"Doc.\"\"\"\n    day = \"2026-01-01\"\n    print(day)\n"},
	{"missing-docstring", 1, "def main():\n    print(1)\n", "def main():\n    \"\"\"Prints one.\"\"\"\n    print(1)\n"},
	{"sql-built-from-values", 4,
		"def main():\n    \"\"\"Doc.\"\"\"\n    day = run.params.get(\"day\", \"2026-01-01\")\n    platform.query(\"SELECT 1 WHERE d = '\" + day + \"'\", connection = \"acme-warehouse\")\n",
		"def main():\n    \"\"\"Doc.\"\"\"\n    day = run.params.get(\"day\", \"2026-01-01\")\n    platform.query(\"SELECT 1 WHERE d = :day\", connection = \"acme-warehouse\", params = {\"day\": day})\n"},
	{"call-in-loop", 4,
		"def main():\n    \"\"\"Doc.\"\"\"\n    for r in [\"a\", \"b\"]:\n        platform.query(\"SELECT :r\", connection = \"acme-warehouse\", params = {\"r\": r})\n",
		"def main():\n    \"\"\"Doc.\"\"\"\n    platform.query(\"SELECT 1 WHERE r IN :rs\", connection = \"acme-warehouse\", params = {\"rs\": [\"a\", \"b\"]})\n"},
	{"save-state-without-read", 3,
		"def main():\n    \"\"\"Doc.\"\"\"\n    platform.save_state({\"n\": 1})\n",
		"def main():\n    \"\"\"Doc.\"\"\"\n    platform.save_state({\"n\": run.state.get(\"n\", 0) + 1})\n"},
}

// refusedRules1938 is every rule a refused save names with a line and a hint.
func refusedRules1938(out map[string]any) map[string]float64 {
	got := map[string]float64{}
	for _, f := range findings1944(out) {
		if f["severity"] == "error" && f["hint"] != "" {
			rule, _ := f["rule"].(string)
			line, _ := f["line"].(float64)
			got[rule] = line
		}
	}
	return got
}

// TestIssue1938_EachRuleIsRefusedAndTheCorrectionSaves: one script per rule is
// refused on save with the rule, its line and a hint; validate reports the
// same without saving; the corrected script saves.
func TestIssue1938_EachRuleIsRefusedAndTheCorrectionSaves(t *testing.T) {
	c := connect(t)
	for _, tc := range rules1938 {
		t.Run(tc.rule, func(t *testing.T) {
			name := fmt.Sprintf("acc-1938-%s-%d", tc.rule, time.Now().UnixNano())
			t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

			validated := c.call("manage_script", map[string]any{"command": "validate", "name": name, "source": tc.broken})
			if line, ok := refusedRules1938(validated)[tc.rule]; !ok || line != tc.line || validated["ok"] != false {
				t.Errorf("validate: want %s on line %v: %v", tc.rule, tc.line, validated["findings"])
			}
			refused := c.call("manage_script", map[string]any{
				"command": "create", "name": name, "source": tc.broken, "description": "Acceptance #1938.",
			})
			if line, ok := refusedRules1938(refused)[tc.rule]; refused["status"] != "invalid" || !ok || line != tc.line {
				t.Fatalf("save: want %s refused on line %v: %v", tc.rule, tc.line, refused)
			}
			saved := c.call("manage_script", map[string]any{
				"command": "create", "name": name, "source": tc.corrected, "description": "Acceptance #1938.",
			})
			if saved["status"] != "created" {
				t.Errorf("the corrected script did not save: %v", saved)
			}
		})
	}
}

// TestIssue1938_AnOlderScriptIsRefusedOnlyForWhatAnEditAdds: a script saved
// before the gates, carrying a finding, saves an edit that adds none and is
// refused for an edit that adds one, naming only that one.
func TestIssue1938_AnOlderScriptIsRefusedOnlyForWhatAnEditAdds(t *testing.T) {
	c := connect(t)
	db := issue1904DB(t)
	name := fmt.Sprintf("acc-1938-legacy-%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	const old = "def show(a, b):\n    print(a)\n\nshow(1, 2)\n"
	c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": "def main():\n    \"\"\"Doc.\"\"\"\n    print(1)\n",
		"description": "Acceptance #1938: a script saved before the gates.",
	})
	issue1904Exec(t, db, `UPDATE scripts SET legacy = TRUE, source_code = $2 WHERE name = $1`, name, old)

	kept := c.call("manage_script", map[string]any{"command": "update", "name": name, "source": "# a comment\n" + old})
	if kept["status"] != "updated" || len(findings1944(kept)) == 0 {
		t.Fatalf("an edit adding no finding: %v", kept)
	}
	added := c.call("manage_script", map[string]any{"command": "update", "name": name, "source": old + "run = 1\nprint(run)\n"})
	got := refusedRules1938(added)
	line := float64(strings.Count(strings.Split(fmt.Sprint(added["formatted_source"]), "run = 1")[0], "\n") + 1)
	if added["status"] != "invalid" || len(got) != 1 || got["shadowed-name"] != line {
		t.Errorf("an edit adding a shadowed name: want only shadowed-name on line %v, got %v: %v", line, got, added)
	}
}

// TestIssue1938_TheBuiltInExamplesPassEveryRule: every worked example help
// lists is one validate reports no finding for, as the script it would save.
func TestIssue1938_TheBuiltInExamplesPassEveryRule(t *testing.T) {
	c := connect(t)
	help := c.call("manage_script", map[string]any{"command": "help"})
	examples, _ := help["examples"].([]any)
	if len(examples) == 0 {
		t.Fatalf("help lists no examples: %v", help)
	}
	for _, e := range examples {
		name, _ := e.(map[string]any)["name"].(string)
		source := c.call("manage_script", map[string]any{"command": "get", "name": name})["source"]
		out := c.call("manage_script", map[string]any{"command": "validate", "source": source})
		if out["ok"] != true || len(findings1944(out)) != 0 || out["formatted_source"] != source {
			t.Errorf("%s: %v", name, out["findings"])
		}
	}
	if !strings.Contains(fmt.Sprint(help["dialect"]), "THE SHAPE OF A SCRIPT, AND WHAT A SAVE CHECKS") {
		t.Errorf("help does not state what a save checks")
	}
}
