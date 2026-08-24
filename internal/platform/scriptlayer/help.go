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
	"A saved script runs: run_script executes its latest saved version as the script's own principal, " +
	"presenting the roles you held when you saved it, and a schedule fires it the same way."

// DialectContract is the help command's body: what a script is, what is
// predeclared, and what a Python instinct will reach for and not find.
// It is exported for the built-in knowledge pages (#1390): the authoring page
// derives its dialect section from this constant, so the two cannot drift.
const DialectContract = `Managed scripts are written in Starlark: Python-shaped syntax, deliberately smaller.

WHAT IS AVAILABLE
  platform.query(sql, connection=..., params={})  Run read-only SQL. Returns
      {"columns": [...], "rows": [...], "row_count": n}; rows are dicts keyed by
      column name. It is the read tool, so a statement that modifies state —
      INSERT, UPDATE, DELETE, CREATE, DROP — is refused by it, and the write
      tool is reached with platform.call("trino_execute", {...}).
      Use :name placeholders and pass the values in params; the platform
      quotes them by type. Never build SQL by string concatenation.
      A date binds as a quoted string, so compare it against a DATE column as
      "DATE :day", which renders the standard date literal DATE '2026-08-12'.
      A query whose result is truncated by the row cap FAILS rather than
      returning a partial answer: aggregate in SQL, or narrow the query.
      A SQL DECIMAL column arrives in the rows as a STRING, not a number, so
      pass it through float() before arithmetic:
      sum([float(r["total"]) for r in rows]).
  platform.export(name, rows, format="csv", destination="portal", key=None)
      Declare an output. rows is a list of dicts serialized in the declared
      format, or a string body written verbatim so a script can compose a
      document: an HTML or JSX dashboard, a prose report, a hand-assembled
      markdown page. Formats: csv, json, markdown, text, html, jsx. csv and
      json require rows, so a data feed stays well-formed by construction;
      html and jsx take only a string body; markdown and text accept either.
      A document is produced two ways, and the choice is made here, not later.
      Compose the whole document in the script when each run is its own kept
      document (a dated archive series), when the structure varies with the
      data (a section that appears only when a threshold trips), or when
      nobody will edit the presentation. Publish the document once and refresh
      only its data region with platform.publish_data when there is one
      stable-named asset at one URL whose layout a person may edit and whose
      numbers alone move per run. Both directions cost something: composing
      the whole document every run overwrites the current version wholesale,
      so a layout edit made in the portal is destroyed by the next fire, and
      publish_data against a document whose structure must vary leaves a data
      region the markup cannot render.
      name is the output's identity across runs: the same name from the same
      script is one portal asset, and every run adds a version of it, so a
      dashboard keeps its identity instead of a new asset appearing every
      morning.
      destination says where the output goes. The default, "portal", is that
      versioned asset. A destination the deployment configures for a bucket
      delivers the same bytes to an external system instead; the script names
      only the destination, and the connection, bucket and prefix come from
      the configuration. Exporting one result to both is two calls with one
      name.
      key is the object key beneath a bucket destination's configured prefix
      ("2026/08/sales.csv"); it defaults to the output name plus the format's
      extension, and the portal takes no key because it stores its own objects.
      destination and key must be passed BY NAME. Only name, rows and format may
      be positional, because where a script writes has to be readable from its
      source.
      In a draft run this writes nothing, wherever it was addressed, and
      reports the shape and size the output would have: the content is
      serialized in the declared format to measure it, so the size is the one
      a real run writes.
  platform.publish_data(name, data)
      Refresh the data region of an existing dashboard without touching its
      markup. name is the same output identity platform.export uses, and must
      already be an html, jsx, or markdown document of this script's; data is
      a dict or list, serialized as JSON and spliced into the interior of the
      ONE element matching ` + script.DataRegionSelector + ` — conventionally
      <script type="application/json" id="data">...</script>, whose content
      the dashboard's own code reads and renders (a markdown document carries
      the island as a raw-HTML block). The write is a new version of the
      asset, so every refresh is a self-contained as-of snapshot; a document
      without the marked region fails the run rather than being written
      anywhere else. Publish the presentation once with
      platform.export(name, body, format="html") (or "jsx" or "markdown"),
      then let the schedule refresh only the numbers: the layout can be
      edited in the asset like any document, with no script change needed.
      Zero rows is your decision, as with any export: publish the empty
      structure or fail().
      In a draft run this writes nothing and reports the payload size it
      would splice.
  platform.call(tool, args={})  Call any platform tool by name and get its
      structured result. This is the same mechanism the three helpers above
      are built on, with the tool left to you, and it is how a script reaches
      everything else the platform can do: writing a table with
      trino_execute, fetching an external API server-side with
      api_invoke_endpoint, reading an object with s3_get_object, capturing a
      memory, updating the catalog.
      A run acts on what its author owns: it authenticates as script:<name>
      and carries the author's address, so it can refresh or patch a
      dashboard the author owns. An asset merely SHARED with them is not
      inherited.
      A script may call every tool ITS AUTHOR may call. Each call is
      authorized by the persona filter at the moment it is made, presenting
      the roles you held when you saved the version, so a tool your persona
      does not allow is refused in the persona filter's own words. There is
      no separate script allowlist to consult.
      Prefer the three helpers where they apply. They are not a restriction
      you are working around: platform.query pushes the row cap down into the
      query and FAILS a truncated result, which a raw trino_query call hands
      you to notice yourself, and platform.export records what it wrote on the
      run, which a write made by tool call does not appear in.
      The result byte cap applies to every call.
      A tool that answers with plain text rather than a structured object
      arrives as {"text": "..."}; decode it yourself if it is JSON.
      args is a dict of the tool's own arguments, passed through unchanged:
      platform.call("trino_execute", {"connection": "warehouse",
                                      "sql": "INSERT INTO t VALUES (1)"}).
      Name the tool with a string literal, and write the args dict out in
      the call. validate reads both, which is how a reader learns what a
      script reaches without reading the Starlark: a computed tool name is
      reported as a gap in the tool list, and a computed args dict as a gap
      in the connection list.
      run_script and manage_script run_draft are refused from inside a run.
      A run executes one at a time, so a script waiting on a run it started
      would wait on the worker running it. Give the second script its own
      schedule.
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
  sum(iterable, start=0)  Adds numbers left to right. Starlark's own universe
      has no sum, so the platform predeclares it; a non-number element is
      refused by position rather than concatenated.
  The Starlark built-ins: len, range, sorted, min, max, enumerate, zip,
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
  open / requests     There is no filesystem and no direct network. The platform
                      is the only outside world a script has: reach an external
                      API through a configured connection with
                      platform.call("api_invoke_endpoint", {...}).
  credentials         Never in the source. Name a connection; the platform holds
                      its credentials and authorizes the call.

WHAT DETERMINISTIC MEANS HERE
  Same script version + same parameters + same underlying data produce the same
  output. The warehouse still changes between runs, and that is the point of
  re-running: the promise is that the SCRIPT contributes no variation of its own.

THE LOOP
  create -> validate -> run_draft -> patch -> validate -> run_draft. validate
  parses and reports the capabilities, the tools platform.call names, the
  connections, and the destinations the script's OUTPUTS go to; run_draft
  executes it under your own identity with nothing persisted.
  Both act on the source you send with the call, and on the saved version when
  you send none: a save is immediately the version run_script executes and a
  schedule fires, so sending the edit is how you try it without making it live.
  validate also reports a destination this deployment does not declare, which
  the run would otherwise refuse only after your queries had already run.`

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

