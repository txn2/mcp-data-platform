package scriptrun

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRun_CallsMain is #1944: a script whose work is in main() is run by the
// platform calling main() after the module loads, and what main() reports
// through platform.result is the run's result, the same as the work written at
// the top level produces.
func TestRun_CallsMain(t *testing.T) {
	const inMain = `ROWS_WANTED = 1

def total(rows):
    """Adds up the totals."""
    return sum([r["total"] for r in rows])

def main():
    """Reports the total."""
    rows = platform.query("SELECT 1", connection = "primary")["rows"]
    platform.result({"total": total(rows), "wanted": ROWS_WANTED})
    return "ignored"
`
	const atTop = `rows = platform.query("SELECT 1", connection = "primary")["rows"]
platform.result({"total": sum([r["total"] for r in rows]), "wanted": 1})
`
	caller := &recordingCaller{}
	fromMain, err := execute(t, inMain, caller, nil)
	require.NoError(t, err)
	fromTop, err := execute(t, atTop, &recordingCaller{}, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"total": 42, "wanted": 1}`, string(fromMain.Return))
	assert.JSONEq(t, string(fromTop.Return), string(fromMain.Return), "main() reports what the top-level version reports")
	assert.Len(t, caller.calls, 1)
}

// TestRun_MainIsCalledOnce covers the scripts saved before #1944: a script
// that calls its own main() at the bottom runs it once, not twice, and a
// main that takes parameters is the script's own function, not an entry point.
func TestRun_MainIsCalledOnce(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		runs int
	}{
		"top level calls main":         {"def main():\n    print(\"ran\")\n\nmain()\n", 1},
		"main takes a param, called":   {"def main(x):\n    print(\"ran\")\n\nmain(1)\n", 1},
		"main takes a param, uncalled": {"def main(x):\n    print(\"ran\")\n", 0},
		"no main":                      {"print(\"ran\")\n", 1},
		"platform calls main":          {"def main():\n    print(\"ran\")\n", 1},
		"main rebound to a value":      {"def main():\n    print(\"ran\")\n\nmain = 1\n", 0},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := execute(t, tc.src, &recordingCaller{}, nil)
			require.NoError(t, err)
			assert.Equal(t, tc.runs, strings.Count(res.Log, "ran"))
		})
	}
}

// TestRun_MainFailureIsTheRunFailure: an error raised inside main() fails the
// run with the backtrace naming the author's line.
func TestRun_MainFailureIsTheRunFailure(t *testing.T) {
	_, err := execute(t, "def main():\n    \"\"\"Fails.\"\"\"\n    fail(\"stop here\")\n", &recordingCaller{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stop here")
	assert.Contains(t, err.Error(), "test:3")
}
