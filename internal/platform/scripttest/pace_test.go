package scripttest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pollingSource polls a slow upstream every 10 s for up to 5 minutes with
// short calls, which is #2112's whole point.
const pollingSource = `
def main():
    """Waits for the report the upstream is building."""
    for attempt in range(30):
        status = platform.call("api_invoke_endpoint", {"connection": "app", "method": "GET", "path": "/report"})
        if status["body"]["state"] == "ready":
            return attempt
        if platform.remaining_ms() < 15000:
            fail("the report was not ready in time")
        platform.sleep(10)
    fail("the report was never ready")

def test_polls_until_the_report_is_ready():
    for state in ["building", "building", "ready"]:
        testing.answer("api_invoke_endpoint", {"path": "/report"}, {"status": 200, "body": {"state": state}})
    testing.set_run(remaining_ms = 300000)
    assert.eq(main(), 2)

def test_stops_before_the_deadline():
    testing.answer("api_invoke_endpoint", {"path": "/report"}, {"status": 200, "body": {"state": "building"}})
    testing.set_run(remaining_ms = 10000)
    assert.fails(main, "not ready in time")
`

// TestAPollingScriptsTestRunsInstantly is #2112's second criterion through the
// real test runner: thirty waits of ten seconds cost nothing in a test.
func TestAPollingScriptsTestRunsInstantly(t *testing.T) {
	start := time.Now()
	report, err := Run(context.Background(), Request{Source: pollingSource, Name: "poll"})
	require.NoError(t, err)
	require.Len(t, report.Tests, 2)
	for _, r := range report.Tests {
		assert.True(t, r.Passed, "%s: %s", r.Name, r.Failure)
	}
	assert.Less(t, time.Since(start), 5*time.Second, "a test does not wait")
}
