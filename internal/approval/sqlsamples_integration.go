//go:build integration

package approval

// SQLSamples renders the statement this package assembles at run time, for
// the gate that hands store SQL to a real PostgreSQL to parse and plan
// (#1512): the listing with no filter and with every filter.
//
// The file is integration-tagged, so it is absent from the default build.
func SQLSamples() map[string]string {
	all, _ := listQuery(Filter{})
	every, _ := listQuery(Filter{Status: StatusPending, Requester: "jane@example.com", ScriptID: "s1", Limit: 10})
	return map[string]string{"list": all, "list filtered": every}
}
