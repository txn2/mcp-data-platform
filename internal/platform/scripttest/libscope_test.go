package scripttest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testing.outputs() carries the library a "resources" output is filed in
// (#2080), so a test asserts on where the script writes.
func TestOutputsCarryTheLibrary(t *testing.T) {
	report := runDeclared(t, `def main():
    """Export the orders where the team reads them."""
    platform.export(name="shared", rows=[{"a": 1}], destination="resources", key="d/s.csv", scope="persona", scope_id="finance")
    platform.export(name="mine", rows=[{"a": 1}], destination="resources", key="d/m.csv")
    platform.export(name="asset", rows=[{"a": 1}])

def test_library():
    main()
    out = testing.outputs()
    assert.eq(out.exports[0].scope, "persona")
    assert.eq(out.exports[0].scope_id, "finance")
    assert.eq(out.exports[1].scope, "user")
    assert.eq(out.exports[1].scope_id, "")
    assert.eq(out.exports[2].scope, "")
    assert.eq([e.rows for e in out.exports], [[{"a": 1}], [{"a": 1}], [{"a": 1}]])
`)
	require.Len(t, report.Tests, 1)
	assert.True(t, report.Tests[0].Passed, report.Tests[0].Failure)
}
