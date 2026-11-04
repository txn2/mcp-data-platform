// Package appdbhealth reads an application database's health from its own
// statistics views (#2409): the server and its extensions, table and index
// sizes and their vacuum state, unused, duplicate and invalid indexes,
// transaction id age, connections against the limit, long-running and
// idle-in-transaction sessions, blocking lock chains, the cache hit ratio,
// replication lag, and the statements pg_stat_statements ranks first when the
// extension is installed.
//
// It reads through the postgres toolkit's Query as the connection's own login,
// so the own-database refusal and the reset a released connection runs hold.
// A section the database refuses is reported under Errors with the toolkit's
// value-free summary; the rest of the report stands.
//
// Statement and session text can repeat a literal. Report carries it for the
// administrator reading the report and nothing else: Summary, the form the
// history keeps and the alerts are built from, carries none of it.
package appdbhealth

import (
	"context"
	"strings"
	"time"
)

// Report is one reading of an application database's health.
type Report struct {
	Connection  string    `json:"connection"`
	GeneratedAt time.Time `json:"generated_at"`
	Server      Server    `json:"server"`
	// Extensions are the extensions the database holds.
	Extensions  []Extension       `json:"extensions"`
	Connections ConnectionUse     `json:"connections"`
	Tables      []TableStats      `json:"tables"`
	Indexes     IndexReport       `json:"indexes"`
	Sessions    []Session         `json:"sessions"`
	Blocking    []Blocked         `json:"blocking"`
	Replication []Replica         `json:"replication"`
	Statements  StatementsSection `json:"statements"`
	// Errors names each section the database did not answer, with the
	// toolkit's summary of why. Empty when the report is complete.
	Errors []SectionError `json:"errors"`
}

// Server is the server and the database the connection reaches.
type Server struct {
	Version   string `json:"version"`
	VersionNo int64  `json:"version_num"`
	Database  string `json:"database"`
	SizeBytes int64  `json:"size_bytes"`
	// TransactionIDAge is the age of the database's oldest unfrozen
	// transaction id; PostgreSQL stops accepting writes near 2^31.
	TransactionIDAge int64 `json:"transaction_id_age"`
	// CacheHitRatio is the share of block reads served from shared buffers.
	CacheHitRatio float64 `json:"cache_hit_ratio"`
	InRecovery    bool    `json:"in_recovery"`
	StatsReset    string  `json:"stats_reset,omitempty"`
}

// Extension is one installed extension.
type Extension struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ConnectionUse is the server's client connections against its limit.
type ConnectionUse struct {
	Max     int64            `json:"max"`
	Total   int64            `json:"total"`
	ByState map[string]int64 `json:"by_state"`
}

// TableStats is one table's size and maintenance state.
type TableStats struct {
	Schema      string `json:"schema"`
	Name        string `json:"name"`
	SizeBytes   int64  `json:"size_bytes"`
	RowEstimate int64  `json:"row_estimate"`
	LiveTuples  int64  `json:"live_tuples"`
	DeadTuples  int64  `json:"dead_tuples"`
	// DeadRatio is dead tuples over live and dead together.
	DeadRatio       float64 `json:"dead_ratio"`
	SeqScans        int64   `json:"seq_scans"`
	IndexScans      int64   `json:"index_scans"`
	LastVacuum      string  `json:"last_vacuum,omitempty"`
	LastAutovacuum  string  `json:"last_autovacuum,omitempty"`
	LastAnalyze     string  `json:"last_analyze,omitempty"`
	LastAutoanalyze string  `json:"last_autoanalyze,omitempty"`
	// TransactionIDAge is the age of the table's oldest unfrozen
	// transaction id.
	TransactionIDAge int64 `json:"transaction_id_age"`
}

