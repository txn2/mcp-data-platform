package scriptrun

import (
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/exportlibrary"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// ResourceLibrary is the library a "resources" export files in (#2080).
type ResourceLibrary = exportlibrary.Library

// collectExportLibrary records the library one platform.export call files in,
// or the finding when the source does not state one a reader can see.
func (ins *inspection) collectExportLibrary(call *syntax.CallExpr, line int) {
	lib, finding := exportlibrary.FromCall(call, ins.consts.String, script.DestinationResources)
	if finding != nil {
		ins.findings = append(ins.findings, Finding{Severity: SeverityError, Line: line, Message: finding.Message, Hint: finding.Hint})
	} else if lib != nil {
		ins.libraries[*lib] = true
	}
}
