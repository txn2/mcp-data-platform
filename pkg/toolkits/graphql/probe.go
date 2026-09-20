package graphql

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/connprobe"
	"github.com/txn2/mcp-data-platform/internal/gqlschema"
)

// ProbeConnection sends the named connection's endpoint an introspection
// query and reports what came back (#1805).
//
// Reachability and the credential are the question, not introspection itself:
// an endpoint that answers at all — including one that answers a GraphQL error
// because introspection is disabled — has proved the URL resolves, TLS
// completes and the credential was accepted, which is everything a caller
// wants to know before wiring work onto the connection. So a transport failure
// is the only failure, and an endpoint refusing introspection is reported as a
// success that says so, since that connection's schema comes from an upload or
// a catalog rather than from the wire.
//
// Implements connprobe.Prober.
func (t *Toolkit) ProbeConnection(ctx context.Context, name string) connprobe.Result {
	c, _, ok := t.lookup(name)
	if !ok {
		return connprobe.Failure(fmt.Sprintf("connection %q is not served by this toolkit", name), nil)
	}
	res, err := t.execute(ctx, c, graphQLRequest{Query: gqlschema.IntrospectionQuery})
	if err != nil {
		return connprobe.Failure(
			fmt.Sprintf("connection %q could not reach %s", name, c.cfg.EndpointURL), err)
	}
	detail := fmt.Sprintf("%s answered with HTTP %d", c.cfg.EndpointURL, res.status)
	if err := introspectionFailure(res); err != nil {
		detail += fmt.Sprintf("; it declines introspection (%s), so this connection's schema comes "+
			"from an upload or a catalog rather than from the endpoint", err.Error())
	}
	if c.cfg.ReadOnly {
		detail += "; this connection is read_only and refuses mutation documents"
	}
	return connprobe.Success(detail)
}

// Verify interface compliance.
var _ connprobe.Prober = (*Toolkit)(nil)
