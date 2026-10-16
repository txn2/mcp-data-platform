//go:build integration

package acceptance

import "time"

// What the webhook criteria of #2133, #2134 and #2140 share. They were
// declared beside #2132's criteria, which #2140 removed along with the merge
// of the open window they tested; the names are kept so the files that use
// them are unchanged.

const issue2132Secret = "whsec_2132"

// issue2132AwayFromTheHour waits, when the current hour ends within three
// minutes, for the next one, so the events of a criterion land in one window.
func issue2132AwayFromTheHour() {
	now := time.Now().UTC()
	if left := now.Truncate(time.Hour).Add(time.Hour).Sub(now); left < 3*time.Minute {
		time.Sleep(left + 5*time.Second)
	}
}
