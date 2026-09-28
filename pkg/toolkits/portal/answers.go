package portal

import (
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// actionArg is the argument the four tools select an action by.
const actionArg = "action"

// AnswerContracts is what save_asset, manage_asset, manage_table and
// manage_resource always answer, by action (#1953): the bodies a managed
// script's test is held to when it declares a call's answer. An action whose
// answer is assembled per call rather than from one type declares none.
func (*Toolkit) AnswerContracts() []toolkit.AnswerContract {
	return []toolkit.AnswerContract{
		toolkit.ContractFor[saveAssetOutput](SaveToolName, ""),
		toolkit.ContractFor[portal.Asset](ManageToolName, actionArg, actionGet),
		toolkit.ContractFor[shareOutput](ManageToolName, actionArg, actionShare),
		toolkit.ContractFor[tableRegistrationOutput](ManageTableToolName, actionArg, tableActionRegister),
		toolkit.ContractFor[tableListOutput](ManageTableToolName, actionArg, tableActionList),
		toolkit.ContractFor[resourceOutput](ManageResourceToolName, actionArg, resourceActionCreate, resourceActionReplace),
		toolkit.ContractFor[resourceGetOutput](ManageResourceToolName, actionArg, resourceActionGet),
		toolkit.ContractFor[resourceListOutput](ManageResourceToolName, actionArg, resourceActionList),
		toolkit.ContractFor[resourceDeleteOutput](ManageResourceToolName, actionArg, resourceActionDelete),
		toolkit.ContractFor[extractOutput](ManageResourceToolName, actionArg, resourceActionExtract),
	}
}
