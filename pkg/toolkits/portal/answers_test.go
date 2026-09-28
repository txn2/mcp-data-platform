package portal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// The contracts are the bodies the handlers answer with, so an answer a
// script's test declares for them is held to what these tools return (#1953).
func TestAnswerContractsRequireWhatTheToolsAlwaysAnswer(t *testing.T) {
	byCall := map[string]toolkit.AnswerContract{}
	for _, c := range (&Toolkit{}).AnswerContracts() {
		require.NotNil(t, c.Schema, c.Tool)
		for _, v := range c.Values {
			byCall[c.Tool+"/"+v] = c
		}
		if len(c.Values) == 0 {
			byCall[c.Tool] = c
		}
	}
	required := map[string][]string{
		ManageResourceToolName + "/" + resourceActionCreate:  {"resource_id", "reference", "uri", "filename"},
		ManageResourceToolName + "/" + resourceActionReplace: {"resource_id", "uri"},
		ManageResourceToolName + "/" + resourceActionGet:     {"found"},
		ManageResourceToolName + "/" + resourceActionList:    {"resources"},
		ManageResourceToolName + "/" + resourceActionDelete:  {"resource_id"},
		ManageResourceToolName + "/" + resourceActionExtract: {"archive"},
		ManageTableToolName + "/" + tableActionRegister:      {"reference", "message"},
		ManageTableToolName + "/" + tableActionList:          {"table_registrations", "total"},
		ManageToolName + "/" + actionShare:                   {"share_id", "permission"},
		ManageToolName + "/" + actionGet:                     {"id"},
		SaveToolName:                                         {"asset_id", "message"},
	}
	for call, fields := range required {
		c, ok := byCall[call]
		require.True(t, ok, "no contract for %s", call)
		for _, f := range fields {
			assert.Contains(t, c.Schema.Required, f, call)
		}
	}
}
