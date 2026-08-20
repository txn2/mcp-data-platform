# Running Managed Scripts

A managed script is authored interactively and executed unattended. This page
covers the second half: how a version becomes executable, what runs it, what a
run produces, and how long the record of it is kept. The authoring half —
writing, validating, and dry-running a script — is reached through
`manage_script`, and the security model behind everything here is
[Managed Scripts: Security Model](security.md).

## Nothing runs until a version is approved

The platform executes exactly one version of a script, the one
`scripts.approved_version_id` points at, and only an approval writes that
pointer. Until there is one:

- `run_script` refuses, naming `manage_script run_draft` as the way to execute
  the draft as yourself while you are still writing it.
- Nothing else executes the script at all.

### Your own script is approved when you save it

A script at `personal` scope, written by the person who owns it, is approved on
save and runs immediately — on demand and on a schedule — under the access its
author holds. Nobody else can see it or invoke it, and the roles an approved run
presents were always copied from the version's author, so there was never an
authority for a reviewer to decide about.

The platform mints the capability grant itself, from what a static read of the
source plainly reaches: the capabilities it calls, the connections it names, and
the portal for output that names no destination. Four things send the version to
a reviewer instead, and the save says which:

- the source does not parse;
- a call computes its connection or its destination instead of naming one, so
  what it reaches cannot be read from the code;
- the author holds no roles, so an approved run would resolve to the deny-all
  persona and could call nothing;
- the script writes to a bucket destination no approval has pinned an address
  for, or reaches a connection the author's own persona cannot. The first
  delivery to a bucket is therefore always reviewed, and the owner's later edits
  are approved against the address that review pinned.

A personal script is also its owner's to delete: `manage_script delete` refuses a
script with an approved version in favour of deprecating it, because it may be
executing for somebody, and every personal script now carries one — so the
refusal applies only where the caller is not the script's owner.

Widening a personal script's scope takes the approval back: the execution
pointer is cleared and the version returns to the review queue, because a shared
script has an audience that agreed to nothing. An approval a person made
survives the change.

