# Running Managed Scripts

A managed script is authored interactively and executed unattended. This page
covers the second half: what runs a script, what a run produces, and how long
the record of it is kept. The authoring half —
writing, validating, and dry-running a script — is reached through
`manage_script`, and the security model behind everything here is
[Managed Scripts: Security Model](security.md).

## A saved script runs

The platform executes the script's latest saved version: saving a version makes
it the version that runs. `run_script`, the portal's run action, and a schedule
all execute it, and the run gate refuses only a script taken out of service —
disabled, deprecated, or superseded — in its own words on every surface
(`pkg/script/run.go`, `RefuseRun`). There is no approval step and no state in
which a script exists but nothing may execute it; `manage_script run_draft`
remains the way to execute an edit as yourself before saving it.

### The authority a run carries

A run executes as the script's own principal (`script:<name>`), presenting the
roles its author held when they saved the executing version. Those roles are
captured on the immutable version row at the save and cannot be set any other
way, so a script can never do unattended what the person who wrote it could not
do themselves. The middleware resolves them to a persona at every call, exactly
as it does for a person, and the persona filter decides which connections the
run may reach — at run time, with no script-side allowlist. Narrowing a
persona's rules takes effect on the script's next run.

Who may save is the edit rule: a script is one person's, so its owner saves it
and so does an administrator. Deleting it is the same rule, and it takes the
script's schedule and history with it — nobody else could see it, run it, or
notice it go.

A delete is made from the script's page in the portal
(`DELETE /api/v1/portal/scripts/{id}`) or with `manage_script command=delete`.
Both call the same store, so the two surfaces remove exactly the same rows: the
script, its saved versions, its schedule, its run history and the state it
carried. Both also answer with the same account of the removal, composed once
in `script.DeleteMessage` and naming only what the script actually had, so a
script that was never scheduled and carried no state is not reported as having
lost either:

```json
{
  "name": "daily-sales-report",
  "status": "deleted",
  "message": "daily-sales-report is gone, with its saved versions, its schedule, its run history and the state it carried. The assets and resources it wrote remain, and they still record that it wrote them."
}
```

An agent deleting a script on a person's behalf relays that sentence rather than
inventing one. What a delete does NOT remove is the work: the portal assets and
managed resources the script wrote stay where they are, owned by whoever owns
them, and the producer records naming the script as their writer stay with them
(see [what wrote a file](../server/content-producers.md)) — a deleted script is
still the answer to "what wrote this report". A delete from the portal is
recorded in the audit log as a `script_delete` event of kind `admin`, naming
the script and the owner who lost it; one made through the tool is already in
the log as the `manage_script` call it was.

An administrator can move a script to another owner, from the script's page in
the portal (`PUT /api/v1/portal/scripts/{id}/owner`), choosing the new owner
from the people who have signed in to this deployment at least once: an address
nobody has ever authenticated with cannot open the portal, so a script handed to
one would be visible to administrators alone. Ownership is the whole of
what a script is, so the transfer hands over what its owner sees, edits, runs,
and schedules, all at once — including its history: the new owner reads the run
records and dry-run accounts the previous owner produced, whose logs are free
text those runs printed. That is the reason a transfer is an administrator's
action and not an owner's to give away.

The move is recorded as a new version authored by the administrator making it,
and from then on a run presents THAT administrator's roles: moving a script to
an administrator is how it comes to run with an administrator's reach. It is
refused when the receiving owner already keeps a script of the same name, and
it is recorded in the audit log like any other administrative write.

