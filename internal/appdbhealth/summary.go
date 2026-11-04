package appdbhealth

import (
	"context"
	"fmt"
	"strings"
)

// MinTuplesForRatio is how many rows a table holds before its dead tuple ratio
// means anything: a table of ten rows with five deleted is not a design
// problem.
const MinTuplesForRatio = 1000

// idleInTransaction is the state prefix of a session holding a transaction
// open with no statement running ("idle in transaction", and its "(aborted)"
// form).
const idleInTransaction = "idle in transaction"

// Summary is a report reduced to numbers: what the history keeps, the gauges
// show and the alerts are judged on. It carries no statement or session text,
// no role and no value.
type Summary struct {
	SizeBytes        int64   `json:"size_bytes"`
	TransactionIDAge int64   `json:"transaction_id_age"`
	Connections      int64   `json:"connections"`
	MaxConnections   int64   `json:"max_connections"`
	CacheHitRatio    float64 `json:"cache_hit_ratio"`
	// IdleInTransaction counts the sessions idle in a transaction, and
	// LongestIdleSeconds is how long the longest has been.
	IdleInTransaction  int     `json:"idle_in_transaction"`
	LongestIdleSeconds float64 `json:"longest_idle_seconds"`
	LongRunning        int     `json:"long_running"`
	Blocked            int     `json:"blocked"`
	UnusedIndexes      int     `json:"unused_indexes"`
	DuplicateIndexes   int     `json:"duplicate_indexes"`
	InvalidIndexes     int     `json:"invalid_indexes"`
	// MaxDeadRatio is the highest dead tuple ratio of a table holding at
	// least MinTuplesForRatio rows, and MaxDeadTable that table.
	MaxDeadRatio   float64 `json:"max_dead_ratio"`
	MaxDeadTable   string  `json:"max_dead_table,omitempty"`
	ReplicaLagByte int64   `json:"replica_lag_bytes"`
	Errors         int     `json:"errors"`
}

// ConnectionShare is the share of max_connections in use, 0 when unknown.
func (s Summary) ConnectionShare() float64 {
	if s.MaxConnections <= 0 {
		return 0
	}
	return float64(s.Connections) / float64(s.MaxConnections)
}

// Summarize reduces r.
func Summarize(r *Report) Summary {
	s := Summary{
		SizeBytes: r.Server.SizeBytes, TransactionIDAge: r.Server.TransactionIDAge,
		Connections: r.Connections.Total, MaxConnections: r.Connections.Max, CacheHitRatio: r.Server.CacheHitRatio,
		Blocked: len(r.Blocking), UnusedIndexes: len(r.Indexes.Unused), DuplicateIndexes: len(r.Indexes.Duplicate),
		InvalidIndexes: len(r.Indexes.Invalid), Errors: len(r.Errors),
	}
	for _, x := range r.Sessions {
		if strings.HasPrefix(x.State, idleInTransaction) {
			s.IdleInTransaction++
			s.LongestIdleSeconds = max(s.LongestIdleSeconds, x.StateSeconds)
			continue
		}
		s.LongRunning++
	}
	for _, t := range r.Tables {
		if t.LiveTuples+t.DeadTuples >= MinTuplesForRatio && t.DeadRatio > s.MaxDeadRatio {
			s.MaxDeadRatio, s.MaxDeadTable = t.DeadRatio, t.Schema+"."+t.Name
		}
	}
	for _, rep := range r.Replication {
		s.ReplicaLagByte = max(s.ReplicaLagByte, rep.LagBytes)
	}
	return s
}

// Finding is one thing a build can act on in a schema its migrations made, as
// manage_schema describe reports it (#2409). It names objects and numbers,
// never statement text.
type Finding struct {
	// Kind is unused_index, duplicate_index or dead_tuples.
	Kind   string `json:"kind"`
	Table  string `json:"table"`
	Index  string `json:"index,omitempty"`
	Detail string `json:"detail"`
}

// The kinds of finding.
const (
	FindingUnusedIndex    = "unused_index"
	FindingDuplicateIndex = "duplicate_index"
	FindingDeadTuples     = "dead_tuples"
)

// Findings reads what describe reports about schema beside its objects: an
// index never scanned, indexes that index the same thing, and a table whose
// dead tuple ratio is past deadRatio. A read that fails reports nothing
// rather than failing describe.
func Findings(ctx context.Context, q Querier, connection, schema string, deadRatio float64) []Finding {
	out := []Finding{}
	r := &Report{
		Tables: []TableStats{}, Indexes: IndexReport{Unused: []IndexRef{}, Duplicate: []DuplicateIndexes{}},
		Errors: []SectionError{},
	}
	rd := reader{ctx: ctx, q: q, connection: connection, r: r, params: []any{schema}}
	rd.section(SectionIndexes, inSchema(unusedIndexesSQL, "s.schemaname"), rd.unusedIndex)
	rd.section(SectionIndexes, inSchema(duplicateIndexesSQL, "n.nspname"), rd.duplicateIndex)
	rd.section(SectionTables, inSchema(tablesSQL, "s.schemaname"), rd.table)
	for _, i := range r.Indexes.Unused {
		out = append(out, Finding{Kind: FindingUnusedIndex, Table: i.Table, Index: i.Name, Detail: fmt.Sprintf(
			"never scanned since the statistics were reset and backs no constraint; it costs every write to %s "+
				"(%d bytes). Drop it in a migration if no query needs it", i.Table, i.SizeBytes)})
	}
	for _, d := range r.Indexes.Duplicate {
		out = append(out, Finding{Kind: FindingDuplicateIndex, Table: d.Table, Index: strings.Join(d.Names, ", "), Detail: fmt.Sprintf(
			"%d indexes index the same columns the same way; keep one and drop the rest in a migration", len(d.Names))})
	}
	for _, t := range r.Tables {
		if t.LiveTuples+t.DeadTuples < MinTuplesForRatio || t.DeadRatio < deadRatio {
			continue
		}
		out = append(out, Finding{Kind: FindingDeadTuples, Table: t.Name, Detail: fmt.Sprintf(
			"%.0f%% of its rows are dead (%d of %d): rows are updated or deleted faster than autovacuum reclaims them. "+
				"Update fewer columns that are indexed, move finished rows out, or partition by age so old rows are dropped "+
				"rather than deleted", t.DeadRatio*percent, t.DeadTuples, t.LiveTuples+t.DeadTuples)})
	}
	return out
}

// percent scales a ratio for a sentence.
const percent = 100

// inSchema narrows one of the report's statements to the schema bound as $1.
func inSchema(sql, column string) string {
	cond := column + " = $1"
	for _, kw := range []string{"\nGROUP BY", "\nORDER BY"} {
		if i := strings.Index(sql, kw); i >= 0 {
			where := " AND "
			if !strings.Contains(sql[:i], "WHERE") {
				where = " WHERE "
			}
			return sql[:i] + where + cond + sql[i:]
		}
	}
	return sql
}
