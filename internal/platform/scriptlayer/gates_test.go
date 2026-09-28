package scriptlayer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlint"
	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
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
	first, _ := findings[0].(map[string]any)
	assert.Equal(t, scriptlint.RuleEntryPoint, first["rule"])
	assert.NotEmpty(t, first["hint"])
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

// seedLegacy stores a script as one saved before the gates.
func seedLegacy(t *testing.T, store *memStore, source string) {
	t.Helper()
	sc := &script.Script{
		Name: "old", DisplayName: "Old", Source: source, OwnerEmail: "jane@example.com",
		Enabled: true, Status: script.StatusActive, Legacy: true, TestsOptional: true,
	}
	require.NoError(t, store.Create(context.Background(), sc, script.Author{Email: "jane@example.com"}))
}

// A script saved before the gates saves a version that adds no finding, is
// refused for one that adds a finding naming only that one, and a patch is
// held to the same rule (#1938). Its top level may still work (#1944).
func TestGates_LegacyScriptRefusedOnlyForWhatAnEditAdds(t *testing.T) {
	h, store := newHandle()
	const old = "for r in run.params[\"ids\"]:\n    platform.call(\"x\", {\"id\": r})\n"
	seedLegacy(t, store, old)

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "old", Source: "# still loops\n" + old})
	fields := resultFields(t, res)
	require.Equal(t, "updated", fields[fieldStatus], resultText(res))
	assert.NotEmpty(t, fields["findings"], "the finding it carried is reported")

	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "old", Source: old + "json = 1\n"})
	fields = resultFields(t, res)
	require.Equal(t, "invalid", fields[fieldStatus], resultText(res))
	var refused []any
	list, _ := fields["findings"].([]any)
	for _, f := range list {
		if m, _ := f.(map[string]any); m["severity"] == "error" {
			refused = append(refused, m["rule"])
		}
	}
	assert.Equal(t, []any{scriptlint.RuleShadowedName}, refused)

	res = call(t, h, authorCtx(), manageScriptInput{
		Command: cmdPatch, Name: "old",
		Edits: []textpatch.Edit{{Op: "append", Text: "def helper():\n    return 1\n"}},
	})
	assert.Equal(t, "invalid", resultFields(t, res)[fieldStatus], resultText(res))
}
