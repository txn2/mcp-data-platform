//go:build integration

package acceptance

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	s3client "github.com/txn2/mcp-s3/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/tableparquet"
	"github.com/txn2/mcp-data-platform/internal/tabletype"
)

// Issue #1833: registered tables were text-only and Parquet was supported
// nowhere. A JSON-lines table declared every column VARCHAR though the file
// carried its types, a nested value was refused outright, and a Parquet file
// could not be registered, previewed or written by an export.
//
// What these hold, against the running platform: a Parquet file written by
// pyarrow registers with the types its footer declares and reads back every
// value exactly, microseconds included, over every physical layout a writer
// chooses; a type nothing reads back exactly, and two columns one apart by
// case, are refused by name; bytes named Parquet that are not are refused as
// not Parquet; a Parquet file larger than the registration read cap registers;
// a followed Parquet file's new version moves the table and says which column
// it added; a JSON-lines file registers typed, nested values as ROW and ARRAY;
// a JSON-lines registration made before typing keeps its VARCHAR columns across
// a follow until registered again; sample_sql and the listing follow the
// format; and trino_export and platform.export write Parquet that registers
// back to the rows it was written from.
//
// Wire forms: manage_resource's `action`, `filename`, `display_name`, `path`,
// `description`, `content_type`, `content` and `content_base64`, manage_table's
// `action`, `reference`, `connection`, `table_name` and `registration_id`,
// trino_query's and trino_execute's `connection`, `sql` and `purpose`,
// trino_export's `sql`, `format`, `name` and `purpose`, manage_script's
// `command`, `name`, `description`, `source` and `state_action`, and
// run_script's `name` are typed string, and run_script's `wait_seconds` an
// integer, so each is sent in its one form. A JSON-lines file is text and is
// sent both in `content` and in `content_base64`; a Parquet file is binary and
// reaches a tool only in `content_base64`. trino_export's `resource` admits
// only the object form. manage_table's `follow` is left to its default.

const issue1833Purpose = "Acceptance for #1833: Parquet tables and typed JSON lines."

// issue1833Fixture reads one of the committed pyarrow fixtures
// (testdata/issue_1833_fixtures.py).
func issue1833Fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "issue_1833_"+name+".parquet"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// issue1833Stamp names one test's objects apart from every other run's.
func issue1833Stamp() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

// issue1833Upload files content as a managed resource, in the form named
// ("content" or "content_base64"), and deletes it afterwards.
func issue1833Upload(t *testing.T, c *client, filename, contentType string, body []byte, form string) string {
	t.Helper()
	args := map[string]any{
		"action":       "create",
		"filename":     filename,
		"display_name": "Acceptance 1833 " + filename,
		"path":         "acceptance/issue-1833",
		"description":  "Acceptance #1833 fixture.",
		"content_type": contentType,
	}
	if form == "content" {
		args["content"] = string(body)
	} else {
		args["content_base64"] = base64.StdEncoding.EncodeToString(body)
	}
	out := c.call("manage_resource", args)
	id, _ := out["resource_id"].(string)
	reference, _ := out["reference"].(string)
	if id == "" || reference == "" {
		t.Fatalf("manage_resource create returned no resource: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	return reference
}

// issue1833Register registers a stored file and unregisters it afterwards.
func issue1833Register(t *testing.T, c *client, reference, connection, table string) map[string]any {
	t.Helper()
	reg := c.call("manage_table", map[string]any{
		"action": "register", "reference": reference, "connection": connection, "table_name": table,
	})
	query, _ := reg["query_table"].(string)
	if query == "" {
		t.Fatalf("manage_table did not register the file: %v", reg)
	}
	t.Cleanup(func() {
		if id, _ := reg["registration_id"].(string); id != "" {
			_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
		}
	})
	return reg
}

// issue1833Types renders a registration's column_types as "name TYPE" lines.
func issue1833Types(reg map[string]any) []string {
	raw, _ := reg["column_types"].([]any)
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		m, _ := entry.(map[string]any)
		out = append(out, fmt.Sprintf("%v %v", m["name"], m["type"]))
	}
	return out
}

func issue1833WantTypes(t *testing.T, reg map[string]any, want []string) {
	t.Helper()
	got := issue1833Types(reg)
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Errorf("the declared columns are\n  %s\nwant\n  %s", strings.Join(got, "; "), strings.Join(want, "; "))
	}
}

// issue1833Count runs a query returning one count and returns it.
func issue1833Count(t *testing.T, c *client, connection, sqlText string) int {
	t.Helper()
	got := c.call("trino_query", map[string]any{"connection": connection, "purpose": issue1833Purpose, "sql": sqlText})
	rows, _ := got["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("the count query returned %d rows: %v", len(rows), got)
	}
	row, _ := rows[0].(map[string]any)
	for _, v := range row {
		if n, ok := v.(float64); ok {
			return int(n)
		}
	}
	t.Fatalf("the count query returned no number: %v", row)
	return 0
}

