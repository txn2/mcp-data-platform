package apigateway

import (
	"context"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/upstreamwarn"
)

// OperationKey names the catalog operation a call on connection addresses:
// the operation id it named or its method and path matched, else "METHOD
// template", else "" when the catalog declares no such operation. It is how
// an upstream warning and a script's run are filed (#2485, #2486), and never
// the concrete path, which can carry a filled secret.
func (t *Toolkit) OperationKey(ctx context.Context, connection, method, path, operationID string) string {
	c, ok := t.serving(ctx, connection)
	if !ok {
		return ""
	}
	return c.operationKey(method, path, operationID)
}

// CatalogOf names the API catalog connection reads its specs from, or "".
func (t *Toolkit) CatalogOf(ctx context.Context, connection string) string {
	if c, ok := t.serving(ctx, connection); ok {
		return c.cfg.CatalogID
	}
	return ""
}

func (c *conn) operationKey(method, path, operationID string) string {
	if operationID != "" {
		// Only an id the catalog declares: a caller's own string, which
		// could be anything, is never what a call is filed under.
		for _, op := range c.operations {
			if op.OperationID == operationID {
				return operationID
			}
		}
		return ""
	}
	upper := strings.ToUpper(strings.TrimSpace(method))
	if id := c.resolveViaRouter(upper, ensureLeadingSlash(stripQueryAndFragment(path))); id != "" {
		return id
	}
	if tpl := operationTemplate(c, method, path); tpl != "" {
		return upper + " " + tpl
	}
	return ""
}

// withOperation marks ctx with the operation a resolved call addresses, so
// the outbound chain files what the upstream answers under it.
func withOperation(ctx context.Context, c *conn, in InvokeInput, operationID string) context.Context {
	return upstreamwarn.WithOperation(ctx, c.operationKey(in.Method, in.Path, operationID))
}
