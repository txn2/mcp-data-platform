//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Acceptance for #2455: structured values for ranges, multiranges,
// intervals, composites, enums, hstore, ltree, tsvector, inet and cidr, and
// pgvector's vector, on the dev stack's `orders` (postgres-target, PostgreSQL
// 17 with pgvector installed).
//
// Wire forms: manage_script takes command, name and source as strings and
// allow_writes as a boolean. The values bound as parameters are Starlark
// dicts and lists in the script source.

const conn2455 = "orders"

// kinds2455 makes a schema with one row holding a value of each type and
// returns it. The extensions are created in public once, so dropping the
// schema leaves them.
func kinds2455(t *testing.T) string {
	t.Helper()
	for _, ext := range []string{"hstore", "ltree", "btree_gist", "vector"} {
		targetPSQL(t, "CREATE EXTENSION IF NOT EXISTS "+ext+" SCHEMA public")
	}
	schema := "acc2455_" + unique1579()
	targetPSQL(t, "CREATE SCHEMA "+schema)
	t.Cleanup(func() { targetPSQL(t, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	targetPSQL(t, "CREATE TYPE "+schema+".mood AS ENUM ('calm', 'busy')")
	targetPSQL(t, "CREATE TYPE "+schema+".addr AS (street text, zip int)")
	targetPSQL(t, "CREATE TABLE "+schema+".kinds (r int4range, ts tstzrange, mr int4multirange, iv interval, ip inet, net cidr, "+
		"m "+schema+".mood, a "+schema+".addr, h public.hstore, v public.vector(3), l public.ltree, tsv tsvector)")
	targetPSQL(t, "INSERT INTO "+schema+".kinds VALUES ('[1,5)', '[2026-11-03 10:00+00,)', '{[1,3),[7,10)}', "+
		"'1 year 2 months 3 days 4.000005 seconds', '10.1.2.3/24', '10.1.0.0/16', 'busy', ROW('1 Main', 90210), "+
		"'k=>v, empty=>NULL', '[1.5,2,-3]', 'top.mid.leaf', 'a fat cat')")
	return schema
}

// want2455 is the row the mapping returns, as JSON decodes it.
const want2455 = `{
  "r": {"lower": 1, "upper": 5, "lower_inc": true, "upper_inc": false, "empty": false},
  "ts": {"lower": "2026-11-03T10:00:00Z", "upper": null, "lower_inc": true, "upper_inc": false, "empty": false},
  "mr": [{"lower": 1, "upper": 3, "lower_inc": true, "upper_inc": false, "empty": false},
         {"lower": 7, "upper": 10, "lower_inc": true, "upper_inc": false, "empty": false}],
  "iv": {"months": 14, "days": 3, "microseconds": 4000005, "text": "1 year 2 mons 3 days 00:00:04.000005"},
  "ip": "10.1.2.3/24", "net": "10.1.0.0/16", "m": "busy",
  "a": {"street": "1 Main", "zip": 90210},
  "h": {"k": "v", "empty": null},
  "v": [1.5, 2, -3],
  "l": "top.mid.leaf", "tsv": "'a' 'cat' 'fat'"
}`

func TestIssue2455_EachTypeReturnsItsStructureThroughAScriptAndItsTestReplay(t *testing.T) {
	admin := connect(t)
	schema := kinds2455(t)
	name := "acceptance-2455-kinds-" + unique1579()
	source := fmt.Sprintf(`def main():
    """Prints the row of every structured type and each column's type."""
    res = platform.query("SELECT * FROM %s.kinds", connection = "%s")
    print(json.encode(res["rows"][0]))
    print(json.encode({c["name"]: c["type"] for c in res["columns"]}))
`, schema, conn2455)
	ran := admin.call("manage_script", map[string]any{"command": "run_draft", "name": name, "source": source})
	if ran["status"] != "succeeded" {
		t.Fatalf("the draft failed: %v", ran)
	}
	log, _ := ran["log"].(string)
	lines := strings.Split(strings.TrimSpace(log), "\n")
	if len(lines) != 2 {
		t.Fatalf("the draft printed %q", log)
	}
	var got, want map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("the row is not JSON: %v: %s", err, lines[0])
	}
	_ = json.Unmarshal([]byte(want2455), &want)
	for col, w := range want {
		if !reflect.DeepEqual(got[col], w) {
			t.Errorf("%s = %#v, want %#v", col, got[col], w)
		}
	}
	var types map[string]string
	_ = json.Unmarshal([]byte(lines[1]), &types)
	if types["m"] != schema+".mood" || types["ip"] != "inet" || types["v"] != "vector" || types["tsv"] != "tsvector" {
		t.Errorf("the column types do not name the PostgreSQL types: %v", types)
	}

	// The script's test replays the draft's recording and reads the same
	// structures, with the database untouched.
	recording, _ := ran["recording"].(string)
	test := fmt.Sprintf(`
def test_kinds():
    """The replayed row is the recorded one."""
    testing.replay(%q)
    main()
    assert.eq(testing.outputs().log, %q)
`, recording, log)
	out := admin.call("manage_script", map[string]any{"command": "test", "name": name, "source": source + test})
	if out["ok"] != true {
		t.Fatalf("the test replay did not read the same structures: %v", out)
	}
}

func TestIssue2455_ABookingBoundAsARangeAndAnOverlapRefusedWith23P01(t *testing.T) {
	admin := connect(t)
	schema := kinds2455(t)
	targetPSQL(t, "CREATE TABLE "+schema+".bookings (room int, during tstzrange, EXCLUDE USING gist (room WITH =, during WITH &&))")
	ran := draft2428(admin, "acceptance-2455-booking", `
def main():
    """Books a room for an hour, then tries an overlapping half hour."""
    sql = "INSERT INTO `+schema+`.bookings VALUES (1, :during)"
    platform.execute(sql, connection = "`+conn2455+`", params = {"during": {"lower": "2026-11-03T10:00:00Z", "upper": "2026-11-03T11:00:00Z"}})
    clash = platform.execute(sql, connection = "`+conn2455+`", params = {"during": {"lower": "2026-11-03T10:30:00Z", "upper": "2026-11-03T12:00:00Z"}}, on_error = "return")
    print(clash["error"]["postgres"]["sqlstate"])
    back = platform.query("SELECT during FROM `+schema+`.bookings", connection = "`+conn2455+`")["rows"][0]["during"]
    print(back["lower"], back["upper"], back["lower_inc"], back["upper_inc"])
`, true)
	if ran["status"] != "succeeded" {
		t.Fatalf("the run failed: %v", ran)
	}
	log, _ := ran["log"].(string)
	if !strings.Contains(log, "23P01") {
		t.Errorf("the overlapping booking was not refused with 23P01: %q", log)
	}
	if !strings.Contains(log, "2026-11-03T10:00:00Z 2026-11-03T11:00:00Z True False") {
		t.Errorf("the stored booking does not read back as the bound range: %q", log)
	}
}

func TestIssue2455_AVectorRoundTripsAndAVectorThatIsNotPgvectorsIsText(t *testing.T) {
	admin := connect(t)
	schema := kinds2455(t)
	targetPSQL(t, "CREATE TABLE "+schema+".embeddings (id int, v public.vector(3))")
	targetPSQL(t, "CREATE TYPE "+schema+".vector AS ENUM ('[1,2,3]')")
	ran := draft2428(admin, "acceptance-2455-vector", `
def main():
    """Writes a vector bound as a list and reads it back."""
    platform.execute("INSERT INTO `+schema+`.embeddings VALUES (1, :v)", connection = "`+conn2455+`", params = {"v": [0.25, -1, 3]})
    print(json.encode(platform.query("SELECT v FROM `+schema+`.embeddings", connection = "`+conn2455+`")["rows"][0]["v"]))
    print(json.encode(platform.query("SELECT '[1,2,3]'::`+schema+`.vector AS v", connection = "`+conn2455+`")["rows"][0]["v"]))
`, true)
	if ran["status"] != "succeeded" {
		t.Fatalf("the run failed: %v", ran)
	}
	log, _ := ran["log"].(string)
	lines := strings.Split(strings.TrimSpace(log), "\n")
	var vec []float64
	if len(lines) != 2 || json.Unmarshal([]byte(lines[0]), &vec) != nil || !reflect.DeepEqual(vec, []float64{0.25, -1, 3}) {
		t.Fatalf("the pgvector value is not the list it was bound as: %q", log)
	}
	if lines[1] != `"[1,2,3]"` {
		t.Fatalf("the enum named vector is not its text: %q", lines[1])
	}
}
