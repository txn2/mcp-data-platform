//go:build integration

package acceptance

import (
	"encoding/json"
	"testing"
)

// Issue #2183: mcp-trino v1.8.0 moves the driver to trino-go-client v1.4.1,
// which decodes a ROW into a trino.Row and the values inside an ARRAY, MAP or
// ROW into Go types. trino_export reads a Parquet export's rows as the driver
// decodes them, and the Parquet writer took a ROW only as a slice, so every
// Parquet export of a ROW column was refused.
//
// What these hold, against the running platform and its Trino: a Parquet
// trino_export of a ROW, a ROW inside an ARRAY and inside a MAP, and nested
// DATE, TIMESTAMP, VARBINARY and DOUBLE NaN values registers back to the rows
// the query returned; and trino_query returns a ROW as the array of its field
// values and a nested DATE and TIMESTAMP as the text Trino sent, as it did on
// the previous driver.
//
// Wire forms: trino_export's `sql`, `format`, `name` and `purpose`,
// trino_query's `connection`, `sql` and `purpose`, and manage_table's `action`,
// `reference`, `connection` and `table_name` are typed string, so each is sent
// in its one form. trino_export's `resource` admits only the object form.

const issue2183Purpose = "Acceptance for #2183: nested values on trino-go-client v1.4."

// issue2183Nested is one row of each nested shape the driver decodes into its
// own types, and a row of nulls.
const issue2183Nested = `SELECT * FROM (VALUES
 (CAST(ROW(1, 'two') AS ROW(a BIGINT, b VARCHAR)),
  ARRAY[CAST(ROW(2, DATE '2026-10-16', TIMESTAMP '2026-10-16 12:34:56.123456') AS ROW(a BIGINT, d DATE, ts TIMESTAMP(6)))],
  MAP(ARRAY['k'], ARRAY[CAST(ROW(nan()) AS ROW(x DOUBLE))]),
  ARRAY[X'0001FF', NULL]),
 (NULL, NULL, NULL, NULL)
) AS v(r, ra, m, vb)`

// TestIssue2183_AParquetExportOfNestedRowsRegistersBackToItsRows: the export
// is written, registers with the nested types, and reads back as the query's
// rows. NaN equals nothing, so the comparison leaves the map out and the NaN
// is checked by is_nan.
func TestIssue2183_AParquetExportOfNestedRowsRegistersBackToItsRows(t *testing.T) {
	c := connect(t)
	stamp := issue1833Stamp()
	out := c.call("trino_export", map[string]any{
		"sql": issue2183Nested, "format": "parquet", "name": "Acceptance 2183 nested " + stamp,
		"purpose":  issue2183Purpose,
		"resource": map[string]any{"path": "acceptance/issue-2183", "filename": "acc-2183-nested-" + stamp + ".parquet"},
	})
	landed := issue1663Landing(t, out)
	reference, _ := landed["reference"].(string)
	reg := issue1833Register(t, c, reference, scratchResourceConnection, "acc_2183_nested_"+stamp)
	issue1833WantTypes(t, reg, []string{
		`r ROW("a" BIGINT, "b" VARCHAR)`,
		`ra ARRAY(ROW("a" BIGINT, "d" DATE, "ts" TIMESTAMP(6)))`,
		`m MAP(VARCHAR, ROW("x" DOUBLE))`,
		"vb ARRAY(VARBINARY)",
	})
	query, _ := reg["query_table"].(string)
	issue1833Same(t, c, scratchResourceConnection,
		"SELECT r, ra, vb FROM "+query, "SELECT r, ra, vb FROM ("+issue2183Nested+") q")
	if n := issue1833Count(t, c, scratchResourceConnection,
		"SELECT count(*) AS n FROM "+query+" WHERE is_nan(m['k'].x)"); n != 1 {
		t.Errorf("%d rows carry the NaN inside the map's row; want 1", n)
	}
}

// TestIssue2183_TrinoQueryReturnsNestedValuesAsTrinoSentThem: a ROW is the
// array of its field values, and a DATE and TIMESTAMP inside a container are
// the text Trino sent, not the driver's time in the server's zone.
func TestIssue2183_TrinoQueryReturnsNestedValuesAsTrinoSentThem(t *testing.T) {
	c := connect(t)
	got := c.call("trino_query", map[string]any{
		"connection": scratchResourceConnection, "purpose": issue2183Purpose,
		"sql": "SELECT r, ra FROM (" + issue2183Nested + ") q WHERE r IS NOT NULL",
	})
	rows, _ := got["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("the query returned %d rows: %v", len(rows), got)
	}
	row, _ := rows[0].(map[string]any)
	for name, want := range map[string]string{
		"r":  `[1,"two"]`,
		"ra": `[[2,"2026-10-16","2026-10-16 12:34:56.123456"]]`,
	} {
		b, err := json.Marshal(row[name])
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != want {
			t.Errorf("%s is %s; want %s", name, b, want)
		}
	}
}
