package gates

import (
	"fmt"
	"os"
	"testing"
)

// TestMain runs this package's git invocations with no global or system git
// configuration and with user.useConfigOnly set, which is what a CI runner
// has: no identity. A git call that records an identity (a commit, an
// annotated tag) without naming one then fails on a developer's machine as it
// does in CI, instead of passing on the developer's own identity (#2012's
// annotated-tag test passed locally and failed in CI).
func TestMain(m *testing.M) {
	empty, err := os.CreateTemp("", "gates-gitconfig-")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "gates: creating an empty git config:", err)
		os.Exit(1)
	}
	_ = empty.Close()
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL":   empty.Name(),
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_COUNT":    "1",
		"GIT_CONFIG_KEY_0":    "user.useConfigOnly",
		"GIT_CONFIG_VALUE_0":  "true",
	} {
		if err := os.Setenv(k, v); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "gates: setting", k, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.Remove(empty.Name())
	os.Exit(code)
}
