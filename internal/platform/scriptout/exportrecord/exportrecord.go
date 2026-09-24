// Package exportrecord is what one platform.export call did, as a managed
// script's run records it and as the script is handed it back: the output's
// name, format, size and where it landed, and, for an output appended to
// across calls (#1861), how much it holds so far.
package exportrecord

import (
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/exporttable"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptout"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
	"github.com/txn2/mcp-data-platform/internal/tablexlsx"
)

// exportRecordFields is the allocation hint for the dict RecordValue builds.
const exportRecordFields = 20

// Record is what one platform.export call did, in call order on the run's
// Result. A record with Preview set measured the output and wrote nothing,
// which is what a draft run does; otherwise it names the asset version written
// or the object delivered.
type Record struct {
	Name string `json:"name"`
	// Destination is the name the script wrote, so a run that sends one result
	// to two places reads as two records rather than as a repeat.
	Destination string `json:"destination"`
	Format      string `json:"format"`
	RowCount    int    `json:"row_count"`
	// Document marks an output written verbatim from a string body, whose
	// RowCount is therefore not a fact about it: without the marker a surface
	// rendering "N rows as html" would describe a dashboard as an empty table.
	Document bool `json:"document,omitempty"`
	// Refresh marks a platform.publish_data call: the run replaced the data
	// region of an existing asset rather than writing a whole output, and
	// Bytes is the payload spliced in, not the document around it.
	Refresh bool `json:"refresh,omitempty"`
	// Bytes is the serialized length of the output in its declared format. A
	// preview serializes to measure rather than estimating, so the number is
	// the same one a real run would report for the same rows.
	Bytes int `json:"bytes"`
	// Preview is true when nothing was persisted.
	Preview      bool   `json:"preview"`
	AssetID      string `json:"asset_id,omitempty"`
	AssetVersion int    `json:"asset_version,omitempty"`
	Bucket       string `json:"bucket,omitempty"`
	Key          string `json:"key,omitempty"`
	// ResourceID, ResourceRef, ResourceURI and ResourceVersion name the managed
	// resource a library output landed in (#1663). A script reads the reference
	// and the uri off the record to cite the file it just wrote, and they are
	// the same ones the next run reports.
	ResourceID      string `json:"resource_id,omitempty"`
	ResourceRef     string `json:"reference,omitempty"`
	ResourceURI     string `json:"uri,omitempty"`
	ResourceVersion int    `json:"version,omitempty"`
	// TableChanges is what the version did to the tables registered over the
	// output's file (#1536): one sentence per table, saying it followed onto
	// the version or is pinned and now behind it. The same sentences are
	// printed into the run log, so the run's history says the table moved, or
	// did not, without the script having to print anything.
	//
	// It is a change report, not the `tables` a fetched reference carries,
	// and is named apart from them for that reason (#1666).
	TableChanges []string `json:"table_changes,omitempty"`
	// Table is the registration the export's register= argument made over
	// the written file (#1820), or the one a draft would have made.
	Table *exporttable.Table `json:"table,omitempty"`
	// References is what references= declared, or a draft would have, and [] a
	// clearing (omitzero keeps it apart from absent). UndeclaredReferences is
	// each reference a portal document names that it did not list (#1834).
	References           []string `json:"references,omitzero"`
	UndeclaredReferences []string `json:"undeclared_references,omitempty"`
	// Sheets is an xlsx output's sheets and the data rows in each (#1849).
	Sheets []tablexlsx.SheetShape `json:"sheets,omitempty"`
}

