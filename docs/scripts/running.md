# Running Managed Scripts

A managed script is authored interactively and executed unattended. This page
covers the second half: how a version becomes executable, what runs it, what a
run produces, and how long the record of it is kept. The authoring half —
writing, validating, and dry-running a script — is reached through
`manage_script`, and the security model behind everything here is
[Managed Scripts: Security Model](security.md).

## Nothing runs until a version is approved

A script is inert when it is written. The platform executes exactly one version
of a script, the one `scripts.approved_version_id` points at, and only an
approval writes that pointer. Until then:

- `run_script` refuses, naming `manage_script run_draft` as the way to execute
  the draft as yourself while you are still writing it.
- Nothing else executes the script at all.

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
(`script.ApplyEdit`): a script with an approved version keeps executing that
version and the edit becomes a draft in the review queue, while a script with
nothing approved applies directly. The route is `PUT
/api/v1/portal/scripts/{id}/source`, restricted to the script's owner and to
administrators, and it edits the SOURCE only — scope, personas, status, and the
parameter contract are structured decisions the tool owns, and the domain
refuses to mix a reviewable change with them anyway.

The source is parsed before anything is stored, so code that cannot run is
refused at the keyboard rather than at the next fire.

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
same treatment.

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
