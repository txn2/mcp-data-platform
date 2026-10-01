// Package scriptcontract is the dialect contract a managed script's author
// is handed: what Starlark offers, what the platform adds, and the rules a
// saved script is held to. manage_script command=help returns it and the
// built-in authoring knowledge page embeds it.
package scriptcontract

import "github.com/txn2/mcp-data-platform/pkg/script"

// Dialect is the help command's body: what a script is, what is
// predeclared, and what a Python instinct will reach for and not find.
// It is exported for the built-in knowledge pages (#1390): the authoring page
// derives its dialect section from this constant, so the two cannot drift.
const Dialect = `Managed scripts are written in Starlark: Python-shaped syntax, deliberately smaller.

THE SHAPE OF A SCRIPT, AND WHAT A SAVE CHECKS
  A script's work is in def main():, which the platform calls after the
  script loads. main takes no parameters; read them from run.params. The top
  level only declares: functions, constants written as literals (a string, a
  number, a list or dict of them), and docstrings. A statement that does work
  at the top level (a call, a loop, a read of run.params) is refused on save.
  Every save stores the source in one canonical format, so what get returns
  is what runs: keyword arguments as key = value, double-quoted strings, and
  the items of a multi-line list or dict one per line. validate returns that
  formatted_source; findings' line numbers refer to it.
  A save is refused while any of these holds, each finding naming its rule,
  its line and the fix:
    entry-point / top-level-work   main with parameters, or work at the top
                                   level (a source with no main() is a
                                   library; see LIBRARIES)
    library-effect                 a library that names platform or run
    cyclomatic-complexity          a function with more than 10 paths
    cognitive-complexity           a function scoring more than 15
    function-length                more than 40 statements in a function
    nesting-depth                  blocks nested more than 4 deep
    unused-variable / -parameter   a local or parameter nothing reads (name
                                   it with a leading _ when that is intended)
    shadowed-name                  a name that hides platform, json, xml,
                                   date, run, sum, fail, testing or assert
    missing-docstring              a function whose body does not open with a
                                   """one-sentence docstring""" in plain words;
                                   the flow diagram shows it on the box
    sql-built-from-values          SQL passed to platform.query,
                                   platform.execute, or a platform.call to
                                   trino_query or trino_execute, built with +,
                                   % or .format() from values: read with
                                   platform.query and write with
                                   platform.execute, using :name and params=
    call-in-loop                   platform.query, platform.execute,
                                   platform.call or
                                   platform.export once per element of a
                                   collection; a loop over range() that
                                   fetches one page per pass is not counted
    save-state-without-read        platform.save_state with no read of
                                   run.state
    save-state-before-fail         a WARNING, never a refusal: fail() is
                                   reached after platform.save_state, whose
                                   state a failed run discards; record
                                   progress with platform.checkpoint
    test-called                    the script calls one of its test_*
                                   functions, which only the test runner does
    test-module-outside-test       testing or assert used outside a test_*
                                   function; a run fails where it is used
    constant-assertion             an assertion comparing only values written
                                   into the test, which holds whatever the
                                   script does
  A script saved before these rules keeps running as it is, and its next
  version is held to every rule, like any other script's.

WHAT IS AVAILABLE
  platform.query(sql, connection=..., params={})  Run read-only SQL. Returns
      {"columns": [...], "rows": [...], "row_count": n}; rows are dicts keyed by
      column name, in the SELECT's column order, so rows exported as they
      came keep the query's columns where it put them. It is the read tool, so a statement that modifies state —
      INSERT, UPDATE, DELETE, CREATE, DROP — is refused by it; send it
      through platform.execute.
      Use :name placeholders and pass the values in params; the platform
      quotes them by type. Never build SQL by string concatenation.
      A table a register= made binds by its record: FROM :t with
      params={"t": out["table"]}, rendered as the quoted table name.
      A date binds as a quoted string, so compare it against a DATE column as
      "DATE :day", which renders the standard date literal DATE '2026-08-12'.
      A query whose result is truncated by the row cap FAILS rather than
      returning a partial answer: aggregate in SQL, or narrow the query.
      A SQL DECIMAL column arrives in the rows as a STRING, not a number, so
      pass it through float() before arithmetic:
      sum([float(r["total"]) for r in rows]).
  platform.execute(sql, connection=..., params={})  Run a statement that
      changes state -- INSERT, UPDATE, DELETE, CREATE, DROP -- through
      trino_execute, and return its answer as platform.call would. Values
      bind exactly as in platform.query: :name placeholders quoted by type, a
      list as IN :name, and a table a register= made by its record, so free
      text reaches a table as
        out = platform.export("tickets", rows, format = "jsonl",
                              destination = "resources", key = "tickets.jsonl",
                              register = {"connection": "scratch"})
        platform.execute("INSERT INTO lake.support.tickets SELECT * FROM :src",
                         connection = "scratch", params = {"src": out["table"]})
      It is a trino_execute call: authorized, audited and listed among a
      draft's writes as that call is, and a draft refuses it unless the draft
      was run with allow_writes.
  platform.export(name, rows, format="csv", destination="portal", key=None,
                  register=None, references=None, tags=None, metadata=None,
                  append=False)
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
      register by name, as destination and key are. Bind the record into
      platform.execute to INSERT ... SELECT from the registered table.
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
      append=True adds rows to an output the run builds across calls, so a
      paged API or a large query becomes one file and one registered table
      without holding every page: each page is serialized when it arrives,
      and the script keeps only what the next page needs. The first call to a
      name and destination starts the output and fixes its key, register=,
      tags= and metadata=; every later call passes append=True and adds rows.
      It takes csv or jsonl (a CSV keeps its first page's header, and a later
      page with a column the header lacks fails the call). The call returns
      the rows and bytes the output holds so far, with appending True; the
      output is written once, when the script finishes, and a run that fails
      part-way writes none of it.
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
      structured result. This is the same mechanism the helpers above
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
      Prefer the helpers where they apply; SQL goes through platform.query
      and platform.execute, which bind values, and a platform.call to
      trino_query or trino_execute with SQL built from values is refused on
      save. They are not a restriction
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
      An api_invoke_endpoint or api_export answer carrying
      upstream_retryable -- a 429, or a 503 to a GET or HEAD -- is issued
      again by the host after the interval the upstream named in
      retry_after_seconds (1s, 2s, then 4s when it named none), at most 3
      times and never past the run's deadline, each wait written to the run
      log. The script then has the upstream's last answer as data: its
      status (upstream_status, or status for api_invoke_endpoint), its
      headers, and for an api_export into a resource resource_unchanged, so
      a script can record a throttled day and carry on. An upstream that
      times out or cannot be reached fails the call, and the run is recorded
      with cause upstream and retryable true.
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
      would have saved; a draft that FAILED reports it as state_discarded,
      because a platform run that fails discards it.
  platform.checkpoint(state)  Record progress that is kept however the run
      ends. Same object, 64 KiB limit and revision check as save_state, but
      the platform commits the last checkpoint when the run FAILS or is
      halted at its deadline too. On a successful run a save_state replaces
      it, and a run that succeeds with no save_state commits it. Use it in an
      incremental job that makes durable progress partway through -- rows
      merged, files written -- so one late failure or the time limit does
      not throw away the watermark for the work that landed:
        for page in pages:
            merge(page)
            platform.checkpoint({"synced_through": page["end"]})
      get_run reports state_checkpoint: true when the state a run saved is
      its checkpoint, and a failing automation's notice carries the
      checkpoint it got through.
  platform.remaining_ms()  The milliseconds this run has before its
      deadline, the run_timeout it is halted at. The way to budget a long
      run: stop and checkpoint before the deadline instead of adding up
      duration_ms, which leaves out exports, registrations, retry waits and
      the interpreter's own time:
        if platform.remaining_ms() < 60000:
            platform.checkpoint({"synced_through": mark})
            return
      Each value is recorded with the run, so a test replays it exactly, and
      testing.set_run(remaining_ms=) sets it in a test.
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
  platform.notify(channel, title, body="", link="", data=None)  Post a message to a
      notification channel an administrator configured: a Mattermost channel,
      an incoming webhook, or a named email list. Call
      platform.call("notify", {"action": "list"}) to see which channels this
      script reaches; a channel is reachable when the run's persona reaches
      the api connection behind it, and an email channel is reachable by any
      script. body is markdown, shown as far as the destination can show it.
      link defaults to this run's own page, so a post leads back to the run
      that produced it. data is a dict or list a webhook channel whose format
      is json delivers verbatim beside the message, for a system that
      branches on fields rather than text (at most 64 KiB); every other
      channel ignores it. Delivery is queued: the call returns when the
      message is accepted, not when it appears.
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
  fail(msg, retryable=True)  fail as Starlark has it, plus retryable=: True
      records the failure as temporary (cause transient, retryable), for a
      condition outside the script that the next run may not meet, such as a
      feed that has not published yet.

WHAT IS NOT, AND WHAT TO WRITE INSTEAD
  import              There is no module system. json, xml and date are here.
  try / except        Errors fail the run by design, so the failure is recorded
                      rather than swallowed. Check first, or call fail("why").
                      A failure raised straight after an upstream answered
                      the last call with a 5xx or 429 is recorded as the
                      upstream's (cause upstream, retryable).
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

LIBRARIES
  A library is a script with no main(): pure code other scripts load, so the
  pagination, date windows and table shaping many scripts share are written
  once. Create it with manage_script create like any script; with no main()
  it is saved as a library. It is held to the same gates (format, lint,
  tests, coverage) and its tests call its functions directly:
      def last_week(day):
          """The seven days before day, as {"from": ..., "to": ...}."""
          ...
      def test_last_week():
          """A Monday's week."""
          assert.eq(last_week("2026-09-28")["from"], "2026-09-21")
  A library names neither platform nor run: it shapes data, and the script
  that loads it makes the calls, so validate on that script still reports
  everything it reaches. Pass a library function what it needs.
  A script loads one version of a library at its top level:
      load("lib:date-windows@2", "last_week", "month_of")
  The version is required. A saved version never changes, so a new version of
  the library changes no script until that script names it. A library may load
  another library the same way; a load of a library or version that does not
  exist, and a load cycle, are refused on save. validate reports library: true
  for a library and the libraries a script loads with their versions; a run,
  a draft and a test all run the version named.
  A library's name is unique across the platform, and anyone may load any
  library. A library is never run or scheduled itself, and it cannot be
  deleted while a script's current source loads it. A script and a library do
  not turn into each other: a script keeps its main(), and a library gets none.

WHAT DETERMINISTIC MEANS HERE
  Same script version + same parameters + same state read + same underlying
  data produce the same output. The warehouse still changes between runs, and
  that is the point of re-running: the promise is that the SCRIPT contributes
  no variation of its own. The state a run read is recorded on the run beside
  its parameters, so a run is explained from its own record.

THE LOOP
  run_draft -> write a test that replays the draft -> test -> create, then
  patch -> validate -> run_draft -> test -> save. validate
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

TESTS, AND WHAT A SAVE RUNS
  Every run_draft and every run records each host call it made and the answer
  it was given, with the parameters, state and fire time it started from.
  run_draft returns the recording's id as "recording". A script carries its
  tests beside main(), versioned with it: a top-level def whose name starts
  with test_, with a docstring, taking no parameters.
    def test_weekly():
        """Last week's run exports one row per region."""
        testing.replay("dpx_0123abcd")
        main()
        out = testing.outputs()
        assert.eq([r["region"] for r in out.exports[0].rows], ["east", "west"])
        assert.eq(out.state, {"through": "2026-09-20"})
  testing.replay("<recording>") names the recording, once, as a string
  literal; every host call the test makes is answered from it and nothing
  reaches an upstream. A call it holds no answer for fails the test naming
  the call, as the recording holds no answer for platform.query("select 1").
  testing.answer(tool, args, answer) declares the answer a call gets, alone
  or on top of a recording: a call to tool whose arguments include every key
  and value in args is answered with answer (a dict, as the tool answers), or
  with error="..." fails instead. Declared answers are matched in the order
  they were declared, before the recording, and each answers one call. That
  is how a test reaches a write no draft performed (a draft without
  allow_writes stops at the write, and its recording holds every call before
  it), a failure path, or a branch no recording takes:
    def main():
        """Files the day's extract and reports the resource it became."""
        rows = platform.query(connection = "warehouse", sql = "select region, n from daily")["rows"]
        content = "region,n\n" + "".join(["%s,%d\n" % (r["region"], r["n"]) for r in rows])
        made = platform.call("manage_resource", {"action": "create", "filename": "daily.csv",
                                                 "content_type": "text/csv", "content": content})
        platform.result({"resource": made["resource_id"], "rows": len(rows)})

    def test_files_the_extract():
        """The recorded draft's rows are filed as one resource."""
        testing.replay("dpx_0123abcd")
        testing.answer("manage_resource", {"action": "create", "filename": "daily.csv"}, {
            "resource_id": "r1", "reference": "mcp:resource:r1", "uri": "mcp://resources/r1/daily.csv",
            "filename": "daily.csv", "display_name": "daily.csv", "scope": "user", "path": "",
            "content_type": "text/csv", "size_bytes": 24, "message": "Created."})
        main()
        out = testing.outputs()
        assert.eq(out.calls[1].args["content"], "region,n\neast,1\nwest,2\n")
        assert.eq(out.result, {"resource": "r1", "rows": 2})
  A declared answer is held to what the tool always answers, where the tool
  declares that (manage_resource, manage_table, manage_asset, save_asset and
  trino_execute do): an answer lacking a field the tool always returns, a
  field of another type, or a nested key the tool never returns fails the
  test naming the field. Where the tool declares nothing the answer is not
  checked, and the test result says so in its notes. testing.set_run(state=,
  params=, remaining_ms=) sets what run.state, run.params and
  platform.remaining_ms() read for the rest of the test, which is how a test
  reaches a branch that runs only when saved state is present, or only when
  the run is out of time. A test naming no recording answers only what it declares, and
  its exports are previewed. testing.outputs() is what the execution produced so
  far, written nowhere: exports (each with name, format, destination, key,
  columns, rows, row_count, body), publishes (name, data), notifies (each
  notify call's arguments), calls (every tool call: tool, args, error, and
  declared, true when a declared answer answered it), state (what save_state
  staged, or None; None too when the execution failed under assert.fails,
  because a failed run's save_state is discarded), state_discarded (that
  discarded save_state, or None), checkpoint (the last platform.checkpoint,
  which is kept either way, or None), result (platform.result, or None) and
  log. A row reads as
  a dict (row["region"], row.get("n"), items, keys, values) but is its own
  type: compare rows with assert.eq, which compares their contents, not ==.
  assert.eq(got, want, msg=""), assert.ne(got, other, msg=""),
  assert.true(cond, msg=""), assert.contains(container, item, msg="") fail
  the test at their line saying what they got; assert.fails(fn, *args) calls
  fn and returns the message it failed with, failing the test if it did not.
  Neither module exists in a run, and a schedule never runs a test.
  command=test runs the tests of the source you send, or of the saved script,
  and reports each test's result, the failing assertion and its line, and
  coverage: the statements the tests reached, and the lines of the ones they
  did not (per statement: the branches inside one expression are not
  counted). command=recording with run_id reads a recording; recordings are
  read by the script's owner and administrators, and swept with runs at
  scripts.run_retention_days unless a test in the script's latest version
  names them.
  A save runs the tests. A script is saved only with at least one test, every
  test passing, and at least 80% of its statements reached. A test is
  refused when it makes no assertion, and when it still passes after the
  query rows it replays or declares are altered (the last row dropped, the
  first row's values changed): its assertions must read what the script made
  of the data; a test that the run fails, assert.fails(main), is not altered,
  nor one whose execution produced no output at all. The tests together must
  also read every output their executions produced: each column of each
  export's rows (reading row["region"], or handing a row or the rows to an
  assertion; reading .columns or .row_count does not read a column), the
  staged state, each notification, each published region and
  platform.result. One test may read what another produced. A save naming
  one no test reads is refused, as output "weekly" column "region" is never
  asserted on; command=test lists them under "unread". A script saved before
  tests were required runs as it is; its next version is saved with tests,
  like any other script's.
  A new version of a script that has run also replays the script's recent
  recorded runs through both versions and compares what they produced (rows,
  columns and their types, the state saved, notifications, platform.result,
  and any host call the recordings do not hold), and what each reaches
  (tools, connections, destinations, host bindings). A difference is a
  change in what the automation does: the save is refused, naming each
  difference, until it carries change_summary, what the automation will now
  do differently in plain words for the person it runs for, and
  user_agreed=true, once that person has agreed. Both are kept on the
  version. A refactor that changes no output saves without them. validate
  reports the tests and the differences without saving, so the change can be
  described before it is asked for.

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
  version's code with command=diff.

READING ANOTHER PERSON'S SCRIPT
  A script is how a resource or an asset was produced, so its definition is
  readable by everyone: name its owner with owner_email and get,
  get_content, outline, stats, locate, diff and versions answer as they do for
  your own. manage_script list names every script and its owner. Running it,
  scheduling it, changing it, and reading its runs and its state stay with its
  owner and administrators (a run grant lets someone else run it from the
  portal); a version's author roles are shown to the owner and administrators
  only.`
