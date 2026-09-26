// Package whtable owns what a webhook source is in the query engine (#1870):
// two tables and the view readers name.
//
// webhook_{source}_raw is a JSON table over the raw segments, partitioned by
// dt, hour and minute in the Hive layout the receiver writes: one partition
// per compaction window, minute being the minute the window starts at.
// webhook_{source}_compacted is a Parquet table whose partitions are each
// registered at the directory of the managed resource holding that window.
// webhook_{source} is the view over both: a window whose compacted partition
// is registered is read from there, and every other window from the raw
// segments. Registering a window's partition is the one metastore write that
// moves it from one side to the other, so no window is missing from the view
// or served twice.
//
// Every statement runs through the Trino connection's Exec, the platform's one
// write path into Trino. Registering a partition at an explicit location needs
// the catalog to set hive.allow-register-partition-procedure=true; see
// docs/server/scratch-catalog.md.
package whtable

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/scratchcatalog"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// Executor runs a statement on a Trino connection and reports its scratch
// target. The Trino toolkit satisfies it.
type Executor interface {
	Exec(ctx context.Context, connection, sql string) error
	ScratchTarget(connection string) (trino.ScratchConfig, bool)
	AcceptsWrites(connection string) bool
}

// Refusals a source's connection can meet.
var (
	ErrNoScratchTarget = errors.New("this connection has no scratch catalog and schema configured, so a webhook source's table cannot be created on it")
	ErrReadOnly        = errors.New("this connection is read-only, so a webhook source's table cannot be created on it")
	// ErrRegisterDisabled is the catalog refusing register_partition. Each
	// compacted window is registered at its own resource's directory, which is
	// what the procedure is for, so a source cannot run without it.
	ErrRegisterDisabled = scratchcatalog.ErrRegisterDisabled
	// ErrProcedureDenied is the engine's access control refusing a partition
	// procedure to the connection's Trino user: a catalog rule does not grant
	// procedures, so the source cannot run until a procedures rule does.
	ErrProcedureDenied = scratchcatalog.ErrProcedureDenied
)

// Target is where a source's tables live.
type Target struct {
	Connection string
	Catalog    string
	Schema     string
}

// Tables creates and maintains sources' tables.
type Tables struct {
	exec   Executor
	bucket string
}

// New builds the table manager. bucket is the managed-resources bucket both
// the raw segments and the compacted windows are written to.
func New(exec Executor, bucket string) *Tables {
	return &Tables{exec: exec, bucket: bucket}
}

// TargetFor resolves the connection a source names to its scratch catalog and
// schema, refusing one that has none or will not run write statements.
func (t *Tables) TargetFor(connection string) (Target, error) {
	sc, ok := t.exec.ScratchTarget(connection)
	if !ok || sc.Catalog == "" || sc.Schema == "" {
		return Target{}, ErrNoScratchTarget
	}
	if !t.exec.AcceptsWrites(connection) {
		return Target{}, ErrReadOnly
	}
	return Target{Connection: connection, Catalog: sc.Catalog, Schema: sc.Schema}, nil
}

// S3Location renders a key prefix in the bucket as the URI a table or
// partition location names.
func (t *Tables) S3Location(prefix string) string {
	return "s3://" + t.bucket + "/" + strings.TrimPrefix(prefix, "/")
}

// Create makes the source's schema, both tables and the view. Every statement
// is idempotent, so creating a source whose tables survived an earlier
// attempt finishes the job.
func (t *Tables) Create(ctx context.Context, tg Target, src whsource.Source) error {
	stmts := []string{
		"CREATE SCHEMA IF NOT EXISTS " + quoteIdent(tg.Catalog) + nameSep + quoteIdent(tg.Schema),
		t.createTable(tg, src.RawTableName(), whlayout.RawPrefix(src.Name), "JSON"),
		t.createTable(tg, src.CompactedTableName(), whlayout.CompactedPrefix(src.Name), "PARQUET"),
		ViewStatement(tg, src),
	}
	for _, s := range stmts {
		if err := t.exec.Exec(ctx, tg.Connection, s); err != nil {
			return fmt.Errorf("creating the table of webhook source %s: %w", src.Name, err)
		}
	}
	return nil
}

