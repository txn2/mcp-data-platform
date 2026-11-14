//go:build integration

package callrun

// SQLSamples renders the statements this package assembles, for the gate that
// hands the platform's SQL to a real PostgreSQL to parse and plan (#1512): a
// watch run's insert joins the script store's state-at-creation clause, which
// the extractor cannot fold across packages.
func SQLSamples() map[string]string {
	return map[string]string{
		"insertWatch":  insertWatchSQL,
		"pendingWatch": pendingWatchSQL,
		"recentCalls":  recentSQL,
	}
}
