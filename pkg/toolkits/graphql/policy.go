package graphql

import (
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/gqlschema"
)

// filterByRoutePolicy drops the operations the caller's persona may not
// invoke. A discovery surface never lists an operation its caller
// cannot call: that is the same treatment the API gateway gives a
// denied endpoint, and it is what keeps a search result from being a
// map of what the caller is refused.
//
// A nil policy (no persona authorizer wired) leaves the set unchanged;
// the platform's tool and connection gates still apply.
func filterByRoutePolicy(ctx context.Context, policy RoutePolicy, connection string, ops []gqlschema.Operation) []gqlschema.Operation {
	if policy == nil {
		return ops
	}
	out := make([]gqlschema.Operation, 0, len(ops))
	for _, op := range ops {
		path := op.PolicyPath()
		if allowed, _ := policy.Allow(ctx, connection, string(op.Kind), path, path); allowed {
			out = append(out, op)
		}
	}
	return out
}

// authorizeDocument refuses a document that invokes any operation the
// caller's persona denies. Every top-level operation the document
// reaches must pass: a mixed document is refused when one of its
// selections is denied, rather than being sent with the denied part
// stripped, because the caller asked for one answer and would
// otherwise be handed a different one silently.
func (*Toolkit) authorizeDocument(ctx context.Context, policy RoutePolicy, c *conn, doc *gqlschema.Document) error {
	kind := doc.Kind()
	if c.cfg.ReadOnly && kind == gqlschema.OperationMutation {
		return fmt.Errorf("connection %q is read_only; mutation documents are refused on it for every persona", c.cfg.ConnectionName)
	}
	if policy == nil {
		return nil
	}
	c.schemaMu.RLock()
	ops := c.operations
	c.schemaMu.RUnlock()
	paths := doc.InvokedPaths(gqlschema.NewPathIndex(ops, kind))
	var denied []string
	for _, p := range paths {
		policyPath := "/" + strings.ReplaceAll(p, ".", "/")
		if allowed, _ := policy.Allow(ctx, c.cfg.ConnectionName, string(kind), policyPath, policyPath); !allowed {
			denied = append(denied, string(kind)+" "+policyPath)
		}
	}
	if len(denied) == 0 {
		return nil
	}
	return fmt.Errorf("your persona's route rules on connection %q deny %s", c.cfg.ConnectionName, strings.Join(denied, ", "))
}