// issue1833Same asserts two relations hold the same rows: EXCEPT in both
// directions is empty.
func issue1833Same(t *testing.T, c *client, connection, left, right string) {
	t.Helper()
	for _, dir := range [][2]string{{left, right}, {right, left}} {
		n := issue1833Count(t, c, connection,
			"SELECT count(*) AS n FROM (("+dir[0]+") EXCEPT ("+dir[1]+"))")
		if n != 0 {
			t.Errorf("%d rows of\n  %s\nare not in\n  %s", n, dir[0], dir[1])
		}
	}
}

// issue1833AllTypesExpected is the pyarrow fixture's rows, typed as the
// registration declares them.
const issue1833AllTypesExpected = `SELECT CAST(b AS BOOLEAN) b, CAST(i8 AS TINYINT) i8, CAST(i16 AS SMALLINT) i16,
 CAST(i32 AS INTEGER) i32, CAST(i64 AS BIGINT) i64, CAST(u8 AS SMALLINT) u8, CAST(u16 AS INTEGER) u16,
 CAST(f32 AS REAL) f32, CAST(f64 AS DOUBLE) f64, CAST(amount AS DECIMAL(12,2)) amount,
 CAST(wide AS DECIMAL(38,12)) wide, CAST(s AS VARCHAR) s, CAST(bin AS VARBINARY) bin, CAST(d AS DATE) d,
 CAST(ts_ms AS TIMESTAMP(6)) ts_ms, CAST(ts_us AS TIMESTAMP(6)) ts_us, CAST(tags AS ARRAY(VARCHAR)) tags,
 CAST(attrs AS MAP(VARCHAR, DOUBLE)) attrs, CAST(st AS ROW(a BIGINT, n VARCHAR)) st
FROM (VALUES
 (true, -8, -16, -32, 1, 0, 0, REAL '1.5', DOUBLE '3.141592653589793', DECIMAL '12345.67',
  DECIMAL '12345678901234567890123456.123456789012', 'alpha', X'0001FF', DATE '2024-05-01',
  TIMESTAMP '2024-05-01 12:34:56.789', TIMESTAMP '2024-05-01 12:34:56.789123', ARRAY['a','b'],
  MAP(ARRAY['k'], ARRAY[DOUBLE '1.5']), ROW(1, 'x')),
 (false, 8, 16, 32, 2, 8, 16, REAL '-2.25', DOUBLE '-0.5', DECIMAL '-0.01', DECIMAL '0', 'line' || chr(10) || 'break',
  X'', DATE '1970-01-01', NULL, TIMESTAMP '1999-12-31 23:59:59.999999', ARRAY[], MAP(), ROW(NULL, 'y')),
 (true, 127, 32767, 2147483647, 9007199254740993, 255, 65535, REAL '3.0', DOUBLE '1e300', DECIMAL '9999999999.99',
  DECIMAL '-1', '', X'616263', DATE '2099-12-31', TIMESTAMP '1970-01-01 00:00:00',
  TIMESTAMP '2024-01-01 00:00:00.000001', ARRAY[NULL, 'c'], MAP(ARRAY['x','y'], ARRAY[NULL, DOUBLE '2.0']),
  ROW(3, NULL)),
 (NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL)
) AS v(b, i8, i16, i32, i64, u8, u16, f32, f64, amount, wide, s, bin, d, ts_ms, ts_us, tags, attrs, st)`

