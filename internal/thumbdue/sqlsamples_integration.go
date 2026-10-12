//go:build integration

package thumbdue

// SQLSamples renders the sweep's chunk statement for each table it runs on,
// for the gate that hands store SQL to a real PostgreSQL to parse and plan
// (#1512). The file is integration-tagged, so it is absent from the default
// build.
func SQLSamples() map[string]string {
	return map[string]string{
		"markQuery/resources":          markQuery(Resources),
		"markQuery/portal_assets":      markQuery(Assets),
		"markQuery/portal_collections": markQuery(Collections),
	}
}