The files the script's runs have already written do not move on their own
(#1588). An output records the owner's address when it is first written, and
the move rewrites nothing about it by itself, so a transfer that said nothing
about them would leave the new owner with a script whose every run refreshes
files they cannot open. When the script has created live assets or
collections, the request states what happens to them:

```json
{"owner_email": "jane@example.com", "outputs": "move"}
```

`outputs: move` hands every asset and collection the script created to the
new owner, in the same transaction as the script, so a refused transfer moves
no file and a failed file update moves no script. `outputs: keep` leaves them
with whoever owns them now. A request that says neither is refused with a 400
naming how many files there are to decide about; a script that has created
none accepts either value or neither and moves exactly as it always has. The
portal's confirmation counts the files and offers to move them, on by default.
Only files the script CREATED are its to move: a file it wrote a version over
is somebody else's, a managed resource is filed by library rather than by
address, and a deleted file is gone either way.

The response states what became of them. Moved, it counts what moved; kept,
it lists each file the new owner cannot open, share or delete, by name and by
whose it is, and the script's page marks the same files under Files written.
The audit event records the disposition and, for a move, how many rows it
touched.

### What a run may call

A script calls the tools its author can call. `platform.query`,
`platform.export` and `platform.publish_data` are named helpers for the three
things a report usually does; `platform.call(tool, args)` invokes any other
platform tool by name and hands the script its structured result:

```python
resp = platform.call("api_invoke_endpoint", {
    "connection": "util",
    "operation_id": "fetch_forecast",
    "body": {"office": "PSR"},
})
periods = resp["body"]["properties"]["periods"]

platform.call("manage_asset", {
    "action": "patch",
    "asset_id": "5affca99a698be1b31dd25d0f76cb398",
    "change_summary": "Hourly forecast refresh",
    "edits": [{
        "op": "replace_content",
        "selector": "#data",
        "text": json.encode({"as_of": run.fire_time, "periods": periods}),
    }],
})
```

A collection an API serves in pages is one call, not a loop. Pass `paginate`
to `api_export` (or `api_invoke_endpoint`) and the gateway walks the pages
inside that call, pacing on the upstream's `Retry-After`, streaming the merged
array into one asset, and returning the asset's metadata with `pages_fetched`
and `stopped_by`. A 160-page changelog is one `platform.call`, one rate-limit
token, one audit row, and no page bodies held in the script:

```python
asset = platform.call("api_export", {
    "connection": "vendor",
    "operation_id": "listChangelog",
    "query_params": {"per_page": 100},
    "paginate": {"items": "data", "cursor_param": "cursor", "max_pages": 500},
    "name": "changelog.json",
})
print(asset["pages_fetched"], asset["stopped_by"])
```

See [Walking a paginated
operation](../server/api-gateway.md#walking-a-paginated-operation) for how the
next page is found and where the walk stops.

A script is executed top to bottom; there is no `main` and nothing calls one.

**A statement passed to `trino_execute` is not parameter-bound.** `platform.query`
takes `:name` placeholders and renders each value as a typed SQL literal;
`platform.call("trino_execute", …)` has no such argument, so there is no safe
way to put a value that came from outside into it. Do not build one by
concatenation or `%` formatting: one apostrophe in an upstream field breaks the
statement, and an upstream field can append statements of its own. Write out a
statement whose text your script controls, or land the data as an output and
load it with something that binds.

Every one of those, the helpers included, is one ordinary MCP tool call over
the run's own session. It is authorized by the persona filter at the moment it
is made, presenting the roles the version's author held at the save, so a
script reaches exactly what its author reaches and a tool the persona does not
allow is refused in the persona filter's own words. There is no script-side
allowlist, and nothing to keep in step with the persona configuration it would
duplicate. A deployment that does not want scheduled writes withholds
`trino_execute` from the persona, which is where that decision already lives
for interactive callers. See the security model's [tool-surface
section](security.md#the-tool-surface-is-the-personas-not-the-script-layers).

Name the tool with a string literal, and write the args dict out in the call:
`validate` reads the literal tool names into the report's `tools` list and a
connection named literally inside the args dict into its `connections` list,
which is how a reader learns what a script reaches without reading the
Starlark. What cannot be read is reported as `dynamic_tools` or
`dynamic_connections` rather than quietly left out.

Two things a generic call does not get, which is why the helpers are still the
way to do the three things they do. A query issued by tool call is not counted in the run's query
total, and a write made by tool call is not one of the run's OUTPUTS: the run row's output list and the per-run output cap cover
`platform.export` and `platform.publish_data`, so an object written by
`platform.call("s3_object", {"action": "put", ...})` appears in the audit log rather than on
the run detail page. And a query issued by tool call is handed the tool's own
result, truncation flag included, without the row cap `platform.query` pushes
down into the statement.

A tool that answers with plain text rather than a structured object arrives as
`{"text": "..."}`, so a call always returns something the script can read.

A run acts on what its author owns. It authenticates as `script:<name>`, which
is what audit records and what it stamps on the assets it writes, and it carries
the address of the version's author so ownership checks recognize it: a script
can refresh a dashboard its author owns, patch a document they own, or share
one. (What its outputs belong to is a separate question, answered in [Who the
output belongs to](#who-the-output-belongs-to): the script's owner, on the
address the write records beside the principal.)
The author, not the script's owner, because the run presents the author's roles
and the two halves have to be the same person — so after an administrator
transfers or edits a script, its runs act for that administrator while the owner
is who may trigger them. A share addressed to the author
is not inherited, and `list` still shows the script's own outputs rather than
its author's library.

Two calls are refused from inside a run: `run_script` and `manage_script
run_draft`. That is a runaway-work guard rather than an authorization rule — a
worker executes one run at a time per replica, so a script waiting on a run it
started would be waiting on the worker running it. Give the second script its
own schedule.

### Where output may go

A script writes to the portal by default. Delivering output to a bucket needs
the deployment to declare the destination, by name and complete address, in
configuration:

```yaml
scripts:
  destinations:
    - name: acme-drop
      connection: acme-s3
      bucket: acme-exports
      prefix: weekly
```

A run resolves the destination name a script writes against this list at run
time, so repointing a destination — changing its connection, bucket, or prefix
— is a configuration change that takes effect on the next run. A name nothing
declares is refused inside the interpreter, naming the configured set, and a
draft run resolves through the same list, so a destination a real run would
refuse fails while the author is iterating. The write itself is authorized by
the persona filter like every other call. See the security model's
[delivery section](security.md#delivery-leaving-the-platform).

## Running one

```json
{
  "name": "daily-sales",
  "args": { "day": "2026-08-12" },
  "wait_seconds": 120
}
```

`run_script` validates the arguments against the script's parameter contract —
the latest saved version's — puts a run on the queue, and waits for it. The
run executes the version it was queued against, so a save landing while a run
waits in the queue does not swap code underneath it; the next request executes
the new save.

The call waits up to two minutes by default and at most five. A run that
outlives its window keeps going: the response carries its `run_id` with a
`pending` status, and `manage_script command=get_run run_id=…` reads it when it
finishes. Passing a negative `wait_seconds` queues the run and returns
immediately.

Runs execute on the platform itself, on whichever replica claims them, so a
long report does not hold an agent's connection open and a restart does not lose
a queued run.

### Typed parameters, and the ones the platform can offer

A parameter is typed `string`, `int`, `float`, `bool`, `date`, `enum` or
`connection`. Every surface that asks for a value renders the control the type
deserves: a choice where the value comes from a set somebody already knows, and
a box only for a value the platform genuinely cannot enumerate.

`connection` is the type for a parameter naming a platform connection. It binds
as a string, and it is a type of its own because the platform holds the whole
set of values it can take:

- The surfaces that ask for one OFFER the set instead of asking an author to
  remember the spelling: the connections the caller's own persona reaches. The
  run itself is authorized at each query against the roles captured at the
  script's last save, so a connection outside them is refused at the query.
- The set holds only connections a script can query. A connection is
  identified by kind and name together, and a deployment may legitimately carry
  one name across kinds — a Trino cluster, a DataHub instance and a bucket all
  called `acme`. The value bound here is passed to `platform.query`, which
  reaches the Trino connection, so that is the connection the name resolves to
  and the one the surface describes. `platform.export` names a configured
  destination rather than a connection, so it does not widen the set.

An `enum` carries its own values and renders the same way. An optional
`connection`, like an optional `enum` or `date`, must declare a default: there
is no meaningful empty connection.

### Running one from the portal

The owner of a script runs it from the script's own page. The form is built
from the script's parameter contract, and pressing Run queues exactly what
`run_script` queues: `POST /api/v1/portal/scripts/{id}/runs`, restricted to the
script's owner and to administrators, binds the values against the latest saved
version, puts a run on the queue, and answers with its id. A worker executes it
under the script principal, so a run asked for here and a run asked for by an
agent are the same run in every respect except the label recording who asked.

The run appears in the history below the form and updates as it progresses: the
history re-reads itself while anything is pending or running and stops once
nothing is. The response carries no result — a run may take ten minutes, and
the history is where a run is followed.

![A script's own page: contract, run form, and run history](../images/screenshots/light/user-script-detail-light.webp#only-light)![A script's own page: contract, run form, and run history](../images/screenshots/dark/user-script-detail-dark.webp#only-dark)

Two things it cannot do:

- It cannot run what the platform would refuse. Whether a run would be admitted
  is `script.RefuseRun`'s answer, the same one the contract document reports
  and `run_script` obeys, so a disabled or retired script says so instead of
  offering a control that cannot work.
- It cannot widen anything. The run is authorized at every call against the
  roles captured at the script's last save.

A run's trigger records which of the three producers created it: `tool` for
`run_script`, `schedule` for a fire, and `portal` for one an owner asked for on
the page. They execute identically.

## Running one on a schedule

A schedule is what turns a script into an automation: a cadence, a timezone,
and the parameter values every fire binds. Every fire executes the latest saved
version.

![A script's schedule, paused](../images/screenshots/light/user-script-schedule-paused-light.webp#only-light)![A script's schedule, paused](../images/screenshots/dark/user-script-schedule-paused-dark.webp#only-dark)

The portal shows the cadence, the timezone, the next fire, and whether the
schedule is running or paused, along with how many fires a pause has missed
and the note that a gap is never caught up on.

```json
{
  "command": "schedule_set",
  "name": "daily-sales",
  "cron": "0 7 * * 1-5",
  "timezone": "America/Los_Angeles",
  "args": { "report_date": "${fire_date}" }
}
```

That reads "07:00 on weekdays, Los Angeles time, reporting on the day it fires".
The cadence is a standard five-field cron expression or a descriptor (`@daily`,
`@hourly`, `@every 30m`), read in the timezone named beside it — so a report
stays on its wall clock across a daylight-saving change instead of drifting by
an hour. The floor is one fire a minute.

A script has at most one schedule. Setting one again replaces the cadence in
place, keeping the same automation and the run history that points at it; a
second cadence over the same code is a second script.

| Command | Does |
|---|---|
| `manage_script command=schedule_set name=… cron=… timezone=… args=…` | Create or replace the cadence |
| `manage_script command=schedule_list` | The schedules of the scripts you can see |
| `manage_script command=schedule_disable name=…` | Stop it firing |
| `manage_script command=schedule_enable name=…` | Start it again |

The admin API carries the same four actions: `GET
/api/v1/admin/scripts/schedules`, `GET` and `PUT
/api/v1/admin/scripts/{id}/schedule`, and `POST
/api/v1/admin/scripts/{id}/schedule/enable` and `.../disable`. Pausing is its
own action rather than a field of the cadence, because re-sending a schedule to
turn it off would re-base the fire it resumes on.

There is deliberately no way to delete a schedule. Disabling one stops it and
leaves the row that explains the runs it produced.

### Editing from the portal

The owner of a script edits its source on the script's page, in the portal's
own editor, and the edit crosses the same gate a `manage_script update` crosses
(`script.ApplyEdit`): it lands on the live row, is captured as a version, and
is the version that runs from then on. The save says so, and says instead when
the script is disabled or retired and nothing will execute it. The route is
`PUT /api/v1/portal/scripts/{id}/source`, restricted to the script's owner and
to administrators, and it edits the SOURCE only — the status and the parameter
contract are structured decisions the tool owns, and the owner is the
administrator's transfer.

The source is parsed before anything is stored, so code that cannot run is
refused at the keyboard rather than at the next fire.

![Editing a script's source in the portal](../images/screenshots/light/user-script-source-light.webp#only-light)![Editing a script's source in the portal](../images/screenshots/dark/user-script-source-dark.webp#only-dark)

### Documenting a script

A managed script is complex logic that outlives the conversation that produced
it, so its description is a document rather than a caption: markdown, at
whatever length the automation needs explaining at, rendered on the script's
page the way an asset's description and a knowledge page are rendered elsewhere
in the portal. Write what the script produces, what each parameter means in the
reader's terms, what it assumes about the data, and anything somebody re-reading
it in six months would need.

Four fields say what a script is, and the owner writes all four on the page that
shows them (`PUT /api/v1/portal/scripts/{id}/metadata`), or an agent writes them
with `manage_script update`:

| Field | What it is |
|---|---|
| `display_name` | The label every listing, page header and search result prints. At most 200 characters. |
| `description` | The document, in markdown. |
| `category` | One lowercase slug the script is filed under (`^[a-z][a-z0-9-]{0,30}$`), which the listings filter on. |
| `tags` | Free-form labels, up to 20. |

All four apply immediately and are captured as a version, because what a
script claimed to do is part of explaining what one of its runs did.

The portal form sends all four fields on every save, so clearing a box clears
the field. Through the tool, a field left out of an `update` is left alone: send
`category: ""` or `tags: []` to unset those, and note that `display_name` and
`description` follow the tool's older convention in which an empty string means
"not sent" and cannot clear the field.

All four are matched by search — `script_fts` is composed from the title, the
description, the category, the tags and the parameter contract, and the semantic
index embeds the same text — so how a script is described and filed decides
whether anybody finds it.

**Length.** A description is refused only above 64 KiB, which is a structural
limit rather than an editorial one: the full-text expression is built into a
GIN index, so PostgreSQL runs `to_tsvector` on every write and refuses an input
over 1 MiB, which would make the row unwritable rather than merely unfindable.
Well below that, at about 16 KiB, the write still succeeds and the response
carries a suggestion that the background might belong in a knowledge page the
description links to. It is advice, never a refusal.

**Filing.** A category is one value written one way, which is what makes it a
filter: reuse an existing category rather than coining a near-duplicate, and let
tags carry everything a single axis cannot. `manage_script command=list` accepts
both (`category`, `tags`) and the portal listing offers a chip per value beside a
search box over the name, display name and description, all three of which
narrow the listing on the server rather than in the page.

### Checking an edit before saving it

The editor offers the same two checks `manage_script` offers an agent, on the
page the author is already looking at — worth doing before every save, since
the save is the version that runs.

**Validate** (`POST /api/v1/portal/scripts/{id}/validate`) parses the edited
source and reports the capabilities, connections and destinations it would
reach, with a correction for every finding. It executes nothing and stores
nothing. Where a list is known to be incomplete — a `platform.query` call that
computes its connection rather than naming one — the report says so, because a
list that silently omitted a computed name would be a false statement.

![A draft run of an unsaved edit](../images/screenshots/light/user-script-dry-run-light.webp#only-light)![A draft run of an unsaved edit](../images/screenshots/dark/user-script-dry-run-dark.webp#only-dark)

Validate also checks each destination the source names literally against the
set this deployment declares, and reports the ones it cannot serve in the
words the run itself would use. Destinations are resolved by name at run time,
so the declared set changes underneath a stored script with no script edit;
validate is where an operator finds the affected scripts without running each
one. A destination the source computes is not readable from the source and is
reported by `dynamic_destinations` instead. A save is deliberately not checked
this way: refusing it would take away the edit that fixes the script.

**Dry run** (`POST /api/v1/portal/scripts/{id}/dry-run`) executes the edit
under the author's own identity and persona, with the draft limits, persisting
nothing: `platform.export` reports the shape and size of each output instead of
writing it, no asset is versioned, and no object is delivered. It is the same
execution `manage_script command=run_draft` performs, through one
implementation (`internal/platform/scriptdraft`), so neither surface can drift
from the other.

Both surfaces execute the source sent with the call, which is the whole point:
a save is immediately the version `run_script` executes and a schedule fires,
so a dry run is the only way to try a change without making it live. Sending no
source runs the saved version, which is how a script nobody has edited is
dry-run. The static read runs first either way, so a source that cannot parse,
one carrying an inline credential, or one naming an undeclared destination is
refused before the interpreter starts.

Neither introduces authority. A dry run is the caller's own session: it is
authenticated, authorized, rate limited and audited exactly as the same calls
typed by that person directly would be, and there is nothing reachable through
it that its caller could not already reach. Values bind against the live
record's contract, which is what the code beside them was written against.

A replica runs a small fixed number of drafts at once, because an interpreter's
heap cannot be capped and how many are running is what bounds it. A request that
cannot get a slot within a few seconds is answered as busy — a `503` saying to
try again — rather than held open.

**The account kept.** Each dry run is recorded as an account of itself: who
ran it, when, how it ended, what it printed, and the shape of the outputs it
would have written. The account is keyed by the SOURCE that executed, so it
attaches to whichever version later carries that exact code — which is the
order authors work in, since an edit is dry-run before it is saved. A version
with no account is code that first executes unattended, and the version detail
states that plainly.

An author's accounts are trimmed to the most recent handful per script, so the
table is bounded by the authoring loop rather than by how many times somebody
pressed the button.

### Who sets a cadence

The owner of a script sets its cadence, and so does an administrator: it is the
same rule reading and editing answer to. A cadence carries no authority of its
own — the run gate and the persona filter are re-read at every fire — so
re-timing a script reaches nothing the script could not already reach.

The portal asks for a cadence in the terms a person has it in — hourly, daily,
weekdays, chosen days, a day of the month, a time, a zone — and derives the cron
expression from that, showing it rather than asking for it. An expression the
builder cannot express is still settable, through its Custom field, and an
expression an agent wrote through `manage_script` opens there as itself rather
than being rewritten into something near it.

The same three actions are on the portal, on the pages the owner already reads
their script on: `GET` and `PUT /api/v1/portal/scripts/{id}/schedule`, and `POST
/api/v1/portal/scripts/{id}/schedule/enable` and `.../disable`, restricted to
the script's owner and to administrators and answering "not yours" exactly as
they answer "no such script". See
[Scripts in the portal](../portal/scripts.md#the-schedule).

A cadence on a disabled or retired script saves and stays inert — which both
the tool and the page say plainly rather than leaving an owner waiting on an
automation that was never going to run.

Every run the platform executes is also measured: `script_runs_total`,
`script_run_duration_seconds`, `script_runs_running`, and
`script_missed_fires_total`, labeled by script and trigger. They are what the
admin portal's Runs tab draws, and they answer what the run table cannot — a
missed fire is a run that does not exist. See
[Observability](../server/observability.md).

### `${fire_date}`, and why the date is not computed in the script

A schedule's bound values may contain one token, `${fire_date}`, which expands
at the moment the fire is materialized to the date of that fire in the
schedule's own timezone. The expanded value is what the run row stores.

That is the whole reason the token exists. A script that computed today's date
itself would produce a different answer every time it ran, and a run nobody can
reproduce is not a governed run. With the date pinned onto the run, re-running
it later with the same parameters asks the same question. Anything else a date
needs — the previous day, a month boundary — is arithmetic the script does on
this value through the date module, where a reader can see it.

The bound values are checked against the script's parameter contract when the
schedule is set, not at the first fire: a schedule that could never bind is
refused while somebody is still looking at it.

### Overlap, misfires, and what a schedule guarantees

**One fire, one run, however many replicas.** Every worker replica materializes
due schedules, with no leader and no election. Several notice the same fire at
the same moment, they all try to write the run, and a unique index on
(schedule, fire time) means exactly one of those writes survives.

**Overlap policy: skip if the previous run is still going.** A fire arriving
while the schedule's previous run is still pending or running does not queue
behind it. It is recorded as a `skipped_overlap` run — a terminal row nothing
ever claims — so the skip appears in the run history rather than as silence.

**Misfire policy: fire once, for the latest.** After a gap the platform was not
materializing through — a stopped worker deployment, a restored database — one
run materializes, for the most recent fire that has come due, and the fires
before it are counted on the schedule's `missed_fires`. A catch-up burst the
moment the platform recovers is worse than a visible gap: each of those runs
would compute a date nobody is waiting on any more, all at once, against the
warehouse.

What that gap costs depends on how the script finds its window. A job that
computes its window from `run.fire_time` (the previous day, the previous hour)
leaves the missed fires uncovered, and a backfill is a `run_script` call per
missing window with the parameters that name it. A job written against its own
[state](#state-what-a-run-carries-to-the-next) needs none of that: it reads
`run.state["synced_through"]`, pulls from there to the fire time, and saves the
new mark, so the fire after downtime covers everything the missed fires would
have. Backfill still exists for a job that wants a specific window.

**A failed scheduled run is mailed to the person accountable for it** — the
script's owner — carrying the run id, the failure, and the tail of what the
script printed. Failures of runs
requested through `run_script` are not mailed: that failure is already in the
response its caller is reading. Like every other notification, it needs the
email substrate configured, and a recipient can turn their own mail off.

A schedule that is paused resumes on the fire it was parked on, which the
misfire policy then collapses to one run — a pause is downtime, and gets the
same treatment. While it is paused it reports no next run: the stored due time
is what it will resume on, not a fire anything is going to produce.

## Where runs execute

Every replica runs the queue worker by default, so the single-binary deployment
executes what it enqueues and needs no configuration at all. One switch changes
that:

```yaml
scripts:
  worker:
    # Claim and execute queued runs on this replica. Defaults to true.
    enabled: false
```

A replica with the worker off still serves MCP and portal traffic, still
registers `run_script`, still validates and enqueues a run, and still waits for
the result. It simply never claims: the run is executed by a separate deployment
of the same binary with the worker on, reading the same queue, and the waiting
call picks the result up on its next poll of the run row.

The same switch decides where schedules are materialized. A worker-off replica
serves the schedule commands and the admin routes — setting a cadence is a write
to a table — but never turns a due schedule into a run, because a replica that
will not claim gains nothing by producing rows for one that will. A deployment
whose workers are all off therefore stores schedules that nothing fires, which
is the same shape as a deployment whose workers are all off storing runs that
nothing executes.

Splitting them is worth doing when script execution starts to matter. The
interpreter has no hard memory cap (see the [security model](security.md)), so
a pathological script pushes on the memory of whatever pod runs it;
keeping that pod out of the serving path means the worst case is a restarted
worker rather than a degraded agent session. It also lets the two scale on their
own axes — serving on connections, execution on queue depth — since a replica
executes one run at a time and concurrency comes from replica count.

The two deployments are the same image and the same configuration bar that one
key; the [deployment guide](../server/deployment.md#split-deployment-portal-and-script-workers)
has the manifests.

A worker shutting down stops claiming at once, gives the run it is holding a
short window out of the shutdown budget to finish, and releases whatever does
not finish back onto the queue rather than failing it — a shutdown decides
nothing about a run. A released run is claimable immediately, so a rolling
deploy costs a run at most the time it had already spent, not a wait for its
lease to expire.

## What a run produces

`platform.export` writes an output to the destination it names, resolved
against the deployment's configuration at run time.

By default that destination is the **portal**, and output identity there is
stable: the pair of (script, output name) maps to **one** asset, and each run
writes a new version of it. A daily report therefore keeps its identity, its
shares, and its history instead of producing a new asset every morning, and a
year of runs leaves one asset with a year of versions.

### Who the output belongs to

The asset belongs to the person who owns the script. It sits in their Assets
page and in their `manage_asset action=list` beside the ones they saved
themselves, `search` finds it, `fetch` resolves its `mcp:asset:` reference, and
they open, rename, retag, share, register a table over and delete it with no
administrator. A second person reaches it only through a share, as with any
other asset of theirs.

The row itself records two identifiers, and both are the truth about it. Its
`owner_id` is the run's principal (`script:<name>`) — that is what keeps one
asset per (script, output) and what the stored object key is built from — and
its `owner_email` is the script owner's address at the moment the row was
inserted. A PERSON is judged on either, which is why the principal on the row is
not a fact anybody has to work around; a person's own saved asset carries their
address in the same column, so the same rule answers for both.

A RUN is judged on the address alone, because `script:<name>` names a script
only within its owner and two people who each keep a `daily-sales` present the
same principal: judged on it, a run of one person's script would own the outputs
of the other's (#1579). Nothing is lost by dropping it, since a run's own writes
record the address beside the principal.

What a run ENUMERATES is neither identifier. `manage_asset action=list`,
`search` over assets and the collection listing scope a run by the PRODUCER the
platform recorded for its writes (`content_producers`), which names the script
by id: unique, unaffected by a rename, and unaffected by a transfer. The owner
columns are none of those. A transfer rewrites the address on the assets and
collections the script created only when it is asked to (`outputs: move`,
above); asked to keep them, it leaves those rows with the previous owner's
address, and they stay that person's while the new owner's runs go on writing
new versions into them, since an output keeps its identity across a transfer.
Either way the inventory is unchanged: what the script produced is recorded by
the producer relation, not by the owner columns.

A table registered over an output, or over a managed resource the script
refreshes with `manage_resource replace_content`, follows the file unless it
was registered with `follow=false`: the version the run writes moves the table
onto it before the write returns, so a query against the table reads the new
contents from then on with no second call. What the write did to each table is
reported on the export record (`tables`) and on the tool result, and printed
into the run log as `tables: <output>: <sentence>`, so a run that left a pinned
table behind says so in its history. See
[Registered Tables](../server/registered-tables.md#following-the-file).

### Tables and documents

`rows` carries the output's content in one of two shapes, and the declared
format decides which are valid:

- **A list of dicts**, serialized in the declared format. `csv` and `json`
  accept only this shape, so a data feed another system parses stays well-formed
  by construction.
- **A string body, written verbatim**, so a script can compose a document: an
  HTML or JSX dashboard, a prose report, a hand-assembled markdown page. `html`
  and `jsx` accept only this shape — they have no tabular serialization — and
  `markdown` and `text` accept either. The body lands byte for byte, under the
  content type the portal already stores and renders for that kind of saved
  asset, so a script-published dashboard is patchable and shareable like any
  other document asset.

```python
platform.export(
    name="revenue-dashboard",
    rows="<html><body><h1>Revenue</h1>...</body></html>",
    format="html",
)
```

A document keeps everything a table gets: the same name-to-asset identity and
versioning, the same destinations, the same draft-run preview (the body is
measured, nothing is persisted), the same size ceiling, and the same provenance
capture. An empty body is refused rather than published, so a conditionally
assembled document that ends up blank fails the run loudly instead of silently
replacing the current version of a shared dashboard. Object keys carry the
extension the platform assigns the document's content type — `.md`, `.txt`,
`.html`, and `.html` for `jsx` too, the platform-wide key spelling for
`text/jsx` objects.

### Refreshing a dashboard's data region

A semi-dynamic dashboard is a presentation that stays put while its data tracks
a schedule: one portal asset at one URL, authored once as an HTML, JSX, or
markdown document with real visualizations, whose numbers a script refreshes on
a cadence.

A script produces a document in one of two shapes, and the choice is made
before the first line of it is written. Compose the whole document in the
script when each run is its own kept document (a dated archive series), when
the structure varies with the data (a section that appears only if a threshold
trips), or when nobody will hand-edit the presentation; the cost is that every
fire overwrites the current version wholesale, so a layout edit made in the
portal is destroyed by the next scheduled run, and changing a chart color or a
heading means editing the script. Publish the document once and refresh only
its data region when there is one stable-named asset at one URL whose layout a
person may edit and whose numbers alone move per run; the cost is that the
structure is fixed by the document's author, so a report whose sections have to
appear and disappear with the data leaves a data region the markup cannot
render.

The second shape is the semi-dynamic dashboard: the template stays in the
asset, where a layout change is an ordinary document edit that survives the
schedule, and the data stays in the script. `platform.publish_data` is that
split:

```python
data = {"regions": platform.query(connection="warehouse", sql="SELECT ...")["rows"]}
platform.publish_data("revenue-dashboard", data)
```

- `name` resolves through the same output identity `platform.export` uses: one
  (script, output name) pair is one asset. The asset must already exist and be
  an `html`, `jsx`, or `markdown` document — this call refreshes a region of a
  presentation and can never create one. Publish the dashboard once with
  `platform.export(name, body, format="html")` (or `"jsx"`, or `"markdown"`),
  then let the schedule refresh only the numbers.
- The document marks its **data region**: exactly one element with `id="data"`,
  conventionally `<script type="application/json" id="data">...</script>`, whose
  text content is the JSON the dashboard's own code reads and renders. The
  platform serializes `data` (a dict or a list) as JSON and structurally
  replaces that element's interior — through the same anchored-editing engine
  `manage_asset` patch uses — leaving every other byte of the document exactly
  as its author wrote it. In a JSX document the payload is spliced as a
  template-literal expression child, so the module still compiles and the
  element's rendered text is still the JSON. A markdown document carries the
  island as a raw-HTML block, which is legal markdown; an `id="data"` occurrence
  quoted inside a fenced code block is example text, and a refresh that would
  land there is refused rather than spliced into the fence.
- The write is an ordinary new version of the asset, with the same provenance an
  export gets, so each version is a faithful as-of snapshot: a public share
  works with no auth or view-time fetch, and an old version still shows exactly
  the data it showed.
- A document without the marked region — or with more than one — fails the run
  with a message naming what is missing, rather than writing anywhere else. A
  document of the wrong kind (a CSV, a plain-text page) is refused the same way.
- The zero-rows case is yours, as with any export: publish the empty structure
  or `fail("why")`.
- In a draft run nothing is written; the call reports the payload size it would
  splice. Whether the target asset carries the region is checked by the run
  that writes, because the asset's content changes independently of the script.

The dashboard reads its own island at view time:

```html
<script type="application/json" id="data">{"regions": []}</script>
<script>
  const data = JSON.parse(document.getElementById("data").textContent);
  // render from data
</script>
```

### Delivering to an external system

Some output exists to be consumed elsewhere — the weekly CSV another system
picks up. A **bucket destination** the deployment declares in
`scripts.destinations` receives the same bytes instead:

```python
rows = platform.query(connection="warehouse", sql="SELECT ...")["rows"]

# The dashboard's asset, refreshed: a new version of one asset.
platform.export(name="weekly-sales", rows=rows, format="csv")

# The same result, delivered for another system to read.
platform.export(
    name="weekly-sales",
    rows=rows,
    format="csv",
    destination="acme-drop",
    key="2026/08/sales.csv",
)
```

The script names a destination and nothing else. The connection, the bucket,
and the prefix come from the deployment's `scripts.destinations` declaration,
so a script supplies no endpoint, no credential, and no bucket of its own — see
the security model's
[delivery section](security.md#delivery-leaving-the-platform).

- `destination` defaults to `portal`. A destination the configuration does not
  declare is refused inside the interpreter, before anything is issued.
- `key` is the object key beneath the destination's configured prefix. It defaults
  to the output name plus the format's extension (`weekly-sales.csv`), and a key
  that could climb out of the prefix is refused rather than cleaned up. The
  portal takes no key: it stores its own objects, and the output name is the
  identity there.
- `destination` and `key` must be passed **by name**; only `name`, `rows`, and
  `format` may be positional. A destination passed by position would be
  invisible to the static read that reports where a script writes, and a report
  that is quietly wrong is worse than one that is refused.
- Two outputs may not land on one object. Distinct names can produce one key —
  and one key can simply be named twice — and the second write would replace the
  first in a bucket the platform cannot read back, so it fails instead.
- One output name may be written **once per destination** in a run, which is
  what lets the example above send one result to both places. A second write to
  the *same* destination fails rather than silently keeping one of the two.
- A run reclaimed after a worker died does not deliver twice: each output is
  recorded as it lands, and a reclaimed run skips what it already wrote.

Each delivery is recorded on the run — destination, bucket, key, and bytes — and
audited under the script's own principal like every other capability call.

Each run records what it did — status, timings, interpreter steps, the queries
it issued, the outputs it wrote, and the log the script printed — and that
record is readable through the tool:

| Command | Answers |
|---|---|
| `manage_script command=runs name=daily-sales` | What has this script done lately? |
| `manage_script command=get_run run_id=…` | What did this run do, and what did it print? |
| `manage_script command=state name=daily-sales` | What will the next run read as `run.state`, and who wrote it? |

## State: what a run carries to the next

A script has one JSON object of state, kept by the platform. A run reads it as
`run.state` and writes it with `platform.save_state(obj)`; nothing in the
platform knows or cares what the keys mean. A watermark is
`state["synced_through"]`, a cursor is `state["cursor"]`, a set of ids already
handled is a list, a count of consecutive empty pulls is a number. It is bounded
at 64 KiB because state is a cursor or a summary, not a dataset: a script that
wants to keep a table keeps a resource.

```python
since = run.state.get("synced_through", "1970-01-01T00:00:00Z")
until = run.fire_time
rows = platform.query(connection="primary", sql="""
    SELECT order_id, region, amount, updated_at
      FROM sales.orders
     WHERE updated_at > from_iso8601_timestamp(:since)
       AND updated_at <= from_iso8601_timestamp(:until)
""", params={"since": since, "until": until})["rows"]
if rows:
    platform.export(name="orders-delta-" + until, rows=rows, format="csv")
platform.save_state({"synced_through": until, "last_delta_rows": len(rows)})
```

`manage_script get name=example-incremental-sync` returns this as a worked
example.

**What a run reads.** `run.state` is the state as it stood when the run was
*created*, a frozen dict, `{}` for a script that has never saved any. It is
read once, at creation, with the state's revision, and both are recorded on
the run row beside `params` (`state_revision`, `state_read`). The state read
is an input of the run exactly as the parameters are, so the determinism
contract reads:

> same script version + same parameters + same state read + same underlying
> data => same output

**When the write lands.** `save_state` replaces the whole object, and calling
it twice stages the last value: the run's write is one write. It is applied
when the run **succeeds**, in the same transaction that marks it succeeded,
with a compare-and-set on the revision the run read. A failed run leaves the
state where it was, so a watermark never moves past work that did not happen.
On success the run row records the state as written and the revision that
produced (`state_written`, `state_revision_written`), so `get_run` explains
the run from its own row.

**Two runs, one revision.** The schedule overlap policy already keeps two fires
of one schedule from running at once. A `run_script` call during a scheduled
run, or a reclaimed run whose predecessor is still winding down, can both read
revision N. One of them writes N+1; the other fails at its write with a message
naming the run that wrote, and its outputs stand, since they were produced
from the state it read. The failure is what makes the interleaving visible
instead of silently losing one of the two writes. It holds across replicas
because it is a row predicate, not a lock.

**Resetting it.** A wrong watermark is otherwise stuck, and "clear it and let
the next run start over" is the recovery. The owner and an administrator read,
replace and clear the state with `manage_script command=state` and
`state_action` `get`, `set` (with `state`, the whole object) or `clear`, and
on the script's portal page under **State**. A reset moves the revision and is
recorded with who did it, and a run in flight that read the old revision fails
at its write, which is correct: the reset was after its premise. Neither
`set` nor `clear` is admitted from inside a run, whose one way to write state
is `save_state` under the compare-and-set.

**What a draft does with it.** A draft run reads the live state and previews
the write: `run_draft` and the portal's dry run report the state the source
would have saved (`state`) beside the outputs it would have written, and
persist neither. The dry-run account keeps it, so a reviewer reads what the
code would have carried forward.

**What a reader learns.** `validate` reports `reads_state` and `saves_state`
from the source, and the contract every reference resolves to carries a `state`
block: whether the script reads or saves state, the revision, and when it last
changed. A script that never calls `save_state` has run rows with no state
written and a contract that says it keeps none.

State belongs to the script, not to a version of it: it survives a version
save, a disable and an ownership transfer, and it is deleted with the script.

## Failures

A script failure is **never retried**. The same version, on the same inputs,
fails the same way, so retrying multiplies the cost and changes nothing; the
run is marked failed and carries the Starlark backtrace. The fix is to correct
the script, dry-run it, and save the correction.

The rate limit is not a script failure either. Every call a script makes
crosses the [tool-call rate limiter](../server/configuration.md#tool-call-rate-limiting),
and a loop that issues calls faster than the limiter admits them runs into it.
What happens then depends on who the run is.

A platform run calls as the script principal, and the limiter queues it rather
than refusing it: a call over the rate is held until the sustained rate refills
a token, then admitted. The script sees the admitted call's result and nothing
else, the run's log carries no line for it, and the sustained rate governs the
run's throughput, which is why a run's duration can exceed what its calls
account for. Each held call counts in `mcp_rate_limit_queued_total` and is
logged under the run id, so an operator can see that a deployment's scripts are
running against the limit and raise it. A run canceled or reaching its
deadline while a call is held ends then, not at the next refill.

A draft run calls as its author and shares their bucket, so a loop past the
burst is refused with `rate_limited` as the author's own calls would be. The
script has no clock and no way to catch an error, both by design; the host has
a clock. When a call is refused with that code, the host waits the interval the
refusal's `retry_after_seconds` names, bounded by the run's deadline, and issues
the same call again. The script sees the result of the admitted call and
nothing else, and the interpreter's step count does not advance while the host
waits, so a paced run consumes the steps an unlimited one would. Each wait is
written to the run's log as a line naming the tool and the seconds waited. A
run whose deadline arrives while it is waiting fails as a timeout, exactly as
one whose query took too long, and is not re-queued. No other refusal is
retried.

Platform faults are different: a run whose session could not be opened, or whose
script could not be read, goes back on the queue with an exponential backoff and
a small attempt budget. The boundary is deliberately drawn by *where* the
failure happened rather than by reading error text — see the security model's
note on retry classification.

A worker that dies mid-run does not strand it. Each claim carries a lease; when
the lease expires the run becomes claimable again, and the worker that lost it
can no longer write to it. An output the lost run had already written is not
written twice: the run records each output as it lands, and a reclaimed run
skips what it already produced.

## Seeing what happened

The portal's **Scripts** page is the human view of all of this: every script you
can see, its owner where that is not you, its cadence and next fire, and how its
last run went.

![Scripts: every script you can see](../images/screenshots/light/user-scripts-light.webp#only-light)![Scripts: every script you can see](../images/screenshots/dark/user-scripts-dark.webp#only-dark) Opening one shows its contract, and — for a script you own — its version
history with each version's author and the roles a run of it presents, and its
run history with each run's trigger, duration, outputs, and the log it printed.
See the [portal guide](../portal/scripts.md).

A run is the owner's and the administrator's to read, plus whoever requested
that particular run. Asking an agent to show you your scripts opens the same
pages through the presentation-only `show_scripts` tool; every script operation
an agent performs for its own work uses `manage_script`, which renders nothing.

![A script's run history](../images/screenshots/light/user-script-runs-light.webp#only-light)![A script's run history](../images/screenshots/dark/user-script-runs-dark.webp#only-dark)

![One run's log](../images/screenshots/light/user-script-run-log-light.webp#only-light)![One run's log](../images/screenshots/dark/user-script-run-log-dark.webp#only-dark)

## Run history retention

Run rows are history as much as queue bookkeeping — a scheduled report's run
history is its refresh history — so they are kept far longer than a delivery
queue's rows and the retention is configurable:

```yaml
scripts:
  # How long a finished run is kept. Defaults to 365 days; a run still pending
  # or running is never swept, however old it is.
  run_retention_days: 365
```

The sweep deletes only terminal rows (`succeeded`, `failed`, `skipped_overlap`)
whose `finished_at` is older than the window, and it runs at most hourly, on the
replicas that run a worker (`internal/platform/scriptstore/runs.go`,
`PurgeRuns`).

**What is kept is not what a page shows.** The store caps any run listing at 50
rows (`defaultRunListLimit`), and each surface asks for what it can display and
states the cap when the answer fills it:

| Surface | Shows |
|---|---|
| A script's run history in the portal | The 25 most recent runs of that script |
| The portal Runs tab | The 50 most recent runs across the scripts the caller owns, or across every script for an administrator; `script_id` narrows it to one script |
| The admin Runs tab | The 50 most recent runs across every script |
| `manage_script runs` | The limit the call names, 20 when it names none, clamped to 50 |
| A script's contract (`fetch`, a prompt reference, the detail page) | One run: the last SUCCESSFUL one |
| The script listing | One run per script: the most recent, whatever its state |

Older history is still in the table until retention sweeps it, reachable by
narrowing the listing (by status, or one script at a time). The metric series
are the other way to read the same period — they are aggregates rather than
rows, so they answer rates and percentiles over the whole window and cannot
name a particular run.

## What a deployment needs

| Capability | Requirement |
|---|---|
| Authoring (`manage_script`) | A database |
| Reading and editing scripts in the portal | A database and the portal |
| Running (`run_script`) | The above; the tool is not registered where there is no run queue |
| Executing what was queued | At least one replica with `scripts.worker.enabled` left on, which is the default |
| Firing schedules | The same replicas; scheduling needs no configuration of its own |
| Mailing a failed scheduled run | The email substrate (`notifications`, and an admin-configured mail server) |
| Writing portal outputs | A configured portal asset store and object storage; without them an export to the portal fails the run, which is the honest report for a scheduled asset that never appeared |
| Delivering to a bucket | A destination declared in `scripts.destinations`, over an S3 connection the platform is configured with, not read-only, reachable by the persona the run's roles resolve to. It needs no portal: a run that only delivers writes nothing the platform keeps |
| Calling any other tool (`platform.call`) | Nothing of its own. The tool has to be registered on the deployment and allowed by the persona the run's roles resolve to, which is the same requirement an interactive caller has |