// TestIssue1833_AParquetFileRegistersWithItsTypesAndEveryValueExactly is
// criterion 1, over the mapping's every row: the pyarrow fixture registers as
// Parquet with the declared types, and SELECT * returns every value exactly,
// microseconds and nulls included. The other physical layouts a writer chooses
// (a DECIMAL on INT32 and INT64, an INT96 and a nanosecond timestamp, JSON
// text) read back exactly too.
func TestIssue1833_AParquetFileRegistersWithItsTypesAndEveryValueExactly(t *testing.T) {
	c := connect(t)
	stamp := issue1833Stamp()
	ref := issue1833Upload(t, c, "acc-1833-all-"+stamp+".parquet", tableparquet.ContentType,
		issue1833Fixture(t, "all_types"), "content_base64")
	reg := issue1833Register(t, c, ref, scratchResourceConnection, "acc_1833_all_"+stamp)
	if format, _ := reg["format"].(string); format != "parquet" {
		t.Errorf("manage_table reports format %q; want parquet", format)
	}
	issue1833WantTypes(t, reg, []string{
		"b BOOLEAN", "i8 TINYINT", "i16 SMALLINT", "i32 INTEGER", "i64 BIGINT", "u8 SMALLINT", "u16 INTEGER",
		"f32 REAL", "f64 DOUBLE", "amount DECIMAL(12,2)", "wide DECIMAL(38,12)", "s VARCHAR", "bin VARBINARY",
		"d DATE", "ts_ms TIMESTAMP(6)", "ts_us TIMESTAMP(6)", "tags ARRAY(VARCHAR)", "attrs MAP(VARCHAR, DOUBLE)",
		`st ROW("a" BIGINT, "n" VARCHAR)`,
	})
	query, _ := reg["query_table"].(string)
	issue1833Same(t, c, scratchResourceConnection, "SELECT * FROM "+query, issue1833AllTypesExpected)

	ref = issue1833Upload(t, c, "acc-1833-physical-"+stamp+".parquet", tableparquet.ContentType,
		issue1833Fixture(t, "physical"), "content_base64")
	reg = issue1833Register(t, c, ref, scratchResourceConnection, "acc_1833_physical_"+stamp)
	issue1833WantTypes(t, reg, []string{"dec9 DECIMAL(9,2)", "dec18 DECIMAL(18,2)", "ts_ns TIMESTAMP(6)", "doc VARCHAR"})
	query, _ = reg["query_table"].(string)
	issue1833Same(t, c, scratchResourceConnection, "SELECT * FROM "+query, `SELECT CAST(a AS DECIMAL(9,2)),
 CAST(b AS DECIMAL(18,2)), CAST(c AS TIMESTAMP(6)), CAST(d AS VARCHAR) FROM (VALUES
 (DECIMAL '1234567.89', DECIMAL '-1234567890123456.78', TIMESTAMP '2024-05-01 12:34:56.789123', '{"a": [1, 2]}'),
 (NULL, NULL, NULL, NULL)) AS v(a, b, c, d)`)

	ref = issue1833Upload(t, c, "acc-1833-int96-"+stamp+".parquet", tableparquet.ContentType,
		issue1833Fixture(t, "int96"), "content_base64")
	reg = issue1833Register(t, c, ref, scratchResourceConnection, "acc_1833_int96_"+stamp)
	issue1833WantTypes(t, reg, []string{"ts96 TIMESTAMP(6)"})
	query, _ = reg["query_table"].(string)
	issue1833Same(t, c, scratchResourceConnection, "SELECT * FROM "+query,
		"SELECT CAST(t AS TIMESTAMP(6)) FROM (VALUES (TIMESTAMP '2024-05-01 12:34:56.789123'), (NULL)) AS v(t)")
}

// TestIssue1833_ABigintColumnJoinsWithoutACast is criterion 2: the registered
// table's BIGINT column joins to a BIGINT warehouse column as it is.
func TestIssue1833_ABigintColumnJoinsWithoutACast(t *testing.T) {
	c := connect(t)
	stamp := issue1833Stamp()
	ref := issue1833Upload(t, c, "acc-1833-join-"+stamp+".parquet", tableparquet.ContentType,
		issue1833Fixture(t, "all_types"), "content_base64")
	reg := issue1833Register(t, c, ref, scratchResourceConnection, "acc_1833_join_"+stamp)
	query, _ := reg["query_table"].(string)

	warehouse := "memory.default.acc_1833_warehouse_" + stamp
	c.call("trino_execute", map[string]any{
		"connection": scratchResourceConnection, "purpose": issue1833Purpose,
		"sql": "CREATE TABLE " + warehouse + " AS SELECT * FROM (VALUES BIGINT '1', BIGINT '9007199254740993', " +
			"BIGINT '42') AS w(id)",
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("trino_execute", map[string]any{
			"connection": scratchResourceConnection, "purpose": issue1833Purpose, "sql": "DROP TABLE " + warehouse,
		})
	})
	n := issue1833Count(t, c, scratchResourceConnection,
		"SELECT count(*) AS n FROM "+warehouse+" w JOIN "+query+" t ON w.id = t.i64")
	if n != 2 {
		t.Errorf("the join matched %d rows; want the 2 ids both tables hold", n)
	}
}

// TestIssue1833_ATypeNothingReadsBackIsRefusedByColumn is criterion 3: a
// Parquet file with a TIME, a UUID or an unsigned 32-bit column is refused,
// naming the column and its Parquet type, and no table is created.
func TestIssue1833_ATypeNothingReadsBackIsRefusedByColumn(t *testing.T) {
	c := connect(t)
	for fixture, want := range map[string][]string{
		"time":   {`"at"`, "INT64 TIME("},
		"uuid":   {`"u"`, "FIXED_LEN_BYTE_ARRAY(16) UUID"},
		"uint32": {`"big"`, "INT32 INT(32,false)"},
	} {
		t.Run(fixture, func(t *testing.T) {
			stamp := issue1833Stamp()
			ref := issue1833Upload(t, c, "acc-1833-"+fixture+"-"+stamp+".parquet", tableparquet.ContentType,
				issue1833Fixture(t, fixture), "content_base64")
			issue1833Refused(t, c, ref, "acc_1833_"+fixture+"_"+stamp, want...)
		})
	}
}

