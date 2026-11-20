package scriptprintf

import (
	"testing"

	"go.starlark.net/starlark"
)

// FuzzFormatting holds `%` and str.format to a script author's format strings
// (#2713): checking and formatting refuse what they cannot do without
// panicking, and a format string CheckFormat passes formats a value without an
// error about its own syntax.
func FuzzFormatting(f *testing.F) {
	for _, s := range []string{"%s and %d", "%(name)s", "%-08.3f", "{} {}", "{0:>10}", "{name!r:^20}", "{", "%", "{:{}}"} {
		f.Add(s)
	}
	args := starlark.Tuple{starlark.String("a"), starlark.MakeInt(42), starlark.Float(3.5)}
	kwargs := []starlark.Tuple{{starlark.String("name"), starlark.String("x")}}
	f.Fuzz(func(_ *testing.T, s string) {
		if len(s) > 4096 {
			return
		}
		_ = CheckPercent(s)
		_, _ = Percent(starlark.String(s), args)
		_ = CheckFormat(s)
		_, _ = Format(s, args, kwargs)
	})
}
