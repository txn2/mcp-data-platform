package postgres

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/sqltag"
)

// TestPlatformStatementsCarryTheTag holds every statement the toolkit runs
// for its own bookkeeping to sqltag.Platform, which is how the health
// report leaves them out of the application's statements (#2528).
func TestPlatformStatementsCarryTheTag(t *testing.T) {
	for name, sql := range map[string]string{
		"typeQuery": typeQuery, "attrQuery": attrQuery, "ownQuery": ownQuery, "lintTargetSQL": lintTargetSQL,
		"planChangesSQL": planChangesSQL, "planLocksSQL": planLocksSQL, "claimsSQL": claimsSQL,
		"roleProbeSQL": roleProbeSQL, "outboxPrivilegeSQL": outboxPrivilegeSQL,
		"describeTablesSQL": describeTablesSQL, "describeColumnsSQL": describeColumnsSQL,
		"describeConstraintsSQL": describeConstraintsSQL, "describeIndexesSQL": describeIndexesSQL,
		"describeServerSQL": describeServerSQL, "describeExtensionsSQL": describeExtensionsSQL,
		"describeAvailableSQL": describeAvailableSQL, "describeFunctionsSQL": describeFunctionsSQL,
		"describeTriggersSQL": describeTriggersSQL, "describeViewsSQL": describeViewsSQL,
		"describePoliciesSQL": describePoliciesSQL, "describePartitionsSQL": describePartitionsSQL,
		"describeEnumsSQL": describeEnumsSQL, "describeDomainsSQL": describeDomainsSQL,
	} {
		assert.True(t, strings.HasPrefix(sql, sqltag.Platform), name)
	}
}