// issue1833Refused asserts a registration is refused saying each of want, and
// that nothing was registered over the file.
func issue1833Refused(t *testing.T, c *client, reference, table string, want ...string) {
	t.Helper()
	res, text, err := c.callRaw("manage_table", map[string]any{
		"action": "register", "reference": reference, "connection": scratchResourceConnection, "table_name": table,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("the file was registered: %s", text)
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("the refusal does not say %q: %s", w, text)
		}
	}
	list := c.call("manage_table", map[string]any{"action": "list", "reference": reference})
	if regs, _ := list["table_registrations"].([]any); len(regs) != 0 {
		t.Errorf("a refused file has registrations: %v", regs)
	}
}

// TestIssue1833_ColumnsOneApartByCaseAreRefused is criterion 4.
func TestIssue1833_ColumnsOneApartByCaseAreRefused(t *testing.T) {
	c := connect(t)
	stamp := issue1833Stamp()
	ref := issue1833Upload(t, c, "acc-1833-case-"+stamp+".parquet", tableparquet.ContentType,
		issue1833Fixture(t, "case"), "content_base64")
	issue1833Refused(t, c, ref, "acc_1833_case_"+stamp, `"Id"`, `"id"`, "one column")
}

// TestIssue1833_BytesNamedParquetThatAreNotAreRefused is criterion 5: a file
// named .parquet holding text is refused as not Parquet, whether it was
// declared Parquet or declared nothing specific, and in either form the bytes
// reach the tool in.
func TestIssue1833_BytesNamedParquetThatAreNotAreRefused(t *testing.T) {
	c := connect(t)
	for _, form := range []string{"content", "content_base64"} {
		for _, declared := range []string{tableparquet.ContentType, "application/octet-stream"} {
			t.Run(form+" "+declared, func(t *testing.T) {
				stamp := issue1833Stamp()
				ref := issue1833Upload(t, c, "acc-1833-fake-"+stamp+".parquet", declared,
					[]byte("id,name\n1,this is a CSV\n"), form)
				issue1833Refused(t, c, ref, "acc_1833_fake_"+stamp, "not a Parquet file")
			})
		}
	}
}

// TestIssue1833_AParquetFileLargerThanTheReadCapRegisters is criterion 6. The
// read cap is the deployment's upload ceiling, so no write path the platform
// offers stores a file past it; a file past it arrives as an overwrite of the
// object in place, which is how a vendor drop replaces its file. The asset an
// export wrote is overwritten in the object store with a Parquet file larger
// than the cap, and the registration reads its footer and nothing else.
func TestIssue1833_AParquetFileLargerThanTheReadCapRegisters(t *testing.T) {
	c := connect(t)
	stamp := issue1833Stamp()
	out := c.call("trino_export", map[string]any{
		"sql": "SELECT BIGINT '1' AS id", "format": "parquet", "name": "Acceptance 1833 large " + stamp,
		"purpose": issue1833Purpose,
	})
	assetID, _ := out["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("trino_export wrote no asset: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/portal/assets/"+assetID, http.NoBody) })
	status, asset := c.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the asset: %d %v", status, asset)
	}
	bucket, _ := asset["s3_bucket"].(string)
	key, _ := asset["s3_key"].(string)

	const rows, rowBytes = 270, 1 << 20 // 270 MiB, past the dev stack's 250 MB ceiling
	big := issue1833LargeParquet(t, rows, rowBytes)
	issue1833PutObject(t, bucket, key, big)

	reg := issue1833Register(t, c, "mcp:asset:"+assetID, scratchConnection, "acc_1833_large_"+stamp)
	issue1833WantTypes(t, reg, []string{"id BIGINT", "blob VARBINARY"})
	query, _ := reg["query_table"].(string)
	n := issue1833Count(t, c, scratchConnection, "SELECT count(*) AS n FROM "+query+" WHERE length(blob) = "+
		fmt.Sprint(rowBytes))
	if n != rows {
		t.Errorf("the table serves %d full rows; want %d", n, rows)
	}
}

