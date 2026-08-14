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

Approving is one REST call against the admin API. It binds two things at once —
the code and the capabilities that code may use — because approving them
separately would mean approving a script whose reach could change afterwards.

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
        "destinations":  ["portal"]
      }' \
  https://platform.example.com/api/v1/admin/scripts/$SCRIPT_ID/versions/3/approve
```

The request never names roles. The authority an approved run presents is the
set of roles the version's **author** held when they wrote it, copied from the
version by the approval itself, so approving can narrow what a script reaches
and can never hand it access its author did not have.

An approval is refused when the grant does not cover what the code plainly
calls: a script approved without the connection it queries would fail on its
first statement, and that is not a decision anyone intends to make. Changing a
grant later means approving again, which re-stamps the approval alongside the
new capability set.

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

`platform.export` writes a portal asset. Output identity is stable: the pair of
(script, output name) maps to **one** asset, and each run writes a new version
of it. A daily report therefore keeps its identity, its shares, and its history
instead of producing a new asset every morning, and a year of runs leaves one
asset with a year of versions.

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

## What a deployment needs

| Capability | Requirement |
|---|---|
| Authoring (`manage_script`) | A database |
| Approving (admin REST) | A database and the admin API |
| Running (`run_script`) | The above; the tool is not registered where there is no run queue |
| Executing what was queued | At least one replica with `scripts.worker.enabled` left on, which is the default |
| Firing schedules | The same replicas; scheduling needs no configuration of its own |
| Mailing a failed scheduled run | The email substrate (`notifications`, and an admin-configured mail server) |
| Writing outputs | A configured portal asset store and object storage; without them a run still executes and `platform.export` reports the shape it would have written |