// knowledgePageRefPrefix is the fetchable form of a knowledge-page key. It is
// written out rather than taken from pkg/portal/knowledgepage so the script
// seam does not depend on the portal's page store to name a page it only
// points at; the scheme is pinned by the drift test in knowledgebuiltin.
const knowledgePageRefPrefix = "mcp:knowledge_page:"

// KnowledgePage names one built-in knowledge page an author should read.
type KnowledgePage struct {
	// Slug is the page's reconcile key and the only identifier stable across
	// deployments: a built-in page's row id is generated at reconcile time, so
	// it differs per deployment and cannot be named in shipped text.
	Slug string `json:"slug"`
	// Reference is the slug in the form fetch takes.
	Reference string `json:"reference"`
	// Summary says what the page answers, so an author fetches the one that
	// bears on the decision in front of it rather than all of them.
	Summary string `json:"summary"`
}

// KnowledgePages is the reading `manage_script help` names, so the tool an
// agent is told to call before writing its first script is the tool that
// routes it to the platform's own authoring guidance instead of leaving that
// guidance to whatever a search happens to rank (#1476).
//
// The slugs are declared here rather than in knowledgebuiltin because
// knowledgebuiltin already imports this package for the dialect contract and
// the reverse import would cycle; a test there fails when the two sets drift.
var KnowledgePages = []KnowledgePage{
	{
		Slug:      "platform-writing-managed-scripts",
		Reference: knowledgePageRefPrefix + "platform-writing-managed-scripts",
		Summary: "The dialect and the authoring loop: what Starlark deliberately lacks, what a " +
			"script may call and the persona that decides it, and what a save makes runnable.",
	},
	{
		Slug:      "platform-script-outputs-and-export-identity",
		Reference: knowledgePageRefPrefix + "platform-script-outputs-and-export-identity",
		Summary: "Where an output lands and what identity it keeps across runs: a stable name " +
			"refreshes one asset, a dated name builds an archive, and a bucket destination " +
			"delivers the same bytes elsewhere.",
	},
	{
		Slug:      "platform-semi-dynamic-dashboards",
		Reference: knowledgePageRefPrefix + "platform-semi-dynamic-dashboards",
		Summary: "Choosing between composing a whole document every run and publishing one " +
			"document whose data region a schedule refreshes, and the mechanics of the " +
			"second.",
	},
	{
		Slug:      "platform-provenance-and-the-capture-loop",
		Reference: knowledgePageRefPrefix + "platform-provenance-and-the-capture-loop",
		Summary: "Naming sources with call references so an output's provenance is exact, and " +
			"the loop that turns session knowledge into reviewed catalog knowledge.",
	},
}

// handleHelp returns the dialect contract, the capability surface, the example
// names, and the built-in pages that carry the reasoning the contract states
// only in outline.
func (*Handle) handleHelp(_ context.Context, _ manageScriptInput) (*mcp.CallToolResult, any, error) {
	names := make([]map[string]any, 0, len(examples))
	for _, ex := range examples {
		names = append(names, map[string]any{fieldName: ex.name, "description": ex.description})
	}
	return jsonResult(map[string]any{
		"dialect":      DialectContract,
		"capabilities": scriptrun.Capabilities,
		"limits": map[string]any{
			"draft_max_steps":  scriptrun.DraftMaxSteps,
			"draft_timeout":    scriptrun.DraftTimeout.String(),
			"draft_max_rows":   scriptrun.DraftMaxRows,
			"log_bytes":        scriptrun.MaxLogBytes,
			"max_source_bytes": script.MaxSourceBytes,
			"note": "A draft run is bounded more tightly than a platform run will be. " +
				"A script error is deterministic, so it is never retried.",
		},
		"examples":        names,
		"read_an_example": "Call get with name=" + examples[0].name + " to read one.",
		"see_also":        KnowledgePages,
		"read_a_page":     "Call fetch with the reference to read one in full.",
	})
}
