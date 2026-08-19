package scriptlayer

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// manageScriptDescription is the tool description. It carries the dialect
// contract in-context on purpose: the author is a model, the language is one it
// has read far less of than Python, and the failures it produces are
// predictable — an import, a try block, an f-string, a clock read. Stating what
// is absent up front costs a paragraph and saves a round trip per script.
const manageScriptDescription = "Author, validate, and dry-run managed scripts: small Starlark programs the " +
	"platform stores, versions, and governs so a solved process (a KPI report, a recurring export) can be " +
	"re-run without deriving it again through a conversation. Write a script when the logic is settled and " +
	"the work will repeat; keep using the query tools directly while you are still exploring. " +
	"Call command=help before writing your first one: Starlark is Python-shaped but deliberately smaller, " +
	"and help states exactly what is available. The loop is create or update, then validate (parses and " +
	"reports what the script would reach, runs nothing), then run_draft (executes for real under YOUR " +
	"identity and persona, with tighter limits, persisting nothing). " +
	"A script does not run on its own until a version is approved; until then run_draft is the only way it " +
	"executes, and it can reach nothing you could not reach yourself."

// dialectContract is the help command's body: what a script is, what is
// predeclared, and what a Python instinct will reach for and not find.
const dialectContract = `Managed scripts are written in Starlark: Python-shaped syntax, deliberately smaller.

WHAT IS AVAILABLE
  platform.query(sql, connection=..., params={})  Run read-only SQL. Returns
      {"columns": [...], "rows": [...], "row_count": n}; rows are dicts keyed by
      column name. A script cannot write: a statement that modifies state,
      such as INSERT, UPDATE, DELETE, CREATE or DROP, is refused. Compute the
      result with SELECT and write it with platform.export.
      Use :name placeholders and pass the values in params; the platform
      quotes them by type. Never build SQL by string concatenation.
      A date binds as a quoted string, so compare it against a DATE column as
      "DATE :day", which renders the standard date literal DATE '2026-08-12'.
      A query whose result is truncated by the row cap FAILS rather than
      returning a partial answer: aggregate in SQL, or narrow the query.
  platform.export(name, rows, format="csv", destination="portal", key=None)
      Declare an output. Formats: csv, json, markdown, text. name is the
      output's identity across runs: the same name from the same script is one
      portal asset, and every run adds a version of it, so a dashboard keeps its
      identity instead of a new asset appearing every morning.
      destination says where the output goes. The default, "portal", is that
      versioned asset. A destination approved for a bucket delivers the same
      bytes to an external system instead; the script names only the
      destination, and the connection, bucket and prefix come from what was
      approved. Exporting one result to both is two calls with one name.
      key is the object key beneath a bucket destination's granted prefix
      ("2026/08/sales.csv"); it defaults to the output name plus the format's
      extension, and the portal takes no key because it stores its own objects.
      destination and key must be passed BY NAME. Only name, rows and format may
      be positional, because where a script writes has to be readable from its
      source before anyone approves it.
      In a draft run this writes nothing, wherever it was addressed, and
      reports the shape and size the output would have: the rows are serialized
      in the declared format to measure them, so the size is the one a real run
      writes.
  print(...)  Goes to the run log (capped; anything larger is an export).
  run.run_id, run.fire_time, run.params["name"]  The frozen run record.
      A parameter is typed string, int, float, bool, date, enum or connection.
      Declare a connection parameter for a connection the caller chooses rather
      than a string: the surfaces that ask for one offer the connections this
      script may reach, and a name outside them is refused where it was
      entered instead of failing the run.
  json.encode / json.decode / json.indent
  date.of, date.parse, date.format, date.add_days, date.add_months,
      date.diff_days, date.start_of_month, date.weekday  All dates are
      YYYY-MM-DD strings. date.format uses YYYY, MM and DD tokens.
  The Starlark built-ins: len, range, sorted, min, max, sum, enumerate, zip,
      str, int, float, dict, list, set, any, all, fail, and the string, list and
      dict methods (including "{}".format(x) and "%d" % x).

WHAT IS NOT, AND WHAT TO WRITE INSTEAD
  import              There is no module system. json and date are already here.
  try / except        Errors fail the run by design, so the failure is recorded
                      rather than swallowed. Check first, or call fail("why").
  while               Unbounded loops are off so a script's cost is readable
                      from its source. Loop over a list, or do it in SQL.
  recursion           Off, for the same reason. Flatten it into a loop.
  f"..."              Use "{}".format(x) or "%s" % x.
  class               Use dicts for structured values and functions for behavior.
  datetime / now()    There is no clock. Reading one would make the run
                      unreproducible; the fire time is pinned on run.fire_time.
  random              There is no randomness, for the same reason.
  open / requests     There is no filesystem and no network. The platform is the
                      only outside world a script has.
  credentials         Never in the source. Name a connection; the platform holds
                      its credentials and authorizes the call.

WHAT DETERMINISTIC MEANS HERE
  Same script version + same parameters + same underlying data produce the same
  output. The warehouse still changes between runs, and that is the point of
  re-running: the promise is that the SCRIPT contributes no variation of its own.

THE LOOP
  create -> validate -> run_draft -> patch -> validate -> run_draft. validate
  parses and reports the capabilities, connections and destinations the script
  reaches; run_draft executes it under your own identity with nothing persisted.`

