package scriptlayer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlint"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// untidy is a valid new-shape script in a layout nobody would store.
const untidy = "def main():\n  '''Exports one row.'''\n  platform.export(name='x',rows=[{'a':1}],format='csv')  # the output\n" +
	"def test_main():\n  '''Exports it.'''\n  main()\n  assert.eq(testing.outputs().exports[0].rows[0]['a'],1)\n"

// tidy is untidy as the formatter stores it.
const tidy = "def main():\n    \"\"\"Exports one row.\"\"\"\n    platform.export(name = \"x\", rows = [{\"a\": 1}], format = \"csv\")  # the output\n" +
	"\ndef test_main():\n    \"\"\"Exports it.\"\"\"\n    main()\n    assert.eq(testing.outputs().exports[0].rows[0][\"a\"], 1)\n"

// A save stores the formatted source and says so; saving that source again
// changes nothing in it (#1937).
func TestGates_CreateAndUpdateStoreTheFormattedSource(t *testing.T) {
	h, store := newHandle()
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "daily", Source: untidy})
	fields := resultFields(t, res)
	require.Equal(t, "created", fields[fieldStatus], resultText(res))
	assert.Equal(t, true, fields["source_formatted"])
	require.Len(t, store.scripts, 1)
	for _, sc := range store.scripts {
		assert.Equal(t, tidy, sc.Source)
	}

	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", Source: tidy})
	fields = resultFields(t, res)
	require.Equal(t, "updated", fields[fieldStatus], resultText(res))
	assert.NotContains(t, fields, "source_formatted", "already formatted source is stored as sent")
	for _, sc := range store.scripts {
		assert.Equal(t, tidy, sc.Source)
	}
}

// A new script is refused on save while it has any finding, with the rule, the
// line and a hint; the corrected script saves (#1938, #1944).
func TestGates_CreateRefusesAFindingAndNamesIt(t *testing.T) {
	h, store := newHandle()
	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "daily", Source: "rows = platform.query(\"SELECT 1\")\nprint(rows)\n",
	})
	fields := resultFields(t, res)
	require.Equal(t, "invalid", fields[fieldStatus])
	assert.Empty(t, store.scripts)
	findings, _ := fields["findings"].([]any)
	require.NotEmpty(t, findings)
	// A source with no main() is a library, and a library may not name
	// platform; its work at the top level is the other finding (#1941).
	rules := map[string]bool{}
	for _, f := range findings {
		m, _ := f.(map[string]any)
		rule, _ := m["rule"].(string)
		rules[rule] = true
		assert.NotEmpty(t, m["hint"])
	}
	assert.True(t, rules[scriptlint.RuleLibraryEffect], "%v", rules)
	assert.True(t, rules[scriptlint.RuleTopLevelWork], "%v", rules)
	assert.Contains(t, fields, "formatted_source")

	res = call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "daily", Source: tested(inMain("rows = [1]\nprint(rows)\n")),
	})
	assert.Equal(t, "created", resultFields(t, res)[fieldStatus], resultText(res))
}

// validate reports the gates' findings without saving, with the formatted
// source beside them, and ok says whether a save would go through.
func TestGates_ValidateReportsWithoutSaving(t *testing.T) {
	h, store := newHandle()
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdValidate, Source: "def main():\n    print(1)\n"})
	fields := resultFields(t, res)
	assert.Equal(t, false, fields["ok"])
	assert.Equal(t, "def main():\n    print(1)\n", fields["formatted_source"])
	findings, _ := fields["findings"].([]any)
	require.Len(t, findings, 1)
	f, _ := findings[0].(map[string]any)
	assert.Equal(t, scriptlint.RuleMissingDocstring, f["rule"])
	assert.EqualValues(t, 1, f["line"])
	assert.Empty(t, store.scripts)

	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdValidate, Source: untidy})
	fields = resultFields(t, res)
	assert.Equal(t, true, fields["ok"])
	assert.Equal(t, tidy, fields["formatted_source"])
}

// A script saved before the gates, written to the store as it was then, is
// refused on its next save for the findings it already carried: every script
// is held to the same rules (#1965).
func TestGates_AScriptSavedBeforeTheGatesIsHeldToThemOnItsNextSave(t *testing.T) {
	h, store := newHandle()
	const old = "for r in run.params[\"ids\"]:\n    platform.call(\"x\", {\"id\": r})\n"
	sc := &script.Script{
		Name: "old", DisplayName: "Old", Source: old, OwnerEmail: "jane@example.com",
		Enabled: true, Status: script.StatusActive,
	}
	require.NoError(t, store.Create(context.Background(), sc, script.Author{Email: "jane@example.com"}))

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "old", Source: "# still loops\n" + old})
	fields := resultFields(t, res)
	require.Equal(t, "invalid", fields[fieldStatus], resultText(res))
	rules := map[any]bool{}
	list, _ := fields["findings"].([]any)
	for _, f := range list {
		if m, _ := f.(map[string]any); m["severity"] == "error" {
			rules[m["rule"]] = true
		}
	}
	assert.True(t, rules[scriptlint.RuleTopLevelWork], "%v", list)
	assert.True(t, rules[scriptlint.RuleCallInLoop], "%v", list)
}
