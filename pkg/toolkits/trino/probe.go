package trino

import (
	"context"
	"errors"
	"fmt"
	"strings"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/connprobe"
	"github.com/txn2/mcp-data-platform/internal/scratchcatalog"
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
// On a connection that accepts writes and names a scratch target it also
// checks what webhook sources need of that catalog, which otherwise surfaced
// only when an administrator first created a source (#1888); see probeScratch.
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
		return connprobe.Success(detail + "; this connection is read_only and refuses write-class statements")
	}
	target, ok := t.ScratchTarget(name)
	if !ok {
		return connprobe.Success(detail)
	}
	return probeScratch(ctx, client, name, target, detail)
}

// probeTable is a table name no deployment holds. Each partition procedure is
// called on it, so the engine answers the question the probe asks -- may this
// user call the procedure, and does the catalog allow it -- and then refuses
// on the missing table without having changed anything.
const probeTable = "mcp_platform_connection_test_absent"

// probeScratch checks what the scratch features need of a connection's
// catalog beyond reading it (#1888): that its Trino user may execute the three
// partition procedures and that the catalog allows register_partition. Trino
// checks access control before a procedure runs and the Hive connector checks
// its property before it looks up the table, so an answer of "table not found"
// is the answer that both are in place.
//
// It reports every missing setting at once rather than the first, because an
// operator fixing a rules file wants the whole list before restarting anything.
func probeScratch(ctx context.Context, client *trinoclient.Client, name string, target ScratchConfig, detail string) connprobe.Result {
	var remedies, answers []string
	for _, procedure := range scratchcatalog.Procedures {
		_, err := client.Query(ctx, probeCall(target, procedure), trinoclient.QueryOptions{Limit: 1})
		if err == nil || tableMissing(err) {
			continue
		}
		answers = append(answers, err.Error())
		if denied, ok := scratchcatalog.Denied(err); ok {
			remedies = append(remedies, scratchcatalog.DeniedRemedy(name, denied))
			continue
		}
		if scratchcatalog.Disabled(err) {
			remedies = append(remedies, scratchcatalog.DisabledRemedy(target.Catalog))
			continue
		}
		remedies = append(remedies, fmt.Sprintf("calling %s.system.%s failed", target.Catalog, procedure))
	}
	if len(remedies) > 0 {
		return connprobe.Failure(
			detail+", but webhook sources cannot run on this scratch connection: "+strings.Join(remedies, "; "),
			errors.New(strings.Join(answers, "; ")))
	}
	return connprobe.Success(fmt.Sprintf("%s; the scratch catalog %s allows this connection to call %s",
		detail, target.Catalog, strings.Join(scratchcatalog.Procedures, ", ")))
}

// probeCall renders a call of one partition procedure on probeTable.
func probeCall(target ScratchConfig, procedure string) string {
	args := quoteLiteral(target.Schema) + ", " + quoteLiteral(probeTable)
	switch procedure {
	case scratchcatalog.SyncPartitionMetadata:
		args += ", 'ADD'"
	case scratchcatalog.RegisterPartition:
		args += ", ARRAY['dt'], ARRAY['probe'], 's3://" + probeTable + "/'"
	default:
		args += ", ARRAY['dt'], ARRAY['probe']"
	}
	return "CALL " + quoteIdentifier(target.Catalog) + ".system." + procedure + "(" + args + ")"
}

// tableMissing reports the engine's answer for a table that does not exist:
// "Table 'uploads.mcp_platform_connection_test_absent' not found".
func tableMissing(err error) bool {
	return strings.Contains(err.Error(), probeTable+"' not found")
}

// Verify interface compliance.
var _ connprobe.Prober = (*Toolkit)(nil)