// example is one built-in worked script, retrievable by name through get.
type example struct {
	name        string
	description string
	source      string
}

// examples are the seeded worked scripts. Two, not ten: they exist to show the
// shape of a script and the two idioms every report needs — a date derived from
// the pinned fire time, and a bound parameter — not to be a cookbook that
// invites copying without reading.
var examples = []example{
	{
		name:        "example-daily-sales",
		description: "A daily report: derive yesterday from the pinned fire time, query one bound parameter, export the rows.",
		source: `# A daily sales report. Every date comes from the run's pinned fire time,
# so re-running this months later reproduces exactly what it said.
report_date = date.add_days(date.of(run.fire_time), -1)
print("reporting on " + report_date)

result = platform.query(
    connection = "primary",
    sql = """
        SELECT region, sum(amount) AS total, count(*) AS orders
          FROM sales.orders
         WHERE order_date = DATE :day
         GROUP BY region
         ORDER BY region
    """,
    params = {"day": report_date},
)

rows = result["rows"]
print("regions: %d" % len(rows))
for row in rows:
    print("%s %s" % (row["region"], row["total"]))

platform.export(
    name = "daily-sales-" + report_date,
    rows = rows,
    format = "csv",
)
`,
	},
	{
		name:        "example-region-rollup",
		description: "A parameterized rollup: a declared enum and a bound list, with the empty case handled instead of raised.",
		source: `# A month-to-date rollup for a set of regions. Declare the script's params as
# {"name": "regions", "type": "string", "required": True} and
# {"name": "grain", "type": "enum", "values": ["region", "channel"],
#  "required": True}.
today = date.of(run.fire_time)
month_start = date.start_of_month(today)
regions = [r.strip() for r in run.params["regions"].split(",") if r.strip()]

if not regions:
    # There is no try/except: stop deliberately, with a message the run record
    # will carry.
    fail("no regions were supplied")

grain = run.params["grain"]
result = platform.query(
    connection = "primary",
    sql = """
        SELECT region, channel, sum(amount) AS total
          FROM sales.orders
         WHERE order_date >= DATE :start AND order_date <= DATE :end
           AND region IN :regions
         GROUP BY region, channel
    """,
    params = {"start": month_start, "end": today, "regions": regions},
)

totals = {}
for row in result["rows"]:
    key = row[grain]
    totals[key] = totals.get(key, 0) + row["total"]

summary = [{"key": k, "total": totals[k]} for k in sorted(totals)]
print(json.encode(summary))
platform.export(name = "region-rollup", rows = summary, format = "json")
`,
	},
}

// builtinExample looks up a seeded example by name.
func builtinExample(name string) (example, bool) {
	for _, ex := range examples {
		if ex.name == name {
			return ex, true
		}
	}
	return example{}, false
}

// fields renders a built-in example as a get response. It is marked builtin so
// nobody mistakes it for a stored script and tries to patch it.
func (e example) fields() map[string]any {
	return map[string]any{
		fieldName: e.name, "description": e.description, fieldSource: e.source,
		"builtin": true,
		"message": "This is a built-in worked example, not a stored script. Copy it into create and edit from there.",
	}
}

// handleHelp returns the dialect contract, the capability surface, and the
// example names.
func (*Handle) handleHelp(_ context.Context, _ manageScriptInput) (*mcp.CallToolResult, any, error) {
	names := make([]map[string]any, 0, len(examples))
	for _, ex := range examples {
		names = append(names, map[string]any{fieldName: ex.name, "description": ex.description})
	}
	return jsonResult(map[string]any{
		"dialect":      dialectContract,
		"capabilities": scriptrun.Capabilities,
		"limits": map[string]any{
			"draft_max_steps":  scriptrun.DraftMaxSteps,
			"draft_timeout":    scriptrun.DraftTimeout.String(),
			"draft_max_rows":   scriptrun.DraftMaxRows,
			"log_bytes":        scriptrun.MaxLogBytes,
			"max_source_bytes": script.MaxSourceBytes,
			"note": "A draft run is bounded more tightly than an approved run will be. " +
				"A script error is deterministic, so it is never retried.",
		},
		"examples":        names,
		"read_an_example": "Call get with name=" + examples[0].name + " to read one.",
	})
}
