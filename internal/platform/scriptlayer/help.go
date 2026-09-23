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
	"identity and persona, with tighter limits, persisting nothing unless you pass allow_writes). " +
	"A saved script runs: run_script executes its latest saved version as the script's own principal, " +
	"presenting the roles you held when you saved it, and a schedule fires it the same way. " +
	"command=versions reads the history of who wrote each version, which is not the same question as " +
	"who owns the script now."

// DialectContract is the help command's body: what a script is, what is
// predeclared, and what a Python instinct will reach for and not find.
// It is exported for the built-in knowledge pages (#1390): the authoring page
// derives its dialect section from this constant, so the two cannot drift.
const DialectContract = `Managed scripts are written in Starlark: Python-shaped syntax, deliberately smaller.

WHAT IS AVAILABLE
  platform.query(sql, connection=..., params={})  Run read-only SQL. Returns
      {"columns": [...], "rows": [...], "row_count": n}; rows are dicts keyed by
      column name, in the SELECT's column order, so rows exported as they
      came keep the query's columns where it put them. It is the read tool, so a statement that modifies state —
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
  platform.export(name, rows, format="csv", destination="portal", key=None,
                  register=None, references=None, tags=None, metadata=None)
      Declare an output. rows is a list of dicts serialized in the declared
      format, or a string body written verbatim so a script can compose a
      document: an HTML or JSX dashboard, a prose report, a hand-assembled
      markdown page. Formats: csv, json, jsonl, parquet, xlsx, markdown, text,
      html, jsx. csv, json, jsonl and parquet require rows, so a data feed
      stays well-formed by construction; html and jsx take only a string body;
      markdown and text accept either; xlsx takes a dict of sheets.
      xlsx writes an Excel workbook from {"sheets": [{"name": "Summary",
      "columns": [...], "rows": [...], "column_types": {"net": "currency"},
      "freeze": "A2", "widths": {"name": 30}, "title": "..."}, ...]}. Rows are
      dicts (projected onto columns) or lists (positional); columns defaults
      to the rows' keys in the order written. column_types are string,
      integer, decimal, currency, date, datetime and percent; a column with
      none is written as its values are. currency takes the exact decimal
      text ("1234.50") or integer cents (123450), never a float, and shows
      as 1,234.50; date takes "YYYY-MM-DD" and datetime
      "YYYY-MM-DDTHH:MM:SS"; percent takes the fraction (0.125 is 12.50%).
      The header row is bold, freeze names the first cell that scrolls, and
      title adds a row above the header. A sheet name is 1 to 31 characters
      without []:*?/\ and unique ignoring case; a control character in any
      text, a number with more than 15 significant digits, or a workbook
      over the output limit fails the export, naming the sheet, row and
      column. The record carries "sheets", each sheet's name and data rows.
      csv is RFC 4180 and writes every value as it is: a value starting with
      "=", "+", "-" or "@" is not prefixed, a quote is doubled, and a
      backslash is an ordinary character. A table registered over a CSV
      cannot carry a line break inside a value (registration refuses the
      file unless repair is set, and repair joins the value's lines with
      spaces) and reads a null back as an empty string.
      jsonl writes one JSON object per row, keys in column order: line
      breaks, backslashes, quotes and nulls all survive the table, and a
      registered table types each column from its values (all integers
      BIGINT, any fraction DOUBLE, all booleans BOOLEAN, anything else
      VARCHAR). A list or dict value is written as its JSON text, a string,
      which json_parse reads back in SQL. A string that is not valid UTF-8
      fails the export rather than being altered.
      parquet writes a typed, compressed Parquet file with each column's type
      inferred from its values by the same rules, a dict as a row and a list
      as an array: a registered table reads every value back as its type
      with no CAST. A dict, a list and a scalar are three shapes, and a key
      holding any two of them across rows fails the export, naming it.
      register={"connection": "...", "table_name": "...", "follow": True}
      makes the written file a table in the same call: it is manage_table
      register over the file the export wrote, by the reference the write
      reported, on the connection you name. table_name defaults to a slug of
      the file's name and is prefixed with your persona; follow defaults to
      True, so the next run's export moves the table onto its new version.
      It needs format "jsonl", "parquet" or "csv" and the "portal" or "resources"
      destination, is refused before anything is written otherwise, and the
      record the call returns carries "table" with the query_table to select
      from, "columns", and "column_types" as [{"name": ..., "type": ...}],
      the same entries manage_table reports.
      A registration that fails fails the run, naming the output. Pass
      register by name, as destination and key are. This is the path for
      free text into SQL: trino_execute binds no parameters, and
      INSERT ... SELECT from the registered table does.
      tags=["report:sales"] adds tags to a portal output's asset, and
      metadata={"region": "west"} (a small dict, 4 KiB as JSON) is stored on
      the version it writes beside run_id, script, script_version and
      requested_by, which the platform records itself. The asset listing
      filters on both (tag=, metadata.<key>=), and so does
      GET /api/v1/portal/scripts/runs/outputs.
      references=["mcp://global/brand/logo.svg", "mcp:asset:<id>"] declares
      what a portal document names, so the page renders the file: write the
      reference itself in the markup (<img src="mcp://global/brand/logo.svg">)
      and list it here. It is manage_asset update on the asset the export
      wrote, so only something the script's author can read may be declared,
      and declaring it lets everyone the asset is shared with load it,
      including anyone holding a public link. An empty list removes the
      asset's references; leaving the argument out leaves them alone. It
      takes a string body in html, jsx, markdown or text written to
      "portal", and is refused before anything is written otherwise. A
      refusal fails the run, naming the output. Whether or not you pass it,
      the record carries "undeclared_references": every mcp:// URI and
      mcp:asset: reference the body names that references did not list,
      each served exactly as written and resolving to nothing, and the run
      log names them too.
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
      versioned asset. "resources" writes the platform's managed-resource
      library instead, at the path given as key: a file with a stable id and
      mcp:// URI, a version history, and tables registered over it that follow
      its content, which is the destination for a data file other things read
      (a table registration, an asset that references it, a second script). A
      destination the deployment configures for a bucket delivers the same
      bytes to an external system instead; the script names only the
      destination, and the connection, bucket and prefix come from the
      configuration. Exporting one result to several places is one call each,
      sharing the name.
      key is the object key beneath a bucket destination's configured prefix
      ("2026/08/sales.csv"); it defaults to the output name plus the format's
      extension, and the portal takes no key because it stores its own objects.
      For "resources" the key is REQUIRED and is the file's path in the library
      ("datasets/orders.csv"), folder and filename: it is the file's identity
      across runs, so the same key next run records the next version of that
      same file, and the record carries its resource_id, reference, uri and
      version. The file lands in the library of the person the run acts for;
      name is its display name there.
      destination and key must be passed BY NAME. Only name, rows and format may
      be positional, because where a script writes has to be readable from its
      source.
      In a draft run this writes nothing, wherever it was addressed, and
      reports the shape and size the output would have: the content is
      serialized in the declared format to measure it, so the size is the one
      a real run writes. A register= argument then reports the table it
      would make, with preview True and no query_table. A draft started with
      allow_writes writes the output for real and makes the table, as a
      platform run would, except that a draft of a script not yet saved
      cannot write to "portal", which is the saved script's own asset.
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
      api_invoke_endpoint, reading an object with s3_object, capturing a
      memory, updating the catalog, refreshing the stored file a dashboard
      reads with manage_resource.
      That last one is how a referencing asset's data half stays current:
      manage_resource replace_content writes new bytes over a managed
      resource and keeps its id, its mcp:// URI and its filename, so every
      asset referencing it serves the new content with no asset re-saved.
      A table registered over that resource, or over an output this script
      exports, follows the version you write unless it was registered with
      follow=false: the write moves the table before it returns, its result
      says what happened to each table over the file in "table_changes", and
      the run log carries the same lines. A pinned table is reported as
      behind; nothing here moves it. To read the tables themselves, fetch the
      reference: its "tables" name the connection, the table and its columns.
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
      you to notice yourself, and platform.export keeps one asset per output
      name, a new version each run. A trino_export, api_export or
      graphql_export called here with a name does the same: the first run
      creates the script's asset for that name and every later run writes its
      next version, listed on the run's outputs marked with the tool. Pass
      resource= to keep a managed file instead; any other write made by tool
      call is in the audit log only.
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
      A run waiting on a run it started holds a worker slot while it waits,
      and runs waiting on each other can hold every slot there is. Give the
      second script its own schedule.
  platform.save_state(state)  Replace the script's state: one JSON object the
      platform keeps for the script and hands the next run as run.state. Keys
      mean whatever you say they mean; a watermark is
      {"synced_through": "2026-08-28T06:00:00Z"}, a cursor is a cursor, a
      set of ids already handled is a list. It is a dict of JSON values
      bounded at 64 KiB, because state is a cursor or a summary, not a
      dataset: a script that wants to keep a table keeps a resource.
      The write happens when the run SUCCEEDS, in the same write that marks it
      so, and only if nothing else wrote state after this run read it. A
      failed run leaves the state where it was, so a watermark never moves
      past work that did not happen. Calling it twice stages the last value;
      the run's write is one write. If another run of the same script wrote
      in between, this run fails at its write naming that run; its outputs
      stand, since they were computed from the state it read.
      An incremental job reads run.state.get("synced_through"), pulls from
      it, exports, and saves the new mark. After downtime the next fire reads
      where the last successful run stopped and needs no backfill.
      In a draft run this writes nothing and reports the state a platform run
      would have saved.
  platform.result(value)  Hand one JSON value back to whoever ran the script:
      run_script, get_run and the portal's run route return it as "result".
      Use it for a small answer -- the numbers a tile needs, the ids that
      failed a check -- that nobody needs kept as a file; anything larger is
      an output (platform.export). Set it once; a second call, a value that
      cannot be JSON, or one over the cap (limits.run_result_bytes) fails the
      run. (It is result, not return: return is a keyword.)
  platform.progress(message, done=None, total=None)  Report how far the run
      has got, e.g. platform.progress("entities", done=120, total=500). The
      latest report is shown on the running run within a few seconds, beside
      the log printed so far; call it as often as you like, since only the
      latest is written, every few seconds.
  platform.notify(channel, title, body="", link="")  Post a message to a
      notification channel an administrator configured: a Mattermost channel,
      an incoming webhook, or a named email list. Call
      platform.call("notify", {"action": "list"}) to see which channels this
      script reaches; a channel is reachable when the run's persona reaches
      the api connection behind it, and an email channel is reachable by any
      script. body is markdown, shown as far as the destination can show it.
      link defaults to this run's own page, so a post leads back to the run
      that produced it. Delivery is queued: the call returns when the message
      is accepted, not when it appears.
  platform.publish(channel, name, message="")  Post a portal asset to a
      channel: the asset's name, an excerpt of its content and a link to it.
      name is the name platform.export saved the asset under, so a script
      that builds a report and posts it says the name twice rather than
      carrying an id between the two calls. message is a line printed above
      it.
      In a draft run both are refused unless the draft was started with
      allow_writes, like every other call that persists: a message in
      somebody else's chat client is not something a dry run may leave behind.
  print(...)  Goes to the run log (capped; anything larger is an export).
  run.run_id, run.fire_time, run.params["name"], run.state  The frozen run
      record. run.state is the script's state as it stood when the run was
      created, {} for a script that has never saved any; read it with
      run.state.get("key", default).
      A parameter is typed string, int, float, bool, date, enum or connection.
      Declare a connection parameter for a connection the caller chooses rather
      than a string: the surfaces that ask for one offer the connections this
      script may reach, and a name outside them is refused where it was
      entered instead of failing the run.
  json.encode / json.decode / json.indent / json.encode_indent  encode_indent
      is encode followed by indent, taking prefix= and indent= keywords.
  xml.decode(s)  Parses an XML or SOAP document into its root element. An
      element has .tag (local name), .ns (namespace URI, "" when none),
      .attrs, .text (its own character data, trimmed) and .children (child
      elements in document order). json.encode renders an element as those
      five fields.
  xml.find(node, path) / xml.findall(node, path)  Child steps a/b/c, a
      descendant step //c, the wildcard *, [@name='value'] and a 1-based
      [n]. Names match on their local part, so an upstream's namespace
      prefix does not matter. find returns None when nothing matches;
      anything outside this path subset fails the run rather than matching
      nothing.
  xml.encode(tree)  Writes an element, or a dict of the same five fields,
      back to a document — for building a SOAP request body from data.
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
  import              There is no module system. json, xml and date are here.
  try / except        Errors fail the run by design, so the failure is recorded
                      rather than swallowed. Check first, or call fail("why").
                      A rate-limit refusal of a call is not an error the script
                      sees: the host waits the refusal's interval, within the
                      run's deadline, and issues the call again.
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
  a reserved word     These cannot name a function, a parameter, a variable or
  as a name           an attribute: and, as, async, await, break, class,
                      continue, def, del, elif, else, except, finally, for,
                      from, global, if, import, in, is, lambda, load, nonlocal,
                      not, or, pass, raise, return, try, while, with, yield.
                      load is the one an ETL script reaches for: def load(...)
                      does not parse. Name the step load_rows or write_rows.

WHAT DETERMINISTIC MEANS HERE
  Same script version + same parameters + same state read + same underlying
  data produce the same output. The warehouse still changes between runs, and
  that is the point of re-running: the promise is that the SCRIPT contributes
  no variation of its own. The state a run read is recorded on the run beside
  its parameters, so a run is explained from its own record.

THE LOOP
  create -> validate -> run_draft -> patch -> validate -> run_draft. validate
  parses and reports the capabilities, the tools platform.call names, the
  connections, and the destinations the script's OUTPUTS go to; run_draft
  executes it under your own identity with nothing persisted unless you pass
  allow_writes. run_draft also runs source that is not saved yet: send it
  with the name it will have, the params it declares and the args to bind,
  and it runs as that script with empty state, saving nothing.
  Both act on the source you send with the call, and on the saved version when
  you send none: a save is immediately the version run_script executes and a
  schedule fires, so sending the edit is how you try it without making it live.
  validate also reports a destination this deployment does not declare, which
  the run would otherwise refuse only after your queries had already run.
  A draft reads the script's live state and reports what it would have saved;
  manage_script command=state reads, replaces or clears the state itself, which
  is how a wrong watermark is corrected: clear it and let the next run start
  over.

EDITING THE SOURCE
  patch takes "edits", an ordered list of anchored edits. Each edit names the
  text it acts on and carries the text it writes, under a key that depends on
  the op:
    {"op": "replace",       "find": "<exact text>", "replace": "<new text>"}
    {"op": "insert_before", "find": "<exact text>", "text": "<new text>"}
    {"op": "insert_after",  "find": "<exact text>", "text": "<new text>"}
    {"op": "append",  "text": "<new text>"}
    {"op": "prepend", "text": "<new text>"}
  op defaults to replace. "pattern" anchors on a Go RE2 regex instead of
  "find", and "occurrence" ("first", "last", "all", or a 1-based index) acts on
  a repeated anchor. An edit that omits the key its op reads is REFUSED rather
  than treated as empty, and so is one carrying a key this list does not name:
  deleting the anchor is "replace": "" written out, never a key going
  unrecognized. An anchor matching nothing or matching ambiguously refuses the
  whole call and writes nothing, so a patch lands as sent or leaves the script
  exactly as it was. dry_run returns the diff without saving, and base_version
  refuses a patch composed against a version the script has since moved past.

WHO OWNS IT AND WHO WROTE IT
  These are two facts, and two commands answer them. get reports owner_email,
  the person the script is filed under NOW; an administrator can move a script
  to somebody else, and the address moves with it. command=versions reports the
  history newest first — the version number, its author, the roles that author
  held at that save, the status, when it was written, and what the script
  called itself then. The roles are the authority a run of that version
  presents, and the oldest entry names whoever created the script, so a
  transfer never loses the author. It carries no source; read an earlier
  version's code with command=diff.`

