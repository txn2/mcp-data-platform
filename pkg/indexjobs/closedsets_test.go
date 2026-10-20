package indexjobs

import (
	"slices"
	"testing"
)

// The closed sets a metric series is started at zero with name every
// declared value once; a value missing here is a series that appears at its
// first increment and is invisible to a rate over it (#2218).
func TestClosedSetsNameEveryValueOnce(t *testing.T) {
	for name, tc := range map[string]struct{ got, want []string }{
		"triggers": {Triggers(), []string{string(TriggerWrite), string(TriggerReconciler), string(TriggerManualRetry)}},
		"outcomes": {Outcomes(), []string{OutcomeSucceeded, OutcomeRetried, OutcomeFailed, OutcomeSourceGone, OutcomeLeaseLost, OutcomeStoreError}},
	} {
		got := slices.Clone(tc.got)
		slices.Sort(got)
		if len(slices.Compact(got)) != len(tc.got) {
			t.Errorf("%s names a value twice: %v", name, tc.got)
		}
		for _, v := range tc.want {
			if !slices.Contains(tc.got, v) {
				t.Errorf("%s is missing %q", name, v)
			}
		}
	}
}