// issue1833LargeParquet writes a Parquet file of rows rows, each carrying
// rowBytes of random bytes, which no compression makes smaller.
func issue1833LargeParquet(t *testing.T, rows, rowBytes int) []byte {
	t.Helper()
	data := make([][]any, rows)
	for i := range data {
		blob := make([]byte, rowBytes)
		if _, err := rand.Read(blob); err != nil {
			t.Fatal(err)
		}
		data[i] = []any{int64(i), blob}
	}
	b, err := tableparquet.Write([]tabletype.Column{
		{Name: "id", Type: tabletype.Scalar(tabletype.Bigint)},
		{Name: "blob", Type: tabletype.Scalar(tabletype.Varbinary)},
	}, data)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// issue1833PutObject overwrites an object in the dev stack's portal object
// store, which make acceptance locates through dev/.dev-ports.env.
func issue1833PutObject(t *testing.T, bucket, key string, body []byte) {
	t.Helper()
	port := os.Getenv("DEV_S3_PORT")
	if port == "" {
		port = "9000"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s3, err := s3client.New(ctx, &s3client.Config{
		Region: "us-east-1", Endpoint: "http://localhost:" + port, UsePathStyle: true,
		AccessKeyID: "dev-access-key", SecretAccessKey: "dev-secret-key", Timeout: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s3.PutObjectStream(ctx, &s3client.PutObjectStreamInput{
		Bucket: bucket, Key: key, Body: bytes.NewReader(body), ContentType: tableparquet.ContentType,
	}); err != nil {
		t.Fatalf("overwriting s3://%s/%s: %v", bucket, key, err)
	}
}

// TestIssue1833_ANewVersionWithAnAddedColumnMovesTheTable is criterion 7: a
// followed Parquet file's next version, with a column added, moves the table,
// the write's table_changes names the column, and the column is queryable.
func TestIssue1833_ANewVersionWithAnAddedColumnMovesTheTable(t *testing.T) {
	c := connect(t)
	stamp := issue1833Stamp()
	out := c.call("trino_export", map[string]any{
		"sql": "SELECT BIGINT '1' AS id, 'west' AS region", "format": "parquet",
		"name": "Acceptance 1833 follow " + stamp, "purpose": issue1833Purpose,
	})
	assetID, _ := out["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("trino_export wrote no asset: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/portal/assets/"+assetID, http.NoBody) })
	reg := issue1833Register(t, c, "mcp:asset:"+assetID, scratchConnection, "acc_1833_follow_"+stamp)
	query, _ := reg["query_table"].(string)

	next, err := tableparquet.Write([]tabletype.Column{
		{Name: "id", Type: tabletype.Scalar(tabletype.Bigint)},
		{Name: "region", Type: tabletype.Scalar(tabletype.Varchar)},
		{Name: "units", Type: tabletype.Scalar(tabletype.Integer)},
	}, [][]any{{int64(1), "west", int64(7)}, {int64(2), "east", int64(9)}})
	if err != nil {
		t.Fatal(err)
	}
	status, body := c.rest(http.MethodPut, "/api/v1/portal/assets/"+assetID+"/content", bytes.NewReader(next))
	if status != http.StatusOK {
		t.Fatalf("writing the new version: %d %v", status, body)
	}
	changes := fmt.Sprint(body["table_changes"])
	for _, want := range []string{"now reads version", "added units INTEGER"} {
		if !strings.Contains(changes, want) {
			t.Errorf("table_changes does not say %q: %s", want, changes)
		}
	}
	if n := issue1833Count(t, c, scratchConnection, "SELECT sum(units) AS n FROM "+query); n != 16 {
		t.Errorf("the added column sums to %d; want 16", n)
	}
}

// issue1833TypedJSONL is criterion 8's file: integer, fractional, boolean,
// string, mixed-kind and all-null keys.
const issue1833TypedJSONL = `{"i":1,"f":1.5,"b":true,"s":"x","m":1,"n":null}
{"i":2,"f":2,"b":false,"s":"y","m":"two","n":null}
{"i":39,"f":-0.25,"b":true,"s":"z","m":true}
`

// TestIssue1833_JSONLinesColumnsAreTypedFromTheirValues is criterion 8, with
// the file sent in both forms manage_resource takes it in.
func TestIssue1833_JSONLinesColumnsAreTypedFromTheirValues(t *testing.T) {
	c := connect(t)
	for _, form := range []string{"content", "content_base64"} {
		t.Run(form, func(t *testing.T) {
			stamp := issue1833Stamp()
			ref := issue1833Upload(t, c, "acc-1833-typed-"+stamp+".jsonl", "application/x-ndjson",
				[]byte(issue1833TypedJSONL), form)
			reg := issue1833Register(t, c, ref, scratchResourceConnection, "acc_1833_typed_"+stamp)
			issue1833WantTypes(t, reg, []string{"i BIGINT", "f DOUBLE", "b BOOLEAN", "s VARCHAR", "m VARCHAR", "n VARCHAR"})
			query, _ := reg["query_table"].(string)
			if n := issue1833Count(t, c, scratchResourceConnection, "SELECT sum(i) AS n FROM "+query); n != 42 {
				t.Errorf("SUM over the BIGINT column is %d; want 42", n)
			}
		})
	}
}

// TestIssue1833_NestedJSONLinesValuesAreRowsAndArrays is criterion 9.
func TestIssue1833_NestedJSONLinesValuesAreRowsAndArrays(t *testing.T) {
	c := connect(t)
	stamp := issue1833Stamp()
	body := `{"id":1,"store":{"name":"North","sqft":1200},"tags":["a","b"]}
{"id":2,"store":{"name":"South","sqft":800},"tags":["c"]}
`
	ref := issue1833Upload(t, c, "acc-1833-nested-"+stamp+".jsonl", "application/x-ndjson", []byte(body), "content")
	reg := issue1833Register(t, c, ref, scratchResourceConnection, "acc_1833_nested_"+stamp)
	issue1833WantTypes(t, reg, []string{"id BIGINT", `store ROW("name" VARCHAR, "sqft" BIGINT)`, "tags ARRAY(VARCHAR)"})
	query, _ := reg["query_table"].(string)
	if n := issue1833Count(t, c, scratchResourceConnection,
		"SELECT sum(store.sqft) AS n FROM "+query+" WHERE store.name = 'North'"); n != 1200 {
		t.Errorf("the ROW field sums to %d; want 1200", n)
	}
	if n := issue1833Count(t, c, scratchResourceConnection,
		"SELECT count(*) AS n FROM "+query+" CROSS JOIN UNNEST(tags) AS u(tag)"); n != 3 {
		t.Errorf("UNNEST gave %d rows; want 3", n)
	}
}

// TestIssue1833_AKeyThatIsAnObjectAndAListIsRefused is criterion 10.
func TestIssue1833_AKeyThatIsAnObjectAndAListIsRefused(t *testing.T) {
	c := connect(t)
	stamp := issue1833Stamp()
	body := "{\"id\":1,\"shape\":{\"a\":1}}\n{\"id\":2,\"shape\":[1,2]}\n"
	ref := issue1833Upload(t, c, "acc-1833-conflict-"+stamp+".jsonl", "application/x-ndjson", []byte(body),
		"content_base64")
	issue1833Refused(t, c, ref, "acc_1833_conflict_"+stamp, "line 2", `"shape"`, "a list here and was an object")
}

// TestIssue1833_ALegacyJSONLinesRegistrationStaysVarchar is criterion 11. A
// registration made before typing is one whose row the migration marked; that
// row is written here as a deployment upgraded from before #1833 holds it, the
// only way one can exist now.
func TestIssue1833_ALegacyJSONLinesRegistrationStaysVarchar(t *testing.T) {
	c := connect(t)
	stamp := issue1833Stamp()
	table := "acc_1833_legacy_" + stamp
	out := c.call("trino_export", map[string]any{
		"sql": "SELECT BIGINT '1' AS id, 'a' AS note", "format": "jsonl",
		"name": "Acceptance 1833 legacy " + stamp, "purpose": issue1833Purpose,
	})
	assetID, _ := out["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("trino_export wrote no asset: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/portal/assets/"+assetID, http.NoBody) })
	reg := issue1833Register(t, c, "mcp:asset:"+assetID, scratchConnection, table)
	regID, _ := reg["registration_id"].(string)
	issue1833MakeLegacy(t, regID)

	status, body := c.rest(http.MethodPut, "/api/v1/portal/assets/"+assetID+"/content",
		strings.NewReader("{\"id\":2,\"note\":\"b\",\"score\":1.5}\n"))
	if status != http.StatusOK {
		t.Fatalf("writing the new version: %d %v", status, body)
	}
	list := c.call("manage_table", map[string]any{"action": "list", "reference": "mcp:asset:" + assetID})
	regs, _ := list["table_registrations"].([]any)
	if len(regs) != 1 {
		t.Fatalf("want one registration: %v", list)
	}
	followed, _ := regs[0].(map[string]any)
	issue1833WantTypes(t, followed, []string{"id VARCHAR", "note VARCHAR", "score VARCHAR"})

	again := issue1833Register(t, c, "mcp:asset:"+assetID, scratchConnection, table)
	issue1833WantTypes(t, again, []string{"id BIGINT", "note VARCHAR", "score DOUBLE"})
}

// issue1833MakeLegacy marks a registration as one made before typed JSON-lines
// columns, the row migration 000152 leaves on an upgraded deployment.
func issue1833MakeLegacy(t *testing.T, regID string) {
	t.Helper()
	db, err := sql.Open("postgres", issue1682DevDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := db.ExecContext(ctx,
		`UPDATE table_registrations SET all_varchar = TRUE,
		 columns = (SELECT jsonb_agg(jsonb_build_object('name', c->>'name', 'type', 'VARCHAR'))
		            FROM jsonb_array_elements(columns) c)
		 WHERE id = $1`, regID)
	if err != nil {
		t.Fatalf("marking the registration legacy: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("marked %d registrations; want 1", n)
	}
}

// TestIssue1833_SampleSQLAndTheListingFollowTheFormat is criterion 12.
func TestIssue1833_SampleSQLAndTheListingFollowTheFormat(t *testing.T) {
	c := connect(t)
	stamp := issue1833Stamp()
	cases := []struct {
		format, filename, contentType string
		body                          []byte
		wantCast                      bool
		wantFirst                     string
	}{
		{"csv", "acc-1833-sample-" + stamp + ".csv", "text/csv", []byte("id,name\n1,a\n"), true, "id VARCHAR"},
		{"jsonl", "acc-1833-sample-" + stamp + ".jsonl", "application/x-ndjson", []byte("{\"id\":1}\n"), false, "id BIGINT"},
		{"parquet", "acc-1833-sample-" + stamp + ".parquet", tableparquet.ContentType, issue1833Fixture(t, "all_types"),
			false, "b BOOLEAN"},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			ref := issue1833Upload(t, c, tc.filename, tc.contentType, tc.body, "content_base64")
			reg := issue1833Register(t, c, ref, scratchResourceConnection, "acc_1833_sample_"+tc.format+"_"+stamp)
			sample, _ := reg["sample_sql"].(string)
			if hasCast := strings.Contains(sample, "CAST(") && strings.Contains(sample, "every column is VARCHAR"); hasCast != tc.wantCast {
				t.Errorf("sample_sql for %s: %q; want the CAST comment: %v", tc.format, sample, tc.wantCast)
			}
			list := c.call("manage_table", map[string]any{"action": "list", "reference": ref})
			regs, _ := list["table_registrations"].([]any)
			if len(regs) != 1 {
				t.Fatalf("want one registration: %v", list)
			}
			row, _ := regs[0].(map[string]any)
			if types := issue1833Types(row); len(types) == 0 || types[0] != tc.wantFirst {
				t.Errorf("the listing's column_types start %v; want %q", types, tc.wantFirst)
			}
		})
	}
}

// issue1833EveryTrinoType is one row of every Trino type the export mapping
// names, and a row of nulls.
const issue1833EveryTrinoType = `SELECT * FROM (VALUES
 (true, TINYINT '-7', SMALLINT '300', INTEGER '70000', BIGINT '9007199254740993', REAL '1.25', DOUBLE '3.141592653589793',
  DECIMAL '12345.67', CAST('-12345678901234567890123456.123456789012' AS DECIMAL(38,12)), VARCHAR 'line' || chr(10) || 'break',
  CAST('ab' AS CHAR(3)), JSON '{"a":[1,2]}', UUID '6f1c2e8a-6d1e-4c5b-9a3e-0e5f0e5f0e5f', IPADDRESS '10.0.0.1',
  X'0001FF', DATE '2024-05-01', TIMESTAMP '2024-05-01 12:34:56.789', TIMESTAMP '2024-05-01 12:34:56.789123',
  TIMESTAMP '2024-05-01 12:34:56.789123 +02:00', ARRAY[BIGINT '1', NULL, 3], MAP(ARRAY['k', 'n'], ARRAY[DOUBLE '1.5', NULL]),
  CAST(ROW(7, 'x', ARRAY[DECIMAL '1.5']) AS ROW(a BIGINT, b VARCHAR, c ARRAY(DECIMAL(3,1))))),
 (NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL,
  NULL, NULL, NULL)
) AS v(b, ti, si, i, bi, r, d, dec, wide, v, ch, js, u, ip, vb, dt, ts3, ts6, tz, arr, m, rw)`

// TestIssue1833_ATrinoExportRegistersBackToItsRows is criterion 13: every Trino
// type the mapping names, exported as Parquet and registered, reads back as the
// query's rows. The types Parquet stores as text read back as the text of the
// value, and a timestamp with a time zone as its instant in UTC, which the
// export's message says.
func TestIssue1833_ATrinoExportRegistersBackToItsRows(t *testing.T) {
	c := connect(t)
	stamp := issue1833Stamp()
	out := c.call("trino_export", map[string]any{
		"sql": issue1833EveryTrinoType, "format": "parquet", "name": "Acceptance 1833 every type " + stamp,
		"purpose":  issue1833Purpose,
		"resource": map[string]any{"path": "acceptance/issue-1833", "filename": "acc-1833-every-" + stamp + ".parquet"},
	})
	landed := issue1663Landing(t, out)
	reference, _ := landed["reference"].(string)
	message, _ := out["message"].(string)
	for _, want := range []string{"tz read back in UTC", "js (json)", "u (uuid)", "ip (ipaddress)", "ch (char)"} {
		if !strings.Contains(message, want) {
			t.Errorf("the export's message does not say %q: %s", want, message)
		}
	}
	reg := issue1833Register(t, c, reference, scratchResourceConnection, "acc_1833_every_"+stamp)
	issue1833WantTypes(t, reg, []string{
		"b BOOLEAN", "ti TINYINT", "si SMALLINT", "i INTEGER", "bi BIGINT", "r REAL", "d DOUBLE", "dec DECIMAL(7,2)",
		"wide DECIMAL(38,12)", "v VARCHAR", "ch VARCHAR", "js VARCHAR", "u VARCHAR", "ip VARCHAR", "vb VARBINARY",
		"dt DATE", "ts3 TIMESTAMP(6)", "ts6 TIMESTAMP(6)", "tz TIMESTAMP(6)", "arr ARRAY(BIGINT)",
		"m MAP(VARCHAR, DOUBLE)", `rw ROW("a" BIGINT, "b" VARCHAR, "c" ARRAY(DECIMAL(3,1)))`,
	})
	query, _ := reg["query_table"].(string)
	source := `SELECT b, ti, si, i, bi, r, d, dec, wide, v, CAST(ch AS VARCHAR), json_format(js), CAST(u AS VARCHAR),
 CAST(ip AS VARCHAR), vb, dt, CAST(ts3 AS TIMESTAMP(6)), ts6, CAST(tz AT TIME ZONE 'UTC' AS TIMESTAMP(6)), arr, m, rw
 FROM (` + issue1833EveryTrinoType + `) q`
	issue1833Same(t, c, scratchResourceConnection, "SELECT * FROM "+query, source)
}

// TestIssue1833_AScriptExportsParquetAndATypedTableInOneCall is criterion 14.
func TestIssue1833_AScriptExportsParquetAndATypedTableInOneCall(t *testing.T) {
	c := connect(t)
	stamp := issue1833Stamp()
	name := "acc-1833-" + stamp
	table := "acc_1833_script_" + stamp
	source := fmt.Sprintf(`
out = platform.export(
    name="Acceptance 1833 script",
    rows=[{"id": 1, "amount": 1.5, "ok": True, "who": "a", "tags": ["x", "y"], "shop": {"n": "N", "s": 3}},
          {"id": 2, "amount": 2, "ok": False, "who": None, "tags": [], "shop": {"n": "S", "s": 4}}],
    format="parquet",
    destination="resources",
    key="acceptance/issue-1833/%s.parquet",
    register={"connection": %q, "table_name": %q},
)
platform.save_state({
    "query_table": out["table"]["query_table"],
    "registration_id": out["table"]["registration_id"],
    "format": out["table"]["format"],
    "types": [c["name"] + " " + c["type"] for c in out["table"]["column_types"]],
})
`, name, scratchResourceConnection, table)
	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source,
		"description": "Acceptance #1833: rows exported as Parquet and registered in one call.",
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	state := issue1663RunScript(t, c, name)
	query, _ := state["query_table"].(string)
	if query == "" {
		t.Fatalf("the export made no table: %v", state)
	}
	if id, _ := state["registration_id"].(string); id != "" {
		t.Cleanup(func() {
			_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
		})
	}
	if format, _ := state["format"].(string); format != "parquet" {
		t.Errorf("the table is read as %q; want parquet", format)
	}
	// Read the way a script author reads it: column_types is a list of
	// {name, type} entries, the same shape manage_table, a search hit and a
	// fetched document carry under that key.
	want := `[id BIGINT amount DOUBLE ok BOOLEAN who VARCHAR tags ARRAY(VARCHAR) shop ROW("n" VARCHAR, "s" BIGINT)]`
	if got := fmt.Sprint(state["types"]); got != want {
		t.Errorf("the table's column types are %s; want %s", got, want)
	}
	issue1833Same(t, c, scratchResourceConnection, "SELECT * FROM "+query, `SELECT CAST(id AS BIGINT), CAST(amount AS DOUBLE),
 CAST(ok AS BOOLEAN), CAST(who AS VARCHAR), CAST(tags AS ARRAY(VARCHAR)), CAST(shop AS ROW(n VARCHAR, s BIGINT))
 FROM (VALUES (1, 1.5, true, 'a', ARRAY['x', 'y'], ROW('N', 3)), (2, 2, false, NULL, ARRAY[], ROW('S', 4)))
 AS v(id, amount, ok, who, tags, shop)`)
}

// TestIssue1833_TheToolsSayWhatEachFormatDeclares holds the agent-facing text:
// manage_table names the three formats and which of them are typed, and
// trino_export offers parquet.
func TestIssue1833_TheToolsSayWhatEachFormatDeclares(t *testing.T) {
	c := connect(t)
	descriptions := map[string]string{}
	schemas := map[string]string{}
	for _, tool := range c.tools() {
		descriptions[tool.Name] = tool.Description
		schemas[tool.Name] = fmt.Sprint(tool.InputSchema)
	}
	for _, want := range []string{"CSV, JSON-lines or Parquet", "A CSV's columns all come back as VARCHAR",
		"typed from every record", "declares its own columns and types", "column_types"} {
		if !strings.Contains(descriptions["manage_table"], want) {
			t.Errorf("manage_table's description does not say %q", want)
		}
	}
	if !strings.Contains(schemas["trino_export"], "parquet") {
		t.Error("trino_export's format does not offer parquet")
	}
}