// Drop removes the view and both tables. The objects under them are the
// compactor's to delete; a dropped external table leaves its files.
func (t *Tables) Drop(ctx context.Context, tg Target, src whsource.Source) error {
	var errs []error
	for _, s := range []string{
		"DROP VIEW IF EXISTS " + qualified(tg, src.TableName()),
		"DROP TABLE IF EXISTS " + qualified(tg, src.RawTableName()),
		"DROP TABLE IF EXISTS " + qualified(tg, src.CompactedTableName()),
	} {
		if err := t.exec.Exec(ctx, tg.Connection, s); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("dropping the tables of webhook source %s: %w", src.Name, err)
	}
	return nil
}

// PartitionColumns are the partition columns of both tables and the view, in
// order: the UTC date, hour and minute a window starts at.
var PartitionColumns = []string{"dt", "hour", "minute"}

// partitionArray is PartitionColumns as the ARRAY literal the partition
// procedures and the table properties take.
const partitionArray = "ARRAY['dt', 'hour', 'minute']"

// columnDefs are the columns both tables declare, then the partition columns.
func columnDefs() string {
	cols := make([]string, 0, len(whevent.Columns))
	for _, c := range whevent.Columns {
		typ := "varchar"
		if c == "received_at" || c == "landed_at" {
			typ = "timestamp(6)"
		}
		cols = append(cols, quoteIdent(c)+" "+typ)
	}
	for _, c := range PartitionColumns {
		cols = append(cols, quoteIdent(c)+" varchar")
	}
	return strings.Join(cols, listSep)
}

// createTable renders one partitioned external table.
func (t *Tables) createTable(tg Target, table, prefix, format string) string {
	return "CREATE TABLE IF NOT EXISTS " + qualified(tg, table) + " (" + columnDefs() +
		") WITH (external_location = " + quoteLiteral(t.S3Location(prefix)) +
		", format = '" + format + "', partitioned_by = " + partitionArray + ")"
}

// ViewStatement renders the view readers name. Each window is read from
// exactly one side: the compacted table when the window's partition is
// registered there, and the raw segments otherwise.
func ViewStatement(tg Target, src whsource.Source) string {
	cols := make([]string, 0, len(whevent.Columns))
	for _, c := range append(append([]string{}, whevent.Columns...), PartitionColumns...) {
		cols = append(cols, quoteIdent(c))
	}
	list := strings.Join(cols, listSep)
	partitions := quoteIdent(tg.Catalog) + nameSep + quoteIdent(tg.Schema) + nameSep + quoteIdent(src.CompactedTableName()+"$partitions")
	return "CREATE OR REPLACE VIEW " + qualified(tg, src.TableName()) + " SECURITY INVOKER AS " +
		"SELECT " + list + " FROM " + qualified(tg, src.CompactedTableName()) +
		" UNION ALL SELECT " + list + " FROM " + qualified(tg, src.RawTableName()) + " r" +
		" WHERE NOT EXISTS (SELECT 1 FROM " + partitions + " p WHERE p.\"dt\" = r.\"dt\" AND p.\"hour\" = r.\"hour\" AND p.\"minute\" = r.\"minute\")"
}

// SyncRaw makes the raw table's partitions match the window directories that
// exist: a window a segment was just written into becomes readable, and a
// window whose segments retention deleted stops being listed.
func (t *Tables) SyncRaw(ctx context.Context, tg Target, src whsource.Source) error {
	stmt := "CALL " + quoteIdent(tg.Catalog) + ".system.sync_partition_metadata(" +
		quoteLiteral(tg.Schema) + listSep + quoteLiteral(src.RawTableName()) + ", 'FULL')"
	if err := t.exec.Exec(ctx, tg.Connection, stmt); err != nil {
		return classify(tg, fmt.Errorf("syncing the raw partitions of webhook source %s: %w", src.Name, err))
	}
	return nil
}

// RegisterWindow points a window's compacted partition at location,
// replacing where it pointed before. It is what moves the window from the raw
// side of the view to the compacted side.
func (t *Tables) RegisterWindow(ctx context.Context, tg Target, src whsource.Source, start time.Time, location string) error {
	if err := t.UnregisterWindow(ctx, tg, src, start); err != nil {
		return err
	}
	stmt := partitionCall(tg, "register_partition", src.CompactedTableName(), start, location)
	if err := t.exec.Exec(ctx, tg.Connection, stmt); err != nil {
		return classify(tg, fmt.Errorf("registering window %s of webhook source %s: %w", windowName(start), src.Name, err))
	}
	return nil
}

