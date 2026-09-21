package gateway

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/connprobe"
)

// ProbeConnection lists the named upstream's tools over the live session, which
// is the request every proxied tool call travels, so a success means calls work
// right now rather than that a dial once succeeded (#1805).
//
// A connection registered without a live client — the shape an OAuth
// connection has before anyone has authorized it — is reported as a failure
// naming that, because it is exactly the state where a listing looks complete
// and a call cannot be made.
//
// Implements connprobe.Prober.
func (t *Toolkit) ProbeConnection(ctx context.Context, name string) connprobe.Result {
	tools, err := t.TestLiveConnection(ctx, name)
	if err != nil {
		return connprobe.Failure(fmt.Sprintf("connection %q did not answer a tools/list", name), err)
	}
	return connprobe.Success(fmt.Sprintf("the upstream answered a tools/list with %d tool(s)", len(tools)))
}

// Verify interface compliance.
var _ connprobe.Prober = (*Toolkit)(nil)
