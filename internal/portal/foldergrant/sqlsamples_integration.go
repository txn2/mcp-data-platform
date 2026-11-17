//go:build integration

package foldergrant

// SQLSamples renders each statement this package assembles at run time, for
// the gate that hands store SQL to a real PostgreSQL to parse and plan
// (#1512): the queries built from the segment rule (grantsource.UnderSQL).
func SQLSamples() map[string]string {
	return map[string]string{
		"listQuery":         listQuery,
		"folderExistsQuery": folderExistsQuery,
		"sitesQuery":        sitesQuery,
	}
}
