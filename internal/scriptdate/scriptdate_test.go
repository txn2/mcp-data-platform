package scriptdate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"
)

// fireTime is the pinned instant a run reads as run.fire_time. The module has
// no clock, so every date in these cases is derived from this one value.
const fireTime = "2026-08-13T14:30:00Z"

// evalPrint evaluates a one-line expression against the date module alone and
// returns what it printed.
//
// It predeclares the module and a run struct carrying the fire time, which is
// the whole environment these functions see. Running them without the engine is
// the reason this package is separate from it.
func evalPrint(t *testing.T, expr string) (string, error) {
	t.Helper()
	var out strings.Builder
	thread := &starlark.Thread{
		Name:  "t",
		Print: func(_ *starlark.Thread, msg string) { _, _ = out.WriteString(msg + "\n") },
	}
	env := starlark.StringDict{
		"date": Module,
		"run": starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
			"fire_time": starlark.String(fireTime),
		}),
	}
	_, err := starlark.ExecFileOptions(&syntax.FileOptions{}, thread, "t.star", "print("+expr+")", env)
	return out.String(), err
}

func TestDateModule(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want string
	}{
		{"of a timestamp", `date.of(run.fire_time)`, "2026-08-13\n"},
		{"of a date passes through", `date.of("2026-08-13")`, "2026-08-13\n"},
		{"parse normalizes", `date.parse("2026-08-13")`, "2026-08-13\n"},
		{"format tokens", `date.format("2026-08-13", "YYYY/MM/DD")`, "2026/08/13\n"},
		{"format partial", `date.format("2026-08-13", "YYYY-MM")`, "2026-08\n"},
		{"add days forward", `date.add_days("2026-08-13", 3)`, "2026-08-16\n"},
		{"add days back over a month", `date.add_days("2026-08-01", -1)`, "2026-07-31\n"},
		{"add months", `date.add_months("2026-08-13", 2)`, "2026-10-13\n"},
		{"add months normalizes", `date.add_months("2026-01-31", 1)`, "2026-03-03\n"},
		{"diff days", `date.diff_days("2026-08-01", "2026-08-13")`, "12\n"},
		{"diff days negative", `date.diff_days("2026-08-13", "2026-08-01")`, "-12\n"},
		{"start of month", `date.start_of_month("2026-08-13")`, "2026-08-01\n"},
		{"weekday", `date.weekday("2026-08-13")`, "thursday\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evalPrint(t, tc.expr)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDateModule_Refusals(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want string
	}{
		{"of junk", `date.of("not a time")`, "neither an RFC 3339 timestamp nor"},
		{"parse junk", `date.parse("13/08/2026")`, "YYYY-MM-DD"},
		{"format junk", `date.format("x", "YYYY")`, "YYYY-MM-DD"},
		{"add days junk", `date.add_days("x", 1)`, "YYYY-MM-DD"},
		{"add months junk", `date.add_months("x", 1)`, "YYYY-MM-DD"},
		{"diff start junk", `date.diff_days("x", "2026-08-13")`, "YYYY-MM-DD"},
		{"diff end junk", `date.diff_days("2026-08-13", "x")`, "YYYY-MM-DD"},
		{"start of month junk", `date.start_of_month("x")`, "YYYY-MM-DD"},
		{"weekday junk", `date.weekday("x")`, "YYYY-MM-DD"},
		// An argument the binding cannot unpack at all, which reads as the
		// call it came from rather than as a bare argument name.
		{"wrong argument type", `date.parse(13)`, "in date.parse"},
		{"unknown argument name", `date.of(when="x")`, "in date.of"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := evalPrint(t, tc.expr)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestDateModule_HasNoClock is the determinism guarantee stated as a test: the
// module offers date arithmetic and no way to ask what time it is.
func TestDateModule_HasNoClock(t *testing.T) {
	for _, expr := range []string{"date.now()", "date.today()", "date.utcnow()"} {
		_, err := evalPrint(t, expr)
		require.Error(t, err, "%s must not exist", expr)
	}
}
