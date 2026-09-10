package scriptdraft

import (
	"github.com/txn2/mcp-data-platform/internal/toolwrite"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// ToolkitLister is the live toolkit registry. The resolver below asks it at the
// moment it classifies a call rather than holding a toolkit resolved at
// assembly: connections are added, reloaded and removed while the platform
// runs, and a barrier reading a snapshot would classify against a catalog that
// has moved on. It is the same reason a provenance capture takes the registry.
type ToolkitLister interface {
	All() []registry.Toolkit
}

// apiMethodLookup is the half of the api gateway toolkit a write barrier needs:
// what HTTP method an operation id sends. It is declared here rather than
// imported so this package does not depend on a toolkit to name one method of
// it; pkg/toolkits/apigateway implements it.
type apiMethodLookup interface {
	MethodForOperation(connection, spec, operationID string) (method string, ok bool)
}

// ClassifierOver builds the classifier a draft's write barrier decides with,
// over the deployment's live toolkits (#1664).
//
// It exists here, rather than at each composition root, because both surfaces
// that reach a draft need it and exactly one of them may decide what it is: the
// manage_script tool an agent calls and the editor its owner works in.
//
// A deployment with no api gateway resolves nothing through it, and a call
// addressed by operation id is then classified as a write like anything else
// the table cannot read.
func ClassifierOver(toolkits ToolkitLister) toolwrite.Classifier {
	if toolkits == nil {
		return toolwrite.Classifier{}
	}
	return toolwrite.Classifier{
		ResolveMethod: func(connection, spec, operationID string) (string, bool) {
			for _, tk := range toolkits.All() {
				lookup, ok := tk.(apiMethodLookup)
				if !ok {
					continue
				}
				if method, resolved := lookup.MethodForOperation(connection, spec, operationID); resolved {
					return method, true
				}
			}
			return "", false
		},
	}
}
