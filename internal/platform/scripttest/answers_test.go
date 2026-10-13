package scripttest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// browserFlow drives a slow web app: a login, nine page scripts while a form
// builds, polling a status until it reads done, and the twenty rows read
// back one call each. Thirty calls, most of them with the same arguments.
const browserFlow = `
WD = "webdriver"

def drive(script):
    return platform.call("api_invoke_endpoint", {"connection": WD, "method": "POST",
        "path": "/session/s1/execute/async", "body": {"script": script, "args": []}})["body"]["value"]

def main():
    """Logs in, builds the form, waits for it, and reads its rows."""
    drive("login()")
    for _ in range(9):
        drive("buildStep()")
    for _ in range(10):
        if drive("status()") == "done":
            break
    rows = [drive("row(%d)" % i) for i in range(18)]
    platform.result({"rows": len(rows), "first": rows[0]})

def fake_login():
    """The login every test of the flow starts from."""
    testing.answer("api_invoke_endpoint", {"body": {"script": "login()", "args": []}},
        {"status": 200, "body": {"value": "ok"}})

def fake_form(steps = 9):
    """The steps building the form, each answered alike."""
    testing.answer("api_invoke_endpoint", {"body": {"script": "buildStep()", "args": []}},
        {"status": 200, "body": {"value": None}}, times = steps)

def test_reads_the_form_once_it_is_built():
    fake_login()
    fake_form()
    testing.answers("api_invoke_endpoint", {"body": {"script": "status()", "args": []}}, [
        {"status": 200, "body": {"value": "building"}},
        {"status": 200, "body": {"value": "done"}},
    ])
    for i in range(18):
        testing.answer("api_invoke_endpoint", {"body": {"script": "row(%d)" % i, "args": []}},
            {"status": 200, "body": {"value": "row %d" % i}})
    main()
    out = testing.outputs()
    assert.eq(len(out.calls), 30)
    assert.eq(out.result, {"rows": 18, "first": "row 0"})

def test_an_answer_used_up_falls_through():
    fake_login()
    fake_form(steps = 8)
    assert.fails(main)
`

// TestARepeatedFlowIsDeclaredThroughSharedHelpers is #2116's criterion through
// the real test runner: a thirty-call flow is declared with a shared helper,
// times= and a list of successive answers.
func TestARepeatedFlowIsDeclaredThroughSharedHelpers(t *testing.T) {
	report, err := Run(context.Background(), Request{Source: browserFlow, Name: "browser"})
	require.NoError(t, err)
	require.Len(t, report.Tests, 2)
	for _, r := range report.Tests {
		assert.True(t, r.Passed, "%s: %s", r.Name, r.Failure)
	}
}

func TestDeclaredAnswersRefuseWhatTheyCannotDeclare(t *testing.T) {
	cases := map[string]string{
		"times zero":         `testing.answer("t", {}, {"a": 1}, times = 0)`,
		"times too many":     `testing.answer("t", {}, {"a": 1}, times = 1001)`,
		"answers empty":      `testing.answers("t", {}, [])`,
		"answers not a dict": `testing.answers("t", {}, [1])`,
		"answers not a list": `testing.answers("t", {}, {"a": 1})`,
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			report, err := Run(context.Background(), Request{Name: "s", Source: `
def main():
    """Prints."""
    print(1)

def test_it():
    ` + call + `
    main()
`})
			require.NoError(t, err)
			require.Len(t, report.Tests, 1)
			assert.False(t, report.Tests[0].Passed)
		})
	}
}