// Value renders one export record as the dict the script receives. Where
// the output went decides which part of the record is present: an asset version
// for the portal, an object for a bucket, the file's reference and version for
// the library.
func Value(record Record) starlark.Value {
	out := starlark.NewDict(exportRecordFields)
	_ = out.SetKey(starlark.String("preview"), starlark.Bool(record.Preview))
	_ = out.SetKey(starlark.String("name"), starlark.String(record.Name))
	_ = out.SetKey(starlark.String("destination"), starlark.String(record.Destination))
	_ = out.SetKey(starlark.String("format"), starlark.String(record.Format))
	_ = out.SetKey(starlark.String("row_count"), starlark.MakeInt(record.RowCount))
	_ = out.SetKey(starlark.String("document"), starlark.Bool(record.Document))
	_ = out.SetKey(starlark.String("refresh"), starlark.Bool(record.Refresh))
	_ = out.SetKey(starlark.String("bytes"), starlark.MakeInt(record.Bytes))
	setLocators(out, record)
	if record.Table != nil {
		if table, err := starlarkconv.ToStarlark(record.Table.Map()); err == nil {
			_ = out.SetKey(starlark.String("table"), table)
		}
	}
	if len(record.TableChanges) > 0 {
		_ = out.SetKey(starlark.String("table_changes"), stringList(record.TableChanges))
	}
	if record.References != nil { // [] cleared them, which absence did not
		_ = out.SetKey(starlark.String("references"), stringList(record.References))
	}
	if len(record.UndeclaredReferences) > 0 {
		_ = out.SetKey(starlark.String("undeclared_references"), stringList(record.UndeclaredReferences))
	}
	if record.Sheets != nil {
		_ = out.SetKey(starlark.String("sheets"), scriptout.SheetsValue(record.Sheets))
	}
	return out
}

// stringList converts a list of strings to a Starlark list.
func stringList(values []string) *starlark.List {
	list := make([]starlark.Value, 0, len(values))
	for _, v := range values {
		list = append(list, starlark.String(v))
	}
	return starlark.NewList(list)
}

// setLocators adds where a record's output landed: the asset version for the
// portal, the object for a bucket, the file's reference and version for the
// library.
func setLocators(out *starlark.Dict, record Record) {
	if record.AssetID != "" {
		_ = out.SetKey(starlark.String("asset_id"), starlark.String(record.AssetID))
		_ = out.SetKey(starlark.String("asset_version"), starlark.MakeInt(record.AssetVersion))
	}
	if record.Bucket != "" {
		_ = out.SetKey(starlark.String("bucket"), starlark.String(record.Bucket))
	}
	if record.Key != "" {
		_ = out.SetKey(starlark.String("key"), starlark.String(record.Key))
	}
	if record.ResourceID != "" {
		_ = out.SetKey(starlark.String("resource_id"), starlark.String(record.ResourceID))
		_ = out.SetKey(starlark.String("reference"), starlark.String(record.ResourceRef))
		_ = out.SetKey(starlark.String("uri"), starlark.String(record.ResourceURI))
		_ = out.SetKey(starlark.String("version"), starlark.MakeInt(record.ResourceVersion))
	}
}

// Appending is one append=True call's answer (#1861): the output it added to
// and how much that output holds so far. The output is written when the run
// finishes, so there is no asset or file to name yet.
type Appending struct {
	Name, Destination, Format string
	Rows, Bytes               int
	Preview                   bool
}

// AppendingValue renders an Appending as the dict the script receives.
func AppendingValue(a Appending) starlark.Value {
	out := starlark.NewDict(appendingFields)
	_ = out.SetKey(starlark.String("name"), starlark.String(a.Name))
	_ = out.SetKey(starlark.String("destination"), starlark.String(a.Destination))
	_ = out.SetKey(starlark.String("format"), starlark.String(a.Format))
	_ = out.SetKey(starlark.String("row_count"), starlark.MakeInt(a.Rows))
	_ = out.SetKey(starlark.String("bytes"), starlark.MakeInt(a.Bytes))
	_ = out.SetKey(starlark.String("appending"), starlark.True)
	_ = out.SetKey(starlark.String("preview"), starlark.Bool(a.Preview))
	return out
}

// appendingFields is the field count of the dict AppendingValue builds.
const appendingFields = 7