// IndexRef is one index.
type IndexRef struct {
	Schema    string `json:"schema"`
	Table     string `json:"table"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	Scans     int64  `json:"scans"`
}

// DuplicateIndexes are indexes of one table over the same columns, operator
// classes, expressions and predicate.
type DuplicateIndexes struct {
	Schema     string   `json:"schema"`
	Table      string   `json:"table"`
	Names      []string `json:"names"`
	Definition string   `json:"definition"`
}

// IndexReport lists the indexes worth an operator's look.
type IndexReport struct {
	// Unused were never scanned since the statistics were reset and back no
	// constraint.
	Unused    []IndexRef         `json:"unused"`
	Duplicate []DuplicateIndexes `json:"duplicate"`
	// Invalid were left by a failed CREATE INDEX CONCURRENTLY or REINDEX.
	Invalid []IndexRef `json:"invalid"`
}

// Session is a backend running a statement past the long-running bound, or
// idle in a transaction.
type Session struct {
	PID         int64  `json:"pid"`
	User        string `json:"user"`
	Application string `json:"application"`
	State       string `json:"state"`
	// Seconds is how long the transaction (or, outside one, the statement)
	// has run; StateSeconds how long the session has been in its state.
	Seconds      float64 `json:"seconds"`
	StateSeconds float64 `json:"state_seconds"`
	Wait         string  `json:"wait,omitempty"`
	// Query is the session's current or last statement. It can repeat a
	// literal: it is shown to the administrator and kept nowhere.
	Query string `json:"query"`
}

// Blocked is a backend waiting on a lock others hold.
type Blocked struct {
	PID       int64   `json:"pid"`
	User      string  `json:"user"`
	BlockedBy []int64 `json:"blocked_by"`
	Seconds   float64 `json:"seconds"`
}

// Replica is one standby streaming from the server.
type Replica struct {
	Name       string  `json:"name"`
	State      string  `json:"state"`
	LagBytes   int64   `json:"lag_bytes"`
	LagSeconds float64 `json:"lag_seconds"`
}

// StatementsSection is what pg_stat_statements ranks first by total time.
type StatementsSection struct {
	Installed bool `json:"installed"`
	// Note says why there are no statements: the extension is not installed,
	// or installed and not loaded by shared_preload_libraries.
	Note string      `json:"note,omitempty"`
	Top  []Statement `json:"top"`
}

// Statement is one normalized statement and its totals. Query is
// PostgreSQL's normalized text, which can keep a literal of a DDL or utility
// statement: it is shown to the administrator and kept nowhere.
type Statement struct {
	QueryID string  `json:"query_id"`
	Calls   int64   `json:"calls"`
	TotalMS float64 `json:"total_ms"`
	MeanMS  float64 `json:"mean_ms"`
	Rows    int64   `json:"rows"`
	Query   string  `json:"query"`
}

// SectionError is a section the database did not answer.
type SectionError struct {
	Section string `json:"section"`
	Error   string `json:"error"`
}

// Options bound what a session must do to be listed.
type Options struct {
	// LongRunning is how long a statement runs before its session is listed.
	LongRunning time.Duration
}

// DefaultLongRunning lists a statement running a minute or more.
const DefaultLongRunning = time.Minute

// The sections a report reads, as Errors names them.
const (
	SectionServer      = "server"
	SectionExtensions  = "extensions"
	SectionConnections = "connections"
	SectionTables      = "tables"
	SectionIndexes     = "indexes"
	SectionSessions    = "sessions"
	SectionBlocking    = "blocking"
	SectionReplication = "replication"
	SectionStatements  = "statements"
)

// statStatements is the extension the statements section reads.
const statStatements = "pg_stat_statements"

// Read reads connection's health now.
func Read(ctx context.Context, q Querier, connection string, o Options) *Report {
	if o.LongRunning <= 0 {
		o.LongRunning = DefaultLongRunning
	}
	r := &Report{
		Connection: connection, GeneratedAt: time.Now().UTC(), Extensions: []Extension{},
		Connections: ConnectionUse{ByState: map[string]int64{}}, Tables: []TableStats{},
		Indexes:  IndexReport{Unused: []IndexRef{}, Duplicate: []DuplicateIndexes{}, Invalid: []IndexRef{}},
		Sessions: []Session{}, Blocking: []Blocked{}, Replication: []Replica{},
		Statements: StatementsSection{Top: []Statement{}}, Errors: []SectionError{},
	}
	rd := reader{ctx: ctx, q: q, connection: connection, r: r}
	rd.section(SectionServer, serverSQL, rd.server)
	rd.section(SectionExtensions, extensionsSQL, rd.extension)
	rd.section(SectionConnections, connectionsSQL, rd.connectionState)
	rd.section(SectionTables, tablesSQL, rd.table)
	rd.section(SectionIndexes, unusedIndexesSQL, rd.unusedIndex)
	rd.section(SectionIndexes, duplicateIndexesSQL, rd.duplicateIndex)
	rd.section(SectionIndexes, invalidIndexesSQL, rd.invalidIndex)
	rd.section(SectionSessions, sessionsSQL(o.LongRunning), rd.session)
	rd.section(SectionBlocking, blockingSQL, rd.blocked)
	if !r.Server.InRecovery {
		rd.section(SectionReplication, replicationSQL, rd.replica)
	}
	rd.statements()
	return r
}

// reader fills one report.
type reader struct {
	ctx        context.Context
	q          Querier
	connection string
	r          *Report
	// params are bound to every statement's placeholders.
	params []any
}

// section runs sql and hands each row to each, recording a failure under name.
func (rd *reader) section(name, sql string, each func(row)) {
	rows, err := query(rd.ctx, rd.q, rd.connection, sql, rd.params...)
	if err != nil {
		rd.r.Errors = append(rd.r.Errors, SectionError{Section: name, Error: err.Error()})
		return
	}
	for _, x := range rows {
		each(x)
	}
}

func (rd *reader) server(x row) {
	rd.r.Server = Server{
		Version: x.str("version"), VersionNo: x.num("version_num"), Database: x.str("database"),
		SizeBytes: x.num("size_bytes"), TransactionIDAge: x.num("xid_age"),
		CacheHitRatio: x.float("cache_hit_ratio"), InRecovery: x.flag("in_recovery"), StatsReset: x.str("stats_reset"),
	}
	rd.r.Connections.Max = x.num("max_connections")
}

func (rd *reader) extension(x row) {
	rd.r.Extensions = append(rd.r.Extensions, Extension{Name: x.str("name"), Version: x.str("version")})
}

func (rd *reader) connectionState(x row) {
	n := x.num("n")
	rd.r.Connections.ByState[x.str("state")] = n
	rd.r.Connections.Total += n
}

func (rd *reader) table(x row) {
	live, dead := x.num("live_tuples"), x.num("dead_tuples")
	var ratio float64
	if live+dead > 0 {
		ratio = float64(dead) / float64(live+dead)
	}
	rd.r.Tables = append(rd.r.Tables, TableStats{
		Schema: x.str("schema"), Name: x.str("name"), SizeBytes: x.num("size_bytes"), RowEstimate: x.num("row_estimate"),
		LiveTuples: live, DeadTuples: dead, DeadRatio: ratio, SeqScans: x.num("seq_scans"), IndexScans: x.num("index_scans"),
		LastVacuum: x.str("last_vacuum"), LastAutovacuum: x.str("last_autovacuum"),
		LastAnalyze: x.str("last_analyze"), LastAutoanalyze: x.str("last_autoanalyze"), TransactionIDAge: x.num("xid_age"),
	})
}

func indexRef(x row) IndexRef {
	return IndexRef{Schema: x.str("schema"), Table: x.str("table"), Name: x.str("name"), SizeBytes: x.num("size_bytes"), Scans: x.num("scans")}
}

func (rd *reader) unusedIndex(x row) { rd.r.Indexes.Unused = append(rd.r.Indexes.Unused, indexRef(x)) }

func (rd *reader) invalidIndex(x row) {
	rd.r.Indexes.Invalid = append(rd.r.Indexes.Invalid, indexRef(x))
}

func (rd *reader) duplicateIndex(x row) {
	rd.r.Indexes.Duplicate = append(rd.r.Indexes.Duplicate, DuplicateIndexes{
		Schema: x.str("schema"), Table: x.str("table"), Names: x.strs("names"), Definition: x.str("definition"),
	})
}

func (rd *reader) session(x row) {
	rd.r.Sessions = append(rd.r.Sessions, Session{
		PID: x.num("pid"), User: x.str("usename"), Application: x.str("application"), State: x.str("state"),
		Seconds: x.float("seconds"), StateSeconds: x.float("state_seconds"), Wait: x.str("wait"), Query: x.str("query"),
	})
}

func (rd *reader) blocked(x row) {
	rd.r.Blocking = append(rd.r.Blocking, Blocked{
		PID: x.num("pid"), User: x.str("usename"), BlockedBy: x.nums("blocked_by"), Seconds: x.float("seconds"),
	})
}

func (rd *reader) replica(x row) {
	rd.r.Replication = append(rd.r.Replication, Replica{
		Name: x.str("name"), State: x.str("state"), LagBytes: x.num("lag_bytes"), LagSeconds: x.float("lag_seconds"),
	})
}

// statements reads pg_stat_statements when the database holds it. Installed
// and not loaded is reported as such, not as a failure.
func (rd *reader) statements() {
	installed := false
	for _, e := range rd.r.Extensions {
		installed = installed || e.Name == statStatements
	}
	rd.r.Statements.Installed = installed
	if !installed {
		rd.r.Statements.Note = statStatements + " is not installed in this database"
		return
	}
	rows, err := query(rd.ctx, rd.q, rd.connection, statementsSQL)
	if err != nil {
		if strings.Contains(err.Error(), "55000") {
			rd.r.Statements.Note = statStatements + " is installed and not loaded; add it to shared_preload_libraries"
			return
		}
		rd.r.Errors = append(rd.r.Errors, SectionError{Section: SectionStatements, Error: err.Error()})
		return
	}
	for _, x := range rows {
		rd.r.Statements.Top = append(rd.r.Statements.Top, Statement{
			QueryID: x.str("query_id"), Calls: x.num("calls"), TotalMS: x.float("total_ms"),
			MeanMS: x.float("mean_ms"), Rows: x.num("rows"), Query: x.str("query"),
		})
	}
}
