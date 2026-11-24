package browsersession

import (
	"strings"
	"testing"
)

func TestCheckSettings(t *testing.T) {
	if got := CheckSettings(false, false, false, "bogus", false); got != nil {
		t.Errorf("a disabled session is checked: %v", got)
	}
	got := CheckSettings(true, false, false, "strct", false)
	want := []string{"auth.oidc must be enabled", "signing_key is required", `same_site="strct" is invalid`}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i, w := range want {
		if !strings.Contains(got[i], w) {
			t.Errorf("finding %d is %q, want %q", i, got[i], w)
		}
	}
	if got := CheckSettings(true, true, true, "none", false); len(got) != 1 || !strings.Contains(got[0], "secure=true") {
		t.Errorf("none without secure: %v", got)
	}
	if got := CheckSettings(true, true, true, "lax", false); len(got) != 0 {
		t.Errorf("a good setting is refused: %v", got)
	}
}
