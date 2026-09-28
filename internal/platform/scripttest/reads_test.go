package scripttest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestATestReadingOnlyTheRowCountLeavesTheColumnsUnread(t *testing.T) {
	rec, _ := record(t)
	report := runTests(t, weekly+`
def test_weekly():
    testing.replay("run_1")
    main()
    out = testing.outputs()
    assert.eq(out.exports[0].row_count, 3)
    assert.eq(len(out.exports[0].rows), 3)
    assert.eq(len(out.notifies), 1)
`, rec)
	require.True(t, report.OK(), report.Tests[0].Failure)
	assert.Equal(t, []string{
		`output "weekly" column "region"`, `output "weekly" column "n"`,
		"the staged state", `notification "weekly" to ops`,
	}, report.Unread, "columns in the order the output declares them")
}

func TestEveryWayOfReadingAnOutputCounts(t *testing.T) {
	rec, _ := record(t)
	for name, body := range map[string]string{
		"index":           `assert.eq(out.exports[0].rows[0]["region"], "east")` + "\n    " + `assert.eq(out.exports[0].rows[2]["n"], 3)`,
		"get":             `assert.eq(out.exports[0].rows[0].get("region"), "east")` + "\n    " + `assert.eq(out.exports[0].rows[0].get("n", 0), 1)`,
		"whole rows":      `assert.eq(out.exports[0].rows, [{"region": "east", "n": 1}, {"region": "west", "n": 2}, {"region": "north", "n": 3}])`,
		"one whole row":   `assert.eq(out.exports[0].rows[1], {"region": "west", "n": 2})`,
		"items":           `assert.eq(len(out.exports[0].rows[0].items()), 2)`,
		"values":          `assert.eq(sorted([str(v) for v in out.exports[0].rows[0].values()]), ["1", "east"])`,
		"comprehension":   `assert.eq([r["region"] + str(r["n"]) for r in out.exports[0].rows], ["east1", "west2", "north3"])`,
		"export as whole": `assert.true(out.exports[0])`,
	} {
		t.Run(name, func(t *testing.T) {
			report := runTests(t, weekly+`
def test_weekly():
    testing.replay("run_1")
    main()
    out = testing.outputs()
    `+body+`
    assert.eq(out.notifies[0]["body"], "3")
    assert.eq(out.state, {"count": 3})
`, rec)
			require.True(t, report.OK(), report.Tests[0].Failure)
			assert.Empty(t, report.Unread)
		})
	}
}

func TestKeysAndLengthReadNoColumn(t *testing.T) {
	rec, _ := record(t)
	report := runTests(t, weekly+`
def test_weekly():
    testing.replay("run_1")
    main()
    out = testing.outputs()
    row = out.exports[0].rows[0]
    assert.eq(sorted(row.keys()), ["n", "region"])
    assert.eq(len(row), 2)
    assert.eq(sorted([k for k in row]), ["n", "region"])
    assert.true("region" in [k for k in row])
    assert.eq(out.calls[1].args["channel"], "ops")
    assert.eq(out.state, {"count": 3})
`, rec)
	require.True(t, report.OK(), report.Tests[0].Failure)
	assert.Equal(t, []string{`output "weekly" column "region"`, `output "weekly" column "n"`}, report.Unread,
		"reading the notify through calls reads the notification")
}

func TestOneTestMayReadWhatAnotherProduced(t *testing.T) {
	rec, _ := record(t)
	report := runTests(t, weekly+`
def test_count():
    testing.replay("run_1")
    main()
    assert.eq(testing.outputs().exports[0].row_count, 3)

def test_rows():
    testing.replay("run_1")
    main()
    out = testing.outputs()
    assert.eq([r["region"] for r in out.exports[0].rows], ["east", "west", "north"])
    assert.eq([r["n"] for r in out.exports[0].rows], [1, 2, 3])
    assert.eq(out.notifies[0]["title"], "weekly")
    assert.eq(out.state["count"], 3)
`, rec)
	require.True(t, report.OK())
	assert.Empty(t, report.Unread)
}

