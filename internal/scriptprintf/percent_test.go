package scriptprintf

import (
	"testing"

	"go.starlark.net/starlark"
)

// #2713: a width or precision past MaxField is refused, by the save gate's
// check and at run time, rather than allocated.
func TestAFieldPastTheMaximumIsRefused(t *testing.T) {
	for _, f := range []string{"%100000000000000c", "%.9999999f", "%2000000s", "%99999999999999999999d"} {
		if err := CheckPercent(f); err == nil {
			t.Errorf("CheckPercent(%q) accepted a field past %d", f, MaxField)
		}
	}
	for _, f := range []string{"{:2000000}", "{:.2000000f}"} {
		if err := CheckFormat(f); err == nil {
			t.Errorf("CheckFormat(%q) accepted a field past %d", f, MaxField)
		}
	}
	if _, err := Percent(starlark.String("%*s"), starlark.Tuple{starlark.MakeInt(MaxField + 1), starlark.String("x")}); err == nil {
		t.Error("a * width past the maximum was formatted")
	}
	got, err := Percent(starlark.String("%5s"), starlark.String("x"))
	if s, _ := got.(starlark.String); err != nil || s != "    x" {
		t.Errorf("an ordinary width = %v, %v", got, err)
	}
}
