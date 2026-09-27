package scriptrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptguard"
	"github.com/txn2/mcp-data-platform/internal/runstate"
)

// statusCaller answers each call with the next status in its list, as the api
// gateway does: the status is data, never an error.
type statusCaller struct{ statuses []float64 }

func (c *statusCaller) CallTool(context.Context, string, map[string]any) (map[string]any, error) {
	status := c.statuses[0]
	if len(c.statuses) > 1 {
		c.statuses = c.statuses[1:]
	}
	return map[string]any{"status": status, "body": map[string]any{}}, nil
}

const checkedCall = `
def fetch(i):
    res = platform.call("api_invoke_endpoint", {"connection": "nws", "method": "GET", "path": "/p/%d" % i})
    if res["status"] != 200:
        fail("NWS returned %d" % res["status"])
    return res
`

// #1935: a script that checks the status it was handed and fails on a 5xx
// failed because of the upstream, and is recorded so; the backtrace the
// author reads is unchanged.
func TestRun_AFailureStraightAfterAnUpstream5xxIsTheUpstreams(t *testing.T) {
	result, err := execute(t, checkedCall+"fetch(1)\n", &statusCaller{statuses: []float64{500}}, nil)
	require.Error(t, err)
	assert.Equal(t, runstate.CauseUpstream, scriptguard.Cause(err))
	assert.Contains(t, err.Error(), "fail: NWS returned 500")
	assert.Contains(t, err.Error(), "in fetch", "the backtrace is the author's diagnostic, kept whole")
	assert.Contains(t, result.Log, "failed straight after api_invoke_endpoint answered 500 Internal Server Error")
}

// A 5xx the script handled, followed by a call that got through, is not what
// a later failure is blamed on: the rule is the call made last.
func TestRun_AFailureAfterALaterGoodCallIsTheScripts(t *testing.T) {
	src := checkedCall + `
first = platform.call("api_invoke_endpoint", {"connection": "nws", "method": "GET", "path": "/a"})
fetch(2)
fail("the script's own mistake")
`
	_, err := execute(t, src, &statusCaller{statuses: []float64{500, 200}}, nil)
	require.Error(t, err)
	assert.Equal(t, runstate.CauseScript, scriptguard.Cause(err))
}

// A 4xx is the caller's request, not the upstream's failure.
func TestRun_AFailureAfterA4xxIsTheScripts(t *testing.T) {
	_, err := execute(t, checkedCall+"fetch(1)\n", &statusCaller{statuses: []float64{404}}, nil)
	require.Error(t, err)
	assert.Equal(t, runstate.CauseScript, scriptguard.Cause(err))
}

// A limit keeps its own cause after an upstream failure.
func TestRun_ALimitAfterAnUpstream5xxKeepsItsCause(t *testing.T) {
	src := `
platform.call("api_invoke_endpoint", {"connection": "nws", "method": "GET", "path": "/a"})
n = 0
for i in range(100000000):
    n += i
`
	_, err := Run(context.Background(), Options{
		Source: src, Name: "test", RunID: "run_1", FireTime: fireTime, MaxSteps: 1000,
		Caller: &statusCaller{statuses: []float64{503}},
	})
	require.ErrorIs(t, err, ErrStepLimit)
	assert.Equal(t, runstate.CauseScript, scriptguard.Cause(err))
}

// fail(..., retryable=True) declares the failure temporary; without it fail
// is the universe's, text and all (#1935).
func TestFail_Retryable(t *testing.T) {
	for name, tc := range map[string]struct {
		src   string
		cause string
		text  string
	}{
		"declared temporary": {`fail("feed not published", "yet", retryable=True)`, runstate.CauseTransient, "fail: feed not published yet"},
		"declared not":       {`fail("bad input", retryable=False)`, runstate.CauseScript, "fail: bad input"},
		"plain":              {`fail("a", 1, sep="-")`, runstate.CauseScript, "fail: a-1"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := execute(t, tc.src+"\n", &recordingCaller{}, nil)
			require.Error(t, err)
			assert.Equal(t, tc.cause, scriptguard.Cause(err))
			assert.Contains(t, err.Error(), "Error in fail: "+tc.text)
		})
	}
	_, err := execute(t, `fail("x", retryable="yes")`+"\n", &recordingCaller{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retryable must be True or False, not string")
}
