package trino

import (
	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// AnswerContracts is what trino_execute always answers when it answers JSON
// (#1953): the body a managed script's test is held to when it declares the
// answer a write statement gets.
func (*Toolkit) AnswerContracts() []toolkit.AnswerContract {
	return []toolkit.AnswerContract{
		toolkit.ContractFor[trinotools.QueryOutput](toolExecute, "format", "", "json"),
	}
}
