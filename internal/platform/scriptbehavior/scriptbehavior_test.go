package scriptbehavior

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scripttest"
	"github.com/txn2/mcp-data-platform/internal/tablexlsx"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

func export(name string, rows ...map[string]any) scriptrun.ExportRequest {
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, r)
	}
	return scriptrun.ExportRequest{Name: name, Format: "csv", Rows: out, Destination: script.Destination{Name: "portal"}}
}

func details(diffs []Difference) []string {
	out := make([]string, 0, len(diffs))
	for _, d := range diffs {
		out = append(out, d.String())
	}
	return out
}

func TestTheSameOutcomeIsNoDifference(t *testing.T) {
	o := scripttest.Outcome{
		Exports:   []scriptrun.ExportRequest{export("w", map[string]any{"a": int64(1)})},
		Publishes: []scriptrun.PublishRequest{{Name: "d", Data: map[string]any{"x": 1}}},
		State:     map[string]any{"n": 1},
		Calls:     []scriptrec.Made{{Tool: "notify", Args: map[string]any{"channel": "ops", "title": "t"}}},
		Result:    json.RawMessage(`{"ok":true}`),
	}
	diffs, compared := Compare("r", o, o)
	assert.True(t, compared)
	assert.Empty(t, diffs)
}

func TestAnOutputsColumnsTypesAndRowsAreCompared(t *testing.T) {
	before := scripttest.Outcome{Exports: []scriptrun.ExportRequest{
		export("w", map[string]any{"a": int64(1), "b": "x"}, map[string]any{"a": int64(2), "b": "y"}),
		export("gone"),
	}}
	after := scripttest.Outcome{Exports: []scriptrun.ExportRequest{
		export("w", map[string]any{"a": "1", "c": true}),
		export("new"),
	}}
	after.Exports[0].Format = "json"
	diffs, compared := Compare("r", before, after)
	require.True(t, compared)
	assert.ElementsMatch(t, []string{
		`run r: output "w" is written as json, where it was csv`,
		`run r: output "w" column "a" is string, where it was int`,
		`run r: output "w" has a new column "c"`,
		`run r: output "w" no longer has column "b"`,
		`run r: output "w" has 1 rows, where it had 2`,
		`run r: writes a new output "new"`,
		`run r: no longer writes output "gone"`,
	}, details(diffs))

	same := scripttest.Outcome{Exports: []scriptrun.ExportRequest{export("w", map[string]any{"a": int64(1)})}}
	changed := scripttest.Outcome{Exports: []scriptrun.ExportRequest{export("w", map[string]any{"a": int64(9)})}}
	diffs, _ = Compare("r", same, changed)
	assert.Equal(t, []string{`run r: output "w" has the same number of rows with different values`}, details(diffs))
}

func TestDocumentsAndWorkbooksAreComparedByContent(t *testing.T) {
	a, b := "one", "two"
	before := scripttest.Outcome{Exports: []scriptrun.ExportRequest{{Name: "doc", Format: "markdown", Body: &a}}}
	after := scripttest.Outcome{Exports: []scriptrun.ExportRequest{{Name: "doc", Format: "markdown", Body: &b}}}
	diffs, _ := Compare("r", before, after)
	assert.Equal(t, []string{`run r: output "doc" has the same number of rows with different values`}, details(diffs))

	wb := &tablexlsx.Workbook{}
	book := scripttest.Outcome{Exports: []scriptrun.ExportRequest{{Name: "book", Format: "xlsx", Workbook: wb}}}
	diffs, _ = Compare("r", book, book)
	assert.Empty(t, diffs)
}

func TestStateNotificationsAndTheResultAreCompared(t *testing.T) {
	before := scripttest.Outcome{
		Publishes: []scriptrun.PublishRequest{{Name: "d", Data: 1}, {Name: "old", Data: 1}},
		State:     map[string]any{"n": 1},
		Calls:     []scriptrec.Made{{Tool: "notify", Args: map[string]any{"channel": "ops", "body": "1"}}},
		Result:    json.RawMessage(`1`),
	}
	after := scripttest.Outcome{
		Publishes: []scriptrun.PublishRequest{{Name: "d", Data: 2}, {Name: "fresh", Data: 1}},
		Calls:     []scriptrec.Made{{Tool: "notify", Args: map[string]any{"channel": "ops", "body": "2"}}},
	}
	diffs, _ := Compare("r", before, after)
	assert.ElementsMatch(t, []string{
		`run r: refreshes the data of "d" with different data`,
		`run r: refreshes the data of "fresh", which the saved version did not`,
		`run r: no longer refreshes the data of "old"`,
		`run r: saves a different state: nothing, where the saved version saved {"n":1}`,
		`run r: posts 1 notification(s) to [ops], where the saved version posted 1, or posts different words`,
		`run r: returns nothing from platform.result, where the saved version returned 1`,
	}, details(diffs))
}

func TestANewCallAndANewFailureAreDifferences(t *testing.T) {
	diffs, compared := Compare("r", scripttest.Outcome{}, scripttest.Outcome{Missing: `platform.query("x")`})
	require.True(t, compared)
	assert.Equal(t, []string{`run r: makes platform.query("x"), which the recorded run never made`}, details(diffs))

	diffs, _ = Compare("r", scripttest.Outcome{}, scripttest.Outcome{Failure: "boom"})
	assert.Equal(t, []string{"run r: fails where the saved version finished: boom"}, details(diffs))

	_, compared = Compare("r", scripttest.Outcome{Missing: "x"}, scripttest.Outcome{})
	assert.False(t, compared, "a run the saved version cannot replay says nothing about the change")
}

func TestReachListsOnlyWhatIsNew(t *testing.T) {
	before := scriptrun.Report{Tools: []string{"a"}, Connections: []string{"w"}, Capabilities: []string{"platform.query"}}
	after := scriptrun.Report{
		Tools: []string{"a", "b"}, Connections: []string{}, Destinations: []string{"drop"},
		Capabilities: []string{"platform.query", "platform.call"},
	}
	assert.Equal(t, []string{
		`reaches tool "b", which the saved version does not`,
		`reaches destination "drop", which the saved version does not`,
		`reaches host binding "platform.call", which the saved version does not`,
	}, details(Reach(before, after)))
}

func TestTypeNamesAndNormalizing(t *testing.T) {
	for want, v := range map[string]any{
		"string": "a", "bool": true, "int": int64(1), "float": 1.5,
		"list": []any{}, "dict": map[string]any{}, "uint8": uint8(1),
	} {
		assert.Equal(t, want, typeName(v))
	}
	f := func() {}
	assert.True(t, sameJSON(nil, nil))
	assert.NotNil(t, normalize(f), "a value JSON cannot hold is compared as it is")
	assert.Equal(t, "nothing", orNone(nil))
	assert.Nil(t, raw(nil))
	assert.Equal(t, "{bad", raw(json.RawMessage("{bad")))
}
