package scriptsave

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exportsTo is a script whose one export files in the library the args name.
func exportsTo(args string) string {
	return `def main():
    """Exports the orders."""
    platform.export(name="orders", rows=[{"a": 1}], destination="resources", key="d/orders.csv"` + args + `)
`
}

// A save naming a library its author may not write is refused, naming the
// library; one they may write passes this gate (#2080). The rule is the
// resource rule against the saver's persona and roles, which a run of the
// version presents.
func TestASaveIsRefusedALibraryItsAuthorMayNotWrite(t *testing.T) {
	analyst := Caller{Email: "jane@example.com", Persona: "analyst", Roles: []string{"dp_analyst"}}
	financeAdmin := Caller{Email: "fin@example.com", Persona: "analyst", Roles: []string{"dp_analyst", "dp_persona-admin:finance"}}
	admin := Caller{Email: "root@example.com", Persona: "admin", Roles: []string{"dp_admin"}, Admin: true}
	cases := map[string]struct {
		caller  Caller
		args    string
		refused string
	}{
		"own library, anyone":                {analyst, ``, ""},
		"own library named, anyone":          {analyst, `, scope="user"`, ""},
		"own address named, anyone":          {analyst, `, scope="user", scope_id="jane@example.com"`, ""},
		"global, not an administrator":       {analyst, `, scope="global"`, "the global library"},
		"global, an administrator":           {admin, `, scope="global"`, ""},
		"a persona, its persona admin":       {financeAdmin, `, scope="persona", scope_id="finance"`, ""},
		"a persona, not its admin":           {financeAdmin, `, scope="persona", scope_id="sales"`, "the sales persona library"},
		"another person's, not an admin":     {analyst, `, scope="user", scope_id="bob@example.com"`, "the library of bob@example.com"},
		"another person's, an administrator": {admin, `, scope="user", scope_id="bob@example.com"`, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			refusal := checkLibraryReach(Request{Caller: tc.caller}, exportsTo(tc.args))
			if tc.refused == "" {
				assert.Empty(t, refusal)
				return
			}
			assert.Contains(t, refusal, tc.refused)
			assert.Contains(t, refusal, tc.caller.Email+" may not write to")
			assert.Contains(t, refusal, "the source was not saved")
		})
	}
}

// The gate applies the check before the tests run, and names the saver
// generically when the caller has no address.
func TestTheGateRefusesTheLibraryBeforeTheTests(t *testing.T) {
	gate := &Gate{}
	res := gate.Check(context.Background(), Request{
		Name: "daily", Source: exportsTo(`, scope="global"`),
		Caller: Caller{Email: "jane@example.com", Roles: []string{"dp_analyst"}},
	})
	require.True(t, res.Refused())
	assert.Contains(t, res.Refusal, "the global library")
	assert.Nil(t, res.Tests, "refused before the tests ran")

	refusal := checkLibraryReach(Request{}, exportsTo(`, scope="global"`))
	assert.Contains(t, refusal, "which the person saving it may not write to")
}