// example is one built-in worked script, retrievable by name through get.
type example struct {
	name        string
	description string
	source      string
}

// examples are the seeded worked scripts. Three, not ten: they exist to show
// the shape of a script and the idioms every job needs — a date derived from
// the pinned fire time, a bound parameter, and a watermark carried in the
// script's state — not to be a cookbook that invites copying without reading.
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
# {"name": "regions", "type": "list", "items": "string", "label": "Regions"} and
# {"name": "grain", "type": "enum", "values": ["region", "channel"],
#  "required": True}. A list parameter checks every element when the run is
# bound and reaches the script as a list, which platform.query binds as IN (...).
today = date.of(run.fire_time)
month_start = date.start_of_month(today)
regions = run.params["regions"]

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
	{
		name:        "example-incremental-sync",
		description: "An incremental job: read the watermark from the script's state, pull what changed since it, export, and save the new watermark.",
		source: `# An incremental pull. The window starts where the last SUCCESSFUL run
# stopped, read from the script's own state, so a fire missed to downtime is
# covered by the next one without a backfill; the window ends at the pinned
# fire time, never at a clock.
since = run.state.get("synced_through", "1970-01-01T00:00:00Z")
until = run.fire_time
print("syncing orders changed in (%s, %s]" % (since, until))

result = platform.query(
    connection = "primary",
    sql = """
        SELECT order_id, region, amount, updated_at
          FROM sales.orders
         WHERE updated_at > from_iso8601_timestamp(:since)
           AND updated_at <= from_iso8601_timestamp(:until)
         ORDER BY updated_at
    """,
    params = {"since": since, "until": until},
)

rows = result["rows"]
print("changed rows: %d" % len(rows))
if rows:
    platform.export(name = "orders-delta-" + until, rows = rows, format = "csv")

# Saved only if the run succeeds, and only if no other run of this script
# wrote state in between. A run that fails above leaves the watermark alone.
platform.save_state({"synced_through": until, "last_delta_rows": len(rows)})
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
		Slug:      "platform-asset-references-and-the-refresh-loop",
		Reference: knowledgePageRefPrefix + "platform-asset-references-and-the-refresh-loop",
		Summary: "How a document names a file instead of carrying it, and how a run refreshes " +
			"that file so every document naming it shows the new content without being " +
			"re-saved.",
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
func (h *Handle) handleHelp(_ context.Context, _ manageScriptInput) (*mcp.CallToolResult, any, error) {
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
			"run_max_steps":    h.runLimits.MaxSteps,
			"run_timeout":      h.runLimits.Timeout.String(),
			"run_max_rows":     h.runLimits.MaxRows,
			"run_result_bytes": h.runLimits.ResultMaxBytes,
			"log_bytes":        scriptrun.MaxLogBytes,
			"max_source_bytes": script.MaxSourceBytes,
			"state_bytes":      script.MaxStateBytes,
			"note": "A draft run is bounded more tightly than a platform run; the run_ limits are the ones a " +
				"saved script meets on this deployment. A tool a run calls keeps its own ceiling as well: " +
				"a trino_export or api_export inside a run is bounded by that tool's timeout. " +
				"A script error is deterministic, so it is never retried. A rate-limit refusal of a " +
				"call is not a script error: the host waits the refusal's interval within the run's " +
				"deadline and issues the call again, and the wait is written to the run's log.",
		},
		"examples":        names,
		"read_an_example": "Call get with name=" + examples[0].name + " to read one.",
		"see_also":        KnowledgePages,
		"read_a_page":     "Call fetch with the reference to read one in full.",
	})
}
