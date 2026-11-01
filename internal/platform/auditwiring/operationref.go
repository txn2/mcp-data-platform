package auditwiring

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
	"github.com/txn2/mcp-data-platform/internal/platform/provenance"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/connid"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// apiConnectionKind is the connection kind api calls run on and the catalog
// names their operations under.
const apiConnectionKind = "api"

// operationIDResolver is what an api gateway toolkit answers: the operation id
// a method and path on one of its connections matched in that connection's
// specs, or "" when it owns no such connection or nothing matched.
type operationIDResolver interface {
	ResolveOperationID(ctx context.Context, connection, method, path string) string
}

// operationRefs builds the call catalog's operation reference for an api call
// from the live toolkits: the operation id the call named, or the one its
// method and path matched, under the connection's instance name, as the
// catalog scan names the operation's dataset. Toolkits are listed on every
// call, because connections are added and removed while the platform runs. It
// is nil without a toolkit registry, which leaves api records with their
// resource target alone (#2384).
//
// An operation id two of a connection's specs share is cataloged under its
// spec and id; the reference built here names the id alone and finds no such
// dataset, which is the same answer as an operation the catalog does not hold.
func operationRefs(toolkits provenance.ToolkitLister) callrecord.OperationRefBuilder {
	if toolkits == nil {
		return nil
	}
	return func(ctx context.Context, connection, method, path, operationID string) string {
		all := toolkits.All()
		c, ok := connid.NewResolver(all, nil).ByBound(apiConnectionKind, connid.Bound(connection))
		if !ok {
			return ""
		}
		if operationID == "" && path != "" {
			operationID = resolveOperationID(ctx, all, connection, method, path)
		}
		if operationID == "" {
			return ""
		}
		return catalog.DatasetRef(apiConnectionKind, string(c.Instance), operationID).String()
	}
}

// resolveOperationID asks each api gateway toolkit which operation a method
// and path on the connection matched, and returns the first answer.
func resolveOperationID(ctx context.Context, all []registry.Toolkit, connection, method, path string) string {
	for _, tk := range all {
		if r, ok := tk.(operationIDResolver); ok {
			if id := r.ResolveOperationID(ctx, connection, method, path); id != "" {
				return id
			}
		}
	}
	return ""
}
