package scriptflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// issue2848Source is #2848's shape: two writers, each defining a nested
// function of one name and passing it to platform.transaction, and a
// module-level function of the same name that only main() sees.
const issue2848Source = `
SAVE_SQL = "WITH up AS (INSERT INTO app.container_rate (rate) VALUES ($1) RETURNING id) INSERT INTO app.rate_change (rate_id) SELECT id FROM up"
VOLUME_SQL = "WITH up AS (INSERT INTO app.line_volume (n) VALUES ($1) RETURNING id) INSERT INTO app.volume_change (volume_id) SELECT id FROM up"

def work():
    """A module-level function of the nested ones' name."""
    return platform.query("SELECT * FROM app.settings", connection = "appdb")

def write_rates(params):
    """Saves the rates."""
    def work(tx):
        return tx.query(SAVE_SQL, params = params)["rows"]
    got = platform.transaction("appdb", work, on_error = "return")
    return got

def write_volume(params):
    """Saves the volume."""
    def work(tx):
        return tx.query(VOLUME_SQL, params = params)["rows"]
    got = platform.transaction("appdb", work, on_error = "return")
    return got

def main():
    """Runs both writers and reads the settings."""
    write_rates({"rate": 1})
    write_volume({"n": 2})
    work()
`

// TestDataRefs_ANestedFunctionResolvesInItsOwnScope is #2848: a name resolves
// where it is written. Inside write_rates, work is the def written in
// write_rates; inside write_volume, the one written there; in main(), the
// module-level one. Before, every work was the last def of that name in the
// file, so write_rates' transaction read write_volume's statements and its
// own writes were lost.
func TestDataRefs_ANestedFunctionResolvesInItsOwnScope(t *testing.T) {
	refs := DataRefs(issue2848Source)
	require.Len(t, refs, 3)
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{}, Writes: []string{"app.container_rate", "app.rate_change"}, Write: true}, refs[0],
		"write_rates' transaction runs the work written in write_rates")
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{}, Writes: []string{"app.line_volume", "app.volume_change"}, Write: true}, refs[1],
		"write_volume's transaction runs the work written in write_volume")
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{"app.settings"}, Writes: []string{}}, refs[2],
		"main()'s work is the module-level one")
}

// TestDerive_NestedFunctionsKeepTheirOwnNames: the graph and the structure
// name a nested function by its def, never by the key the walk tells two of
// them apart with, and list each def's span.
func TestDerive_NestedFunctionsKeepTheirOwnNames(t *testing.T) {
	g := Derive(issue2848Source)
	for _, grp := range g.Groups {
		assert.NotContains(t, grp.ID, ".", "a box id is built from def names")
		assert.NotContains(t, grp.Label, ".", "a box label is the def's signature")
	}
	works := 0
	for _, f := range g.Structure.Functions {
		assert.NotContains(t, f.Name, ".")
		if f.Name == "work" {
			works++
		}
	}
	assert.Equal(t, 3, works, "each def named work is listed with its own span")
}
