package pathtemplate

import (
	"strings"
	"testing"
)

// FuzzMatch holds template matching to an uploaded spec and a request path
// (#2713): a template with no placeholder matches itself, a concrete path never
// matches a template with a different number of segments, and a caller path
// ValidateCallerPath accepts has no dot segment.
func FuzzMatch(f *testing.F) {
	for _, tpl := range benchTemplates {
		f.Add("/customers/c-1/orders/o-2", tpl)
	}
	f.Add("/a/../b", "/a/{x}")
	f.Fuzz(func(t *testing.T, concrete, template string) {
		got := Match(concrete, template)
		if got && len(Split(concrete)) != len(Split(template)) {
			t.Fatalf("Match(%q, %q) with different segment counts", concrete, template)
		}
		if CountPlaceholders(template) == 0 && !strings.ContainsAny(template, "{}?#") && template != "" && !Match(template, template) {
			t.Fatalf("a literal template does not match itself: %q", template)
		}
		if ValidateCallerPath(concrete) == nil {
			for seg := range strings.SplitSeq(StripQuery(concrete), "/") {
				if seg == ".." {
					t.Fatalf("ValidateCallerPath accepted %q with a dot segment", concrete)
				}
			}
		}
	})
}
