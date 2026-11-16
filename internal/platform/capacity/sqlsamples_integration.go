//go:build integration

package capacity

// SQLSamples renders the references queries narrowed to the keys a listing
// did not find (#2663), for the gate that hands store SQL to a real
// PostgreSQL to parse and plan (#1512). The file is integration-tagged, so it
// is absent from the default build.
func SQLSamples() map[string]string {
	return map[string]string{
		"stillReferencedQuery/portal":    stillReferencedQuery(portalRefsQuery),
		"stillReferencedQuery/resources": stillReferencedQuery(resourceRefsQuery),
	}
}