// RegisterRawWindow makes a new window's raw segments readable at once,
// rather than at the next sync. The receiver calls it when it writes the
// first segment of a window, which is what lets an event be queried as soon
// as it is acknowledged. A window already registered is left as it is.
func (t *Tables) RegisterRawWindow(ctx context.Context, tg Target, src whsource.Source, start time.Time) error {
	stmt := partitionCall(tg, "register_partition", src.RawTableName(), start, t.S3Location(whlayout.WindowPrefix(src.Name, start)))
	err := t.exec.Exec(ctx, tg.Connection, stmt)
	if err == nil || strings.Contains(err.Error(), "] is already registered") {
		return nil
	}
	return classify(tg, fmt.Errorf("registering raw window %s of webhook source %s: %w", windowName(start), src.Name, err))
}

// UnregisterWindow removes a window's compacted partition, which puts the
// window back on the raw side of the view. A window with no partition is
// already unregistered.
func (t *Tables) UnregisterWindow(ctx context.Context, tg Target, src whsource.Source, start time.Time) error {
	stmt := partitionCall(tg, "unregister_partition", src.CompactedTableName(), start, "")
	err := t.exec.Exec(ctx, tg.Connection, stmt)
	if err == nil || partitionMissing(err) {
		return nil
	}
	return classify(tg, fmt.Errorf("unregistering window %s of webhook source %s: %w", windowName(start), src.Name, err))
}

// windowName is a window's partition values as an error names them.
func windowName(start time.Time) string {
	dt, hh, mm := whlayout.PartitionValues(start)
	return dt + " " + hh + ":" + mm
}

// Probe proves a connection can hold a source: the catalog reads the managed
// resources bucket, and allows register_partition. It runs against the
// source's own tables, which Create has made, by registering and removing a
// window that holds nothing.
func (t *Tables) Probe(ctx context.Context, tg Target, src whsource.Source) error {
	count := "SELECT count(*) FROM " + qualified(tg, src.TableName())
	if err := t.exec.Exec(ctx, tg.Connection, count); err != nil {
		return fmt.Errorf("the scratch catalog of connection %s could not read the managed-resources bucket %s: %w",
			tg.Connection, t.bucket, err)
	}
	probe := time.Unix(0, 0).UTC()
	if err := t.RegisterWindow(ctx, tg, src, probe, t.S3Location(whlayout.CompactedPrefix(src.Name)+"probe/")); err != nil {
		return err
	}
	return t.UnregisterWindow(ctx, tg, src, probe)
}

// partitionMissing reports the catalog's answer for a partition that is not
// registered: "Partition 'dt=2026-09-24/hour=07/minute=00' does not exist".
func partitionMissing(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "Partition '") && strings.Contains(msg, "' does not exist")
}

// classify turns the catalog's refusal of the partition procedures, by its
// properties or by access control, into the sentence an operator acts on.
func classify(tg Target, err error) error {
	return scratchcatalog.Classify(tg.Connection, tg.Catalog, err) //nolint:wrapcheck // every caller wraps err with its window first; Classify adds the remedy
}

// partitionCall renders a call of the catalog's register_partition or
// unregister_partition for one window of a table; location is empty for
// unregister_partition, which takes none.
func partitionCall(tg Target, procedure, table string, start time.Time, location string) string {
	dt, hh, mm := whlayout.PartitionValues(start)
	args := []string{
		quoteLiteral(tg.Schema), quoteLiteral(table), partitionArray,
		"ARRAY[" + quoteLiteral(dt) + listSep + quoteLiteral(hh) + listSep + quoteLiteral(mm) + "]",
	}
	if location != "" {
		args = append(args, quoteLiteral(location))
	}
	return "CALL " + quoteIdent(tg.Catalog) + ".system." + procedure + "(" + strings.Join(args, listSep) + ")"
}

// listSep and nameSep join a statement's argument list and a qualified name.
const (
	listSep = ", "
	nameSep = "."
)

func qualified(tg Target, table string) string {
	return strings.Join([]string{quoteIdent(tg.Catalog), quoteIdent(tg.Schema), quoteIdent(table)}, nameSep)
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func quoteLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