Every automatic approval is recorded as one — on the version, in the contract
document, in the version history, and on each run's audit event — so an operator
can tell which scripts nobody reviewed. What that accepts is written up in
[Managed Scripts: Security Model](security.md#a-personal-script-is-approved-for-its-owner).

### A shared script is reviewed

A `global` or `persona`-scoped script, and any script somebody other than its
owner edited, waits for an administrator.

Approving happens in the portal under **Admin, then Scripts**, which lists the
versions waiting for a decision and shows the reviewer what they are agreeing to
— the capability diff against what the script holds today, and the code diff
against what it executes today — before they agree to it. See the [admin portal
guide](../server/admin-portal.md#scripts-admin).

Underneath it is one REST call against the admin API, shown here because it is
also how a deployment scripts an approval. It binds two things at once — the
code and the capabilities that code may use — because approving them separately
would mean approving a script whose reach could change afterwards.

```bash
# What the reviewer reads: the version, what its source reaches for, and
# what the grant it already carries does not cover.
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  https://platform.example.com/api/v1/admin/scripts/$SCRIPT_ID/versions/3

# The approval: the connections, capabilities, and destinations this code is
# approved to use.
curl -s -X POST -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
        "connections":   ["warehouse"],
        "capabilities":  ["platform.query", "platform.export"],
        "destinations":  [
          {"name": "portal", "kind": "portal"},
          {"name": "acme-drop", "kind": "s3", "connection": "acme-s3",
           "bucket": "acme-exports", "prefix": "weekly"}
        ]
      }' \
  https://platform.example.com/api/v1/admin/scripts/$SCRIPT_ID/versions/3/approve
```

A destination carries the address, not only the name the script writes. The
portal is the platform's own asset store and takes none; every other kind names
the connection, bucket, and prefix its output lands under, and that address is
bound to the version — repointing it at a different bucket is a new grant, and a
new approval.

The request never names roles. The authority an approved run presents is the
set of roles the version's **author** held when they wrote it, copied from the
version by the approval itself, so approving can narrow what a script reaches
and can never hand it access its author did not have.

An approval is refused when the grant does not cover what the code plainly
calls: a script approved without the connection it queries would fail on its
first statement, and that is not a decision anyone intends to make. Changing a
grant later means approving again, which re-stamps the approval alongside the
new capability set.

Rejecting is the other decision, and it is confined to a pending draft
(`POST .../versions/{version}/reject`): it takes the proposal out of the queue
and changes nothing about what runs. The live version of a script that has never
been approved is also waiting for review, but declining it means leaving it
unapproved — which is already what it is.

Approving an earlier version is a rollback: it points the execution gate back at
that version and reapplies its snapshot to the live record, so the code being
served and the code being executed stay the same code. The version history in
the portal offers it directly.

An unworked queue is reported rather than left to be noticed. See [script review
queue alerts](../server/notifications.md#script-review-queue-alerts).

## Running one

```json
{
  "name": "daily-sales",
  "args": { "day": "2026-08-12" },
  "wait_seconds": 120
}
```

`run_script` validates the arguments against the approved version's parameter
contract, puts a run on the queue, and waits for it. Parameters are bound
against the **approved** version, not against the live record, so a pending
draft that renamed a parameter cannot change what a run accepts.

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
  remember the spelling. Which set depends on what will execute: a run of the
  approved version may name only the connections its approval granted, while a
  draft run reaches the connections its caller's own persona reaches.
- Either way the set holds only connections a script can query. A connection is
  identified by kind and name together, and a deployment may legitimately carry
  one name across kinds — a Trino cluster, a DataHub instance and a bucket all
  called `acme`. The value bound here is passed to `platform.query`, which
  reaches the Trino connection, so that is the connection the name resolves to
  and the one the surface describes. `platform.export` names a destination the
  approval pinned rather than a connection, so it does not widen the set.
- A value outside that set is refused where it was entered — on the run form, on
  the schedule form, and by `run_script` — rather than at the query it would
  have failed on, hours later, in a run nobody is watching.

An `enum` carries its own values and renders the same way. An optional
`connection`, like an optional `enum` or `date`, must declare a default: there
is no meaningful empty connection, and a run that bound one would be refused by
the grant.

### Running one from the portal

The owner of a script runs it from the script's own page. The form is built
from the approved version's parameter contract, and pressing Run queues exactly
what `run_script` queues: `POST /api/v1/portal/scripts/{id}/runs`, restricted to
the script's owner and to administrators, binds the values against the approved
version, puts a run on the queue, and answers with its id. A worker executes it
under the script principal, so a run asked for here and a run asked for by an
agent are the same run in every respect except the label recording who asked.

The run appears in the history below the form and updates as it progresses: the
history re-reads itself while anything is pending or running and stops once
nothing is. The response carries no result — an approved run may take ten
minutes, and the history is where a run is followed.

Three things it cannot do:

- It cannot run what the platform would refuse. Whether a run would be admitted
  is `script.RefuseNewRun`'s answer, the same one the contract document reports
  and `run_script` obeys, so a script with nothing approved says so instead of
  offering a control that cannot work.
- It cannot bind a connection the approval did not grant. A `connection`
  parameter's value is checked against the version's grant before the run is
  queued, so a name outside it is refused on the form rather than at the query
  it would have failed on.
- It cannot approve anything, widen a grant, or change what executes.

A run's trigger records which of the three producers created it: `tool` for
`run_script`, `schedule` for a fire, and `portal` for one an owner asked for on
the page. They execute identically.

## Running one on a schedule

A schedule is what turns an approved script into an automation: a cadence, a
timezone, and the parameter values every fire binds.

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

The owner of a script edits its source on the script's page, in the portal's own
editor, and the edit crosses the same gate a `manage_script update` crosses
(`script.ApplyEdit`): an edit to the owner's own personal script is applied and
approved, a shared script with an approved version keeps executing that version
while the edit becomes a draft in the review queue, and a script with nothing
approved applies directly. The save states which of the three happened. The route is `PUT
/api/v1/portal/scripts/{id}/source`, restricted to the script's owner and to
administrators, and it edits the SOURCE only — scope, personas, status, and the
parameter contract are structured decisions the tool owns, and the domain
refuses to mix a reviewable change with them anyway.

The source is parsed before anything is stored, so code that cannot run is
refused at the keyboard rather than at the next fire.

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

**None of the four is review-gated.** `script.RequiresReview` keys on the source
and the parameter contract alone, so documenting a script applies immediately
and the approved version keeps executing untouched. The change is still captured
as a version, because what a script claimed to do is part of explaining what one
of its runs did.

Being versioned has one consequence worth knowing while a draft is waiting for a
reviewer: approving a version applies that version's whole snapshot to the live
row, documentation included. So if you document a script after an edit to its
source became a draft, approving that draft restores the documentation the draft
captured. The portal form says so when a version is waiting, so the case is
visible where it happens; document it again after the approval, or document it
before making the source edit.

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
both (`category`, `tags`) and the portal listing offers a chip per value, which
narrows the listing on the server rather than in the page.

### Checking an edit before asking for approval

The editor offers the same two checks `manage_script` offers an agent, on the
page the author is already looking at.

**Validate** (`POST /api/v1/portal/scripts/{id}/validate`) parses the edited
source and reports the capabilities, connections and destinations it would
reach, with a correction for every finding. It executes nothing and stores
nothing. Where a list is known to be incomplete — a `platform.query` call that
computes its connection rather than naming one — the report says so, because a
list that silently omitted a computed name would be a false statement.

**Dry run** (`POST /api/v1/portal/scripts/{id}/dry-run`) executes the edit under
the author's own identity and persona, with the draft limits, persisting
nothing: `platform.export` reports the shape and size of each output instead of
writing it, no asset is versioned, no object is delivered, and the approved
version pointer is untouched. It is the same execution `manage_script
command=run_draft` performs, through one implementation
(`internal/platform/scriptdraft`), so neither surface can drift from the other.

Neither introduces authority. A dry run is the caller's own session: it is
authenticated, authorized, rate limited and audited exactly as the same calls
typed by that person directly would be, and there is nothing reachable through
it that its caller could not already reach. Values bind against the LIVE
record's contract rather than the approved version's, because a draft is
precisely the code that does not match the approved version yet.

A replica runs a small fixed number of drafts at once, because an interpreter's
heap cannot be capped and how many are running is what bounds it. A request that
cannot get a slot within a few seconds is answered as busy — a `503` saying to
try again — rather than held open.

**What the reviewer sees.** Each dry run is recorded as an account of itself:
who ran it, when, how it ended, what it printed, and the shape of the outputs it
would have written. The account is keyed by the SOURCE that executed, so it
attaches to whichever version later carries that exact code — which is the order
authors work in, since an edit is dry-run before it is saved. The review drawer
shows it beside the version, and its ABSENCE is stated plainly: approving a
version nobody has run means that code first executes unattended.

An author's accounts are trimmed to the most recent handful per script, so the
table is bounded by the authoring loop rather than by how many times somebody
pressed the button.

### Who sets a cadence

The owner of a script sets its cadence, at every scope, and so does an
administrator. That is a different rule from the one that governs editing: what
a script DOES is confined to a personal script unless an administrator changes
it, and a change goes back through review. When it runs carries no authority to
confine — the execution gate and the capability grant are re-read at every fire
— so the owner of a shared report re-times or pauses it without asking anyone.

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
they answer "no such script". They are the only routes on that surface that
change anything; approving a version stays on the admin API. See
[Scripts in the portal](../server/portal-user.md#the-cadence).

A cadence can be set before anything is approved. It saves, it binds against the
live record's parameter contract because that is the only contract there is yet,
and it fires nothing until a version is approved — which both the tool and the
page say plainly rather than leaving an owner waiting on an automation that was
never going to run.

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
this value through the date module, where a reviewer can see it.

The bound values are checked against the approved version's parameter contract
when the schedule is set, not at the first fire: a schedule that could never
bind is refused while somebody is still looking at it.

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
warehouse. A backfill somebody actually wants is a `run_script` call with the
parameters they want.

**A failed scheduled run is mailed to the people accountable for it** — the
script's owner and the administrator who approved the version — carrying the
run id, the failure, and the tail of what the script printed. Failures of runs
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
interpreter has no hard memory cap (see the [security model](security.md)), so a
pathological approved script pushes on the memory of whatever pod runs it;
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

`platform.export` writes an output to the destination its approval bound.

By default that destination is the **portal**, and output identity there is
stable: the pair of (script, output name) maps to **one** asset, and each run
writes a new version of it. A daily report therefore keeps its identity, its
shares, and its history instead of producing a new asset every morning, and a
year of runs leaves one asset with a year of versions.

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
a cadence. The wrong way to build one is to make the script re-emit the whole
document every run. That puts the markup inside the script's source, which
costs twice: every fire overwrites the current version wholesale, so a layout
edit made in the portal is destroyed by the next scheduled run; and changing
anything about the presentation — a chart color, a heading — means editing the
script, and an edited script cannot run until its new version is approved. The
right split keeps the template in the asset, where a layout change is an
ordinary document edit that survives the schedule, and the data in the script;
`platform.publish_data` is that split:

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
picks up. A version approved with a **bucket destination** writes the same bytes
to a bucket instead:

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

The script names a destination and nothing else. The connection, the bucket, and
the prefix come from the grant a reviewer bound to this version, so a script
supplies no endpoint, no credential, and no bucket of its own — see the security
model's [delivery section](security.md#delivery-leaving-the-platform).

- `destination` defaults to `portal`. A destination the grant does not name is
  refused inside the interpreter, before anything is issued.
- `key` is the object key beneath the destination's granted prefix. It defaults
  to the output name plus the format's extension (`weekly-sales.csv`), and a key
  that could climb out of the prefix is refused rather than cleaned up. The
  portal takes no key: it stores its own objects, and the output name is the
  identity there.
- `destination` and `key` must be passed **by name**; only `name`, `rows`, and
  `format` may be positional. A destination passed by position would be
  invisible to the static read a reviewer's capability diff is built from, and a
  diff that is quietly wrong is worse than one that is refused.
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

## Failures

A script failure is **never retried**. The same version, on the same inputs,
fails the same way, so retrying multiplies the cost and changes nothing; the run
is marked failed and carries the Starlark backtrace. The fix is to correct the
script, dry-run it, and have the correction approved.

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
can see, what it is executing, its cadence and next fire, and how its last run
went. Opening one shows its contract, and — for a script you own — its version
history with the grant bound to each approval, and its run history with each
run's trigger, duration, outputs, and the log it printed. See the
[portal guide](../server/portal-user.md#scripts).

A run is the owner's and the administrator's to read, plus whoever requested
that particular run. Asking an agent to show you your scripts opens the same
pages through the presentation-only `show_scripts` tool; every script operation
an agent performs for its own work uses `manage_script`, which renders nothing.

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
| Reading scripts and runs in the portal | A database and the portal; the pages are read-only |
| Approving (admin REST) | A database and the admin API |
| Running (`run_script`) | The above; the tool is not registered where there is no run queue |
| Executing what was queued | At least one replica with `scripts.worker.enabled` left on, which is the default |
| Firing schedules | The same replicas; scheduling needs no configuration of its own |
| Mailing a failed scheduled run | The email substrate (`notifications`, and an admin-configured mail server) |
| Writing portal outputs | A configured portal asset store and object storage; without them an export to the portal fails the run, which is the honest report for a scheduled asset that never appeared |
| Delivering to a bucket | An S3 connection the platform is configured with, not read-only, reachable by the persona the script's roles resolve to. It needs no portal: a run that only delivers writes nothing the platform keeps |
