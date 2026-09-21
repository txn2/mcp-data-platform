package trino

import (
	"context"
	"fmt"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/connprobe"
)

// probeStatement is the smallest question a Trino coordinator can be asked. It
// touches no catalog, so the probe reports the connection rather than whatever
// the connection happens to default to.
const probeStatement = "SELECT 1"

// ProbeConnection opens the named connection and runs SELECT 1 against it,
// which is what a caller means by "does this connection work": the DSN parses,
// the coordinator is reachable, and the credential authenticates.
//
// It is the check that was missing when a connection whose user and password
// were stored as unexpanded "${...}" placeholders was created, listed and read
// back as a healthy connection, and failed every query afterwards at DSN
// construction (#1805).
//
// Implements connprobe.Prober.
func (t *Toolkit) ProbeConnection(ctx context.Context, name string) connprobe.Result {
	client, err := t.execClient(name)
	if err != nil {
		return connprobe.Failure(fmt.Sprintf("connection %q could not be opened", name), err)
	}
	if _, err := client.Query(ctx, probeStatement, trinoclient.QueryOptions{Limit: 1}); err != nil {
		return connprobe.Failure(
			fmt.Sprintf("connection %q is configured but the query engine refused %s", name, probeStatement), err)
	}
	detail := fmt.Sprintf("the query engine answered %s", probeStatement)
	if !t.AcceptsWrites(name) {
		detail += "; this connection is read_only and refuses write-class statements"
	}
	return connprobe.Success(detail)
}

// Verify interface compliance.
var _ connprobe.Prober = (*Toolkit)(nil)
