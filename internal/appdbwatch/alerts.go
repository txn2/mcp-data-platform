package appdbwatch

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/appdbdrift"
	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
)

// The conditions an application database is alerted on, the last part of
// each alert's key.
const (
	ConditionTransactionIDAge  = "transaction_id_age"
	ConditionIdleInTransaction = "idle_in_transaction"
	ConditionConnections       = "connections"
	ConditionInvalidIndexes    = "invalid_indexes"
	ConditionDeadTuples        = "dead_tuples"
	ConditionDrift             = "drift"
)

// percent scales a ratio to a percent.
const percent = 100

// driftListed is how many differences an alert names.
const driftListed = 5

// keyPrefix opens every alert key of connection.
func keyPrefix(connection string) string { return AlertFamily + ":" + connection + ":" }

// Findings judges one connection's sample against the settings. Titles and
// bodies carry counts and object names, never statement or session text.
func Findings(connection string, s Sample, settings Settings, link string) []levelalert.Finding {
	out := []levelalert.Finding{}
	add := func(condition string, t levelalert.Thresholds, v float64, title, body string) {
		lvl := t.Of(v)
		if lvl == "" {
			return
		}
		limit := map[levelalert.Level]float64{levelalert.Warning: t.Warning, levelalert.Critical: t.Critical}[lvl]
		out = append(out, levelalert.Finding{
			Key: keyPrefix(connection) + condition, Level: lvl, Link: link,
			Title: "Application database " + connection + ": " + title,
			Body:  fmt.Sprintf("%s The %s threshold is %s.", body, lvl, trim(limit)),
		})
	}
	add(ConditionTransactionIDAge, settings.TransactionIDAge, float64(s.TransactionIDAge),
		fmt.Sprintf("transaction id age is %d", s.TransactionIDAge),
		"PostgreSQL refuses writes as the age nears 2^31. A vacuum that cannot freeze old rows, usually held back by a "+
			"long transaction or an idle-in-transaction session, lets it climb.")
	add(ConditionIdleInTransaction, settings.IdleInTransactionSeconds, s.LongestIdleSeconds,
		fmt.Sprintf("a session has been idle in a transaction for %.0f seconds", s.LongestIdleSeconds),
		fmt.Sprintf("%d session(s) hold a transaction open with no statement running; each holds its locks and keeps "+
			"vacuum from reclaiming rows. The connection page lists them and can terminate one.", s.IdleInTransaction))
	add(ConditionConnections, settings.ConnectionsPercent, s.ConnectionShare()*percent,
		fmt.Sprintf("%.0f%% of max_connections are in use", s.ConnectionShare()*percent),
		fmt.Sprintf("%d of %d connections are in use.", s.Connections, s.MaxConnections))
	add(ConditionInvalidIndexes, settings.InvalidIndexes, float64(s.InvalidIndexes),
		fmt.Sprintf("%d invalid index(es)", s.InvalidIndexes),
		"A failed CREATE INDEX CONCURRENTLY or REINDEX left an index PostgreSQL maintains on every write and never reads. "+
			"Reindex the table, or drop the index in a migration.")
	add(ConditionDeadTuples, settings.DeadTuplePercent, s.MaxDeadRatio*percent,
		fmt.Sprintf("%.0f%% of %s's rows are dead", s.MaxDeadRatio*percent, s.MaxDeadTable),
		"Rows are updated or deleted faster than autovacuum reclaims them. Vacuum the table, and look at why it churns.")
	// Each schema is its own condition: a schema is one application's, and
	// one schema's drift neither hides nor stands in for another's.
	for _, r := range s.Drift {
		if !r.Checked || len(r.Differences) == 0 {
			continue
		}
		add(ConditionDrift+":"+r.Schema, settings.DriftObjects, float64(len(r.Differences)),
			fmt.Sprintf("%d object(s) in schema %s differ from what its migrations build", len(r.Differences), r.Schema),
			"Changed outside migrations: "+driftText(r)+".")
	}
	return out
}

// driftText names a schema's first differences.
func driftText(r appdbdrift.Result) string {
	parts := []string{}
	for _, d := range r.Differences {
		if len(parts) == driftListed {
			parts = append(parts, fmt.Sprintf("and %d more", len(r.Differences)-driftListed))
			break
		}
		parts = append(parts, fmt.Sprintf("%s %s.%s %s", d.Kind, r.Schema, d.Object, d.Change))
	}
	return strings.Join(parts, ", ")
}

// trim writes a threshold without a trailing .0.
func trim(v float64) string {
	return strings.TrimSuffix(fmt.Sprintf("%.2f", v), ".00")
}