func TestTrackedValuesBehaveAsTheirContents(t *testing.T) {
	rec, _ := record(t)
	report := runTests(t, weekly+`
def test_values():
    testing.replay("run_1")
    main()
    out = testing.outputs()
    row = out.exports[0].rows[0]
    assert.eq(type(row), "row")
    assert.eq(type(out), "outputs")
    assert.eq(type(out.exports[0]), "export")
    assert.true(row)
    assert.eq(row.get("missing"), None)
    assert.eq(row == out.exports[0].rows[0], True)
    assert.eq(row == {"region": "east", "n": 1}, False)
    assert.contains(str(row), "east")
    assert.contains(str(out.exports[0]), "export(")
    assert.fails(lambda: {row: 1})
    assert.fails(lambda: {out: 1})
    assert.fails(lambda: row.nothing)
    assert.fails(lambda: out.nothing)
    assert.fails(lambda: row < row)
    assert.fails(lambda: row.get())
    assert.fails(lambda: row.keys(1))
    assert.eq(sorted(dir(row)), ["get", "items", "keys", "values"])
    assert.eq(out.state, {"count": 3})
    assert.eq(out.notifies[0]["body"], "3")
`, rec)
	require.True(t, report.OK(), report.Tests[0].Failure)
}

func TestAPublishAndAResultAreReadThroughTheirFields(t *testing.T) {
	report := runDeclared(t, `def main():
    """Refresh the region and hand back the total."""
    platform.publish_data(name = "dash", data = {"total": 3})
    platform.result({"total": 3})
    platform.export(name = "doc", rows = "# hi", format = "markdown")
    platform.export(name = "empty", rows = [], format = "csv")
`+`
def test_reads():
    main()
    out = testing.outputs()
    assert.eq(out.publishes[0].name, "dash")
    assert.eq(out.exports[0].format, "markdown")
`)
	require.True(t, report.OK(), report.Tests[0].Failure)
	assert.Equal(t, []string{`output "empty"`, "platform.result", `published data "dash"`}, report.Unread)

	report = runDeclared(t, `def main():
    """Refresh the region and hand back the total."""
    platform.publish_data(name = "dash", data = {"total": 3})
    platform.result({"total": 3})
`+`
def test_reads():
    main()
    out = testing.outputs()
    assert.eq(out.publishes[0].data, {"total": 3})
    assert.eq(out.result["total"], 3)
`)
	require.True(t, report.OK(), report.Tests[0].Failure)
	assert.Empty(t, report.Unread)
}

func TestARepeatedNotificationIsNamedByItsTurn(t *testing.T) {
	report := runDeclared(t, `def main():
    """Page twice."""
    platform.notify(channel = "ops", title = "page", body = "a")
    platform.notify(channel = "ops", title = "page", body = "b")
`+`
def test_pages():
    testing.answer("notify", {"body": "a"}, {"ok": True})
    testing.answer("notify", {"body": "b"}, {"ok": True})
    main()
    assert.eq(testing.outputs().notifies[0]["body"], "a")
`)
	require.True(t, report.OK(), report.Tests[0].Failure)
	assert.Equal(t, []string{`notification "page" to ops (2)`}, report.Unread)
}

func TestNoOutputsLeavesNothingUnread(t *testing.T) {
	report := runDeclared(t, `def main():
    """Nothing."""
    return None
`+`
def test_nothing():
    main()
    assert.eq(testing.outputs().exports, [])
`)
	require.True(t, report.OK(), report.Tests[0].Failure)
	assert.Equal(t, []string{}, report.Unread)
}

func TestAFailedNotifyIsNoOutputAndAPublishIsNamedByItsAsset(t *testing.T) {
	report := runDeclared(t, `def main():
    """Posts the report, then pages someone about it."""
    platform.publish(channel = "ops", name = "weekly")
    platform.notify(channel = "ops", title = "page", body = "look")
`+`
def test_page_fails():
    testing.answer("notify", {"action": "publish"}, {"ok": True})
    testing.answer("notify", {"action": "send"}, error = "no channel named ops")
    assert.contains(assert.fails(main), "no channel named ops")
    assert.eq(len(testing.outputs().notifies), 2)
`)
	require.True(t, report.OK(), report.Tests[0].Failure)
	assert.Equal(t, []string{`notification "weekly" to ops`}, report.Unread,
		"the publish is named by its asset; the notify that failed sent nothing")
}
