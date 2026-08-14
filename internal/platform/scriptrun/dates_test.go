package scriptrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// evalPrint runs a one-line expression and returns what it printed.
func evalPrint(t *testing.T, expr string) (string, error) {
	t.Helper()
	result, err := Run(context.Background(), Options{
		Source: "print(" + expr + ")", Name: "t", RunID: "r", FireTime: fireTime,
	})
	require.NotNil(t, result)
	return result.Log, err
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
