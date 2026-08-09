package graphfix

// The corpus: an operations wiki for a fictional company's data platform. Ten
// pages sit on a cell's chain, the rest are ordinary neighbors, several of
// them referenced from on-path pages so every hop has visible alternatives.
//
// Numbers are chosen so no ground truth is guessable from convention (a purge
// window of 62 days, not 30 or 90) and so no ground truth is written twice
// anywhere in the corpus; graphfix.Validate enforces the second part.

var corpus = []Page{
	// ---- cell gt-d0-vacuum: the answer is on the entry page -----------------
	{
		Key:     "vacuum-runbook",
		Slug:    "warehouse-vacuum-job-runbook",
		Title:   "Warehouse Vacuum Job Runbook",
		Summary: "Operating notes for the nightly warehouse-vacuum job: what it reclaims, when it runs, how its row-count delta is reviewed and when it wakes the on-call engineer.",
		Tags:    []string{"runbook", "warehouse", "batch"},
		Body: `# Warehouse Vacuum Job Runbook

The nightly ` + "`warehouse-vacuum`" + ` job reclaims space left by the previous day's
compaction and rewrites the statistics the query planner reads. It is scheduled
inside the overnight maintenance window ({{page:batch-window-calendar|the overnight calendar says which slot}}) and
normally finishes in under forty minutes.

## Row-count delta

The job records the table row count before and after each pass and reports the
difference as the run's row-count delta. A small delta is expected: rows written
during the pass are counted twice and rows tombstoned earlier in the evening
disappear. A large delta usually means an upstream load overlapped the vacuum
window.

**Paging threshold: a row-count delta above 3400 rows pages the on-call
engineer.** Below that the delta is written to the run log and reviewed the
following morning by whoever holds the batch review shift
({{page:oncall-rotation-handbook|the rotation handbook says who}}).

## When it pages

An on-call page from this job is a request to look, not to act. The usual cause
is an overlapping load and the usual response is to let the next night's run
settle it. Repeat pages on consecutive nights mean the maintenance window is too
short for the volume and the window itself needs moving, which is a change to the
overnight calendar rather than to this job.

Tuning notes for the vacuum's own parameters, including the parallelism setting
and the statistics sample rate, are kept separately in
{{page:vacuum-tuning-notes|the tuning notes}}.`,
	},

	// ---- cell gt-d1-clickstream: entry -> storage class register ------------
	{
		Key:     "clickstream-export-runbook",
		Slug:    "clickstream-raw-export-runbook",
		Title:   "Clickstream Raw Export Runbook",
		Summary: "Operating notes for the clickstream-raw export: what it writes, where it lands, how it is registered for storage, and who to call when a nightly drop is missing.",
		Tags:    []string{"runbook", "export", "clickstream"},
		Body: `# Clickstream Raw Export Runbook

The ` + "`clickstream-raw`" + ` export writes the previous day's unaggregated event
stream to object storage as hourly partitions. It runs once a night, after the
event collectors have drained, and is the source every downstream sessionization
job reads from. Its naming and partition layout follow the platform convention
({{page:export-naming-convention|written down once for every export}}).

## Storage registration

The export is registered under **storage class SC-4**. The storage class is what
decides how long a written partition survives and what tier it sits on; the
classes and their windows are maintained centrally in
{{page:storage-class-register|a central register}}. The export itself sets no lifetime of its own,
and a request to keep a partition longer than its class allows is a
reclassification request, not a change to this job.

## Missing drops

A missing hourly partition is almost always a collector that has not drained
rather than an export failure. Check the collector lag first, then re-run the
single hour rather than the whole night: the export is idempotent per partition,
and the safe way to re-run an hour or a night is written up once for every
export ({{page:export-rerun-procedure|and applies here unchanged}}). The hour
each nightly drop is expected by, and what happens when one is repeatedly late,
is likewise set for every export in one place
({{page:export-delivery-expectations|rather than per job}}).
Field-level questions about what the partitions contain belong in
{{page:clickstream-schema-notes|the schema notes}}.

Anything leaving the platform boundary, including a copy of these partitions to
a partner bucket, is governed separately ({{page:egress-approval-policy|by an approval policy}}).`,
	},
	{
		Key:     "storage-class-register",
		Slug:    "storage-class-register",
		Title:   "Storage Class Register",
		Summary: "The central register of platform storage classes: the tier each class writes to, the window after which its objects are removed, and the review cadence for reclassification requests.",
		Tags:    []string{"standard", "storage"},
		Body: `# Storage Class Register

Every dataset the platform writes is registered under one storage class. The
class fixes the tier the bytes land on and the window after which they are
purged. Nothing else sets a lifetime: a job that wants its output kept longer is
asking to be reclassified.

## Classes

| Class | Tier | Purge window |
| --- | --- | --- |
| SC-1 | hot | 7 days |
| SC-2 | hot | 30 days |
| SC-3 | warm | 45 days |
| SC-4 | warm | 62 days |
| SC-5 | cold | 400 days |
| SC-6 | archive | held indefinitely |

The purge runs daily and removes whole objects once their write date is older
than the window. An object is never partially removed, so a window that falls
inside one keeps the whole object until the next boundary.

## Reclassification

Reclassification requests are reviewed monthly. A move up the tiers (cold to
warm, warm to hot) needs a stated reason for the extra cost; a move down needs
confirmation from the owning team that nothing reads the older partitions. A
class is never changed in place for a single dataset without updating this
register, because the purge reads the register and not the request.`,
	},

	// ---- cell gt-d2-billing: entry -> regulated tier -> change class --------
	{
		Key:     "billing-events-runbook",
		Slug:    "billing-events-stream-runbook",
		Title:   "Billing Events Stream Runbook",
		Summary: "Operating notes for the billing-events stream: its producers, its tiering, its replay behavior, and how a proposed change to it is handled.",
		Tags:    []string{"runbook", "stream", "billing"},
		Body: `# Billing Events Stream Runbook

The ` + "`billing-events`" + ` stream carries one record per billing state change:
subscription created, plan moved, invoice settled, refund issued. Three services
produce to it and every finance-facing report consumes it, directly or through
the settled-invoice view.

## Tiering

` + "`billing-events`" + ` is a **regulated-tier** stream. Regulated tier is not a
property of the payload, it is a property of what downstream consumers are
obliged to do with it, and it brings its own operating rules
({{page:regulated-tier-rules|kept with the governance standards}}). Those rules, not this runbook, decide how a
proposed change to the stream is announced and how long producers have to
prepare for it.

## Replay

The stream retains fourteen days of records and can be replayed from any offset
inside that window. A replay is visible to consumers as duplicate keys, so a
consumer that is not idempotent must be paused for the duration. The field
meanings and the enumerations each field accepts are documented in
{{page:billing-events-field-guide|the field guide}}.

## Adding a producer

A new producer is onboarded through the standard stream checklist
({{page:stream-onboarding-checklist|maintained centrally}}); the checklist covers the mechanics, not
the notice a schema change owes its consumers.`,
	},
	{
		Key:     "regulated-tier-rules",
		Slug:    "regulated-tier-operating-rules",
		Title:   "Regulated Tier Operating Rules",
		Summary: "What operating in the regulated tier obliges a stream owner to do: retention floors, access review, producer ownership duties and the change class regulated streams are governed by.",
		Tags:    []string{"standard", "governance"},
		Body: `# Regulated Tier Operating Rules

A stream enters the regulated tier when a downstream consumer is obliged to
reproduce its contents on demand. The tier is assigned by the data governance
group and reviewed annually.

## What the tier obliges

- **Retention floor.** A regulated stream keeps at least fourteen days of
  replayable records, regardless of the volume that implies.
- **Access review.** Every principal with read access is re-confirmed each
  quarter; an unconfirmed principal loses access at the review date rather than
  at a grace period after it.
- **Producer attestation.** Each producing service names an owner who attests,
  at onboarding and after any change, that the records it writes are complete.

## Changes

**Every regulated-tier stream is governed by change class CC-3.** The change
class fixes how a proposed change is announced, who signs it off and how much
advance notice consumers are owed; the classes are defined in
{{page:change-class-reference|the platform's central reference}}. A change that a stream owner believes needs
less notice than its class requires is a request to reclassify the stream, and
is handled by the governance group rather than by the owner.

Schema mechanics, including how a compatible change is registered and how an
incompatible one is staged, are separate from the notice question and live in
{{page:schema-registry-operations|the registry's own operating notes}}.`,
	},
	{
		Key:     "change-class-reference",
		Slug:    "change-class-reference",
		Title:   "Change Class Reference",
		Summary: "The platform's change classes: what each class covers, who signs a change off, and how much advance notice each one owes the consumers of a changed interface.",
		Tags:    []string{"standard", "governance", "change"},
		Body: `# Change Class Reference

Every interface the platform publishes carries a change class. The class is what
decides the paperwork and the notice, so a change is planned from its class
rather than from its size.

## Classes

**CC-1 — local.** No published interface changes. The owning team signs it off
and no notice is owed.

**CC-2 — additive.** A compatible addition to a published interface. The owning
team signs it off and consumers are given two business days of advance notice.

**CC-3 — governed.** A change to an interface whose consumers carry an
obligation of their own. Sign-off is the governance group's, and consumers are
owed **9 business days of advance notice** before the change is applied.

**CC-4 — breaking.** A change that invalidates a published contract. Sign-off is
the governance group's, consumers are owed a full quarter of notice, and the old
contract runs in parallel until the last consumer has moved.

**CC-5 — emergency.** A change applied ahead of its notice because leaving the
defect in place is worse. Sign-off is the duty manager's and the notice is
reconstructed afterwards, with the reason recorded.

## Counting the notice

Notice is counted in business days from the announcement to the change window
opening, excluding the announcement day itself. A notice period that would end
inside a change freeze runs to the first business day after the freeze lifts.

Where an announcement goes out, and what it has to contain, is a channel
question rather than a class question ({{page:change-announcement-channels|and is set out separately}}).`,
	},

	// ---- cell gt-d3-ledger: entry -> bands -> ladders -> matrix -------------
	{
		Key:     "ledger-reconcile-runbook",
		Slug:    "ledger-reconcile-job-runbook",
		Title:   "Ledger Reconcile Job Runbook",
		Summary: "Operating notes for the nightly ledger-reconcile job: what it compares, what a break means, and what a run of consecutive failures opens.",
		Tags:    []string{"runbook", "ledger", "batch"},
		Body: `# Ledger Reconcile Job Runbook

The nightly ` + "`ledger-reconcile`" + ` job compares the settled-invoice view against
the general ledger extract and writes one row per discrepancy. A discrepancy is
called a break. Breaks are expected in small numbers and are worked the next
morning ({{page:reconcile-break-triage|per the triage guide}}).

## Failure of the job itself

A failed run is different from a break. The job fails when it cannot read one of
its two inputs, which usually means the ledger extract has not landed; the close
calendar says when each extract is due ({{page:ledger-close-calendar|maintained beside the close periods}}).

A single failure is retried automatically on the next schedule. **Failures on
three consecutive nights open a severity band B incident**, because at that point
the reconciliation has not been proved for a full accounting period and the
question is no longer whether the job runs. What a band obliges, and how quickly,
is set centrally in {{page:incident-severity-bands|the severity standard}} rather than here.

## Manual runs

The job can be run by hand against a named business date once the missing input
has landed. A manual run does not clear an open incident; the incident closes on
the evidence that the scheduled run has recovered, not on a successful manual
one.`,
	},
	{
		Key:     "incident-severity-bands",
		Slug:    "incident-severity-bands",
		Title:   "Incident Severity Bands",
		Summary: "The platform's incident severity bands: what qualifies for each band, who is notified, and which escalation ladder a band follows.",
		Tags:    []string{"standard", "incident"},
		Body: `# Incident Severity Bands

An incident is assigned a severity band when it is opened; opening one, and
what closing one takes, has its own mechanics ({{page:incident-open-and-close|recorded separately}}).
The band is a statement about consequence, not about effort: a small defect
with an accounting consequence outranks a large one without.

## The bands

**Band A.** Customer-visible loss of service, or a defect that has already
produced an incorrect external figure. Notified immediately to the whole
platform group.

**Band B.** An internal control that has stopped proving what it exists to
prove, with no external figure yet affected. Notified to the owning team and to
the platform group's shift lead.

**Band C.** A degradation with a known workaround already in place.

**Band D.** A defect with no consequence before the next working day.

## Escalation

Each band follows a named escalation route, and the routes are held together in
{{page:escalation-ladders|one place}} so a change to one is visible against the others.
Band A follows the red route, **band B follows the amber route**, bands C and D
follow the standard route.

What is said while an incident is open, and by whom, is not a band question
({{page:incident-communication-guide|and is set out on its own}}).

The record an incident leaves behind, whatever its band, is described in
{{page:incident-postmortem-template|the postmortem standard}}.`,
	},
	{
		Key:     "escalation-ladders",
		Slug:    "escalation-ladders",
		Title:   "Escalation Ladders",
		Summary: "The platform's escalation routes: the rungs each route climbs, who sits on each rung, and where the timings for each rung are held.",
		Tags:    []string{"standard", "incident", "escalation"},
		Body: `# Escalation Ladders

An escalation route is an ordered list of rungs. An incident starts at the first
rung and climbs one rung at a time; skipping a rung is allowed only when the
person on it is the one escalating.

## The routes

**Red route.** First rung: the owning team's shift lead. Second rung: the
platform duty manager. Third rung: the head of platform. Fourth rung: the
executive on call.

**Amber route.** First rung: the owning team's shift lead. **Second rung: the
platform duty manager.** Third rung: the head of platform.

**Standard route.** First rung: the owning team's shift lead. Second rung: the
owning team's manager, during working hours only.

## Timings

The routes above say who, and in what order. How long an incident may sit on a
rung before it climbs is not part of the route, because the same rung is worked
to different clocks on different routes; those clocks are held in
{{page:duty-manager-matrix|the response matrix}}.

Who is on shift on any given night is the rotation's business
({{page:oncall-rotation-handbook|per its handbook}}), and the channel each rung is reached on is
listed in {{page:paging-channel-directory|the channel directory}}.`,
	},
	{
		Key:     "duty-manager-matrix",
		Slug:    "duty-manager-response-matrix",
		Title:   "Duty Manager Response Matrix",
		Summary: "The clocks each escalation route runs to: how long a rung may hold before it climbs, what a doubled overnight clock means, and what is written when a clock runs out.",
		Tags:    []string{"standard", "incident", "duty-manager"},
		Body: `# Duty Manager Response Matrix

This matrix holds the clocks. Each figure is the longest an incident may sit on
that rung of that route before it must reach the next one.

| Route | First rung | Second rung | Third rung |
| --- | --- | --- | --- |
| Red | 5 minutes | 10 minutes | 30 minutes |
| Amber | 20 minutes | 25 minutes | 2 hours |
| Standard | 4 hours | next working day | next working day |

A clock starts when the incident is opened, not when it is acknowledged, and it
runs across shift handovers. An unacknowledged clock that expires climbs on its
own: the escalation is automatic and the acknowledgement is recorded late rather
than the escalation being held back for it.

## Overnight

Between 22:00 and 06:00 the standard route's clocks are doubled, because the
rung below it is on a pager rather than at a desk. The red and amber routes are
worked to the same clocks at every hour: an incident on either of them is
already past the point where the hour is an excuse.

## Recording

Every climb writes a line to the incident record: the rung it left, the rung it
reached, the clock that expired and whether it expired unacknowledged.`,
	},

	// ---- nearer neighbors -------------------------------------------------
	// Pages that share a cell's subject without carrying its answer. They are
	// what a real handbook holds around any operational question, and they are
	// what keeps a cell honest: the answer page is the corpus's authority on the
	// question, so without them a search on the question's own words reaches it
	// directly and the cell's depth is nominal.
	{
		Key:     "collector-drain-runbook",
		Slug:    "event-collector-drain-runbook",
		Title:   "Event Collector Drain Runbook",
		Summary: "How the event collectors drain before the clickstream-raw export runs: the drain signal, the lag metric, and what to do when a collector will not drain.",
		Tags:    []string{"runbook", "clickstream", "collector"},
		Body: `# Event Collector Drain Runbook

The collectors buffer events at the edge and drain into object storage on a
five-minute cycle. The clickstream-raw export waits for a drain signal before it
starts, so a collector that has not drained holds the whole night's export.

## The lag metric

Drain lag is the age of the oldest buffered event. Under normal load it sits
below two minutes. A lag that climbs steadily is a downstream write problem; a
lag that jumps and recovers is a redeploy.

## When a collector will not drain

Restart it rather than draining it by hand: a hand-drained collector writes a
partial partition, and a partial partition is indistinguishable from a complete
one once it has landed. If a restart does not clear it, mark the hour as held
and let the export run without it; the hour can be re-exported once the
collector is healthy.`,
	},
	{
		Key:     "partition-compaction-notes",
		Slug:    "partition-compaction-notes",
		Title:   "Partition Compaction Notes",
		Summary: "How written partitions are compacted: the file-count trigger, what compaction rewrites, and why compaction never extends a partition's life.",
		Tags:    []string{"reference", "storage", "partitions"},
		Body: `# Partition Compaction Notes

A partition written by an export lands as many small files, one per writer.
Compaction rewrites them into a few large ones once the file count crosses the
trigger, which is set per dataset and defaults to two hundred files.

Compaction changes the layout and nothing else. In particular the partition
keeps its original write date: the date is a property of the data, not of the
last time the bytes were touched, and every lifecycle decision downstream is
made against that original date. A compaction that reset the date would quietly
extend the life of everything it touched.

Compaction runs opportunistically inside the maintenance window and is safe to
skip; a skipped partition is simply compacted the following night.`,
	},
	{
		Key:     "sessionization-job-runbook",
		Slug:    "sessionization-job-runbook",
		Title:   "Sessionization Job Runbook",
		Summary: "Operating notes for the sessionization job that reads the clickstream-raw partitions: its window, its idempotency, and how a late partition is picked up.",
		Tags:    []string{"runbook", "clickstream", "batch"},
		Body: `# Sessionization Job Runbook

The sessionization job reads the clickstream-raw partitions for a business date
and writes one row per session. It runs after the export completes and holds a
six-hour lookback so an event that arrived late still lands in the session it
belongs to.

The job is idempotent per business date: re-running it replaces that date's
output entirely rather than appending. A late partition is therefore handled by
re-running the date once the partition has landed, never by patching the output.

Sessions that span midnight are written to the date the session started. This is
the single most common source of disagreement with dashboards that group by
event time.`,
	},
	{
		Key:     "export-delivery-expectations",
		Slug:    "export-delivery-expectations",
		Title:   "Export Delivery Expectations",
		Summary: "When each nightly export is expected to land, how lateness is measured, and who is told when an export misses its expected hour.",
		Tags:    []string{"reference", "export"},
		Body: `# Export Delivery Expectations

Each nightly export has an expected landing hour, measured at the moment its
last partition is written rather than when the job starts.

| Export | Expected by |
| --- | --- |
| clickstream-raw | 02:00 |
| order-events | 02:30 |
| ledger-extract | 03:40 |

Lateness is counted in whole hours past the expected time and is reported in the
morning run review. An export that is late three mornings in a row is raised
with its owning team, because at that point the expectation is wrong or the job
is.

Nothing here says how long a delivered export is kept: landing and lifetime are
separate concerns with separate owners.`,
	},
	{
		Key:     "stream-deprecation-runbook",
		Slug:    "stream-deprecation-runbook",
		Title:   "Stream Deprecation Runbook",
		Summary: "How a stream is retired: consumer discovery, the parallel period, the read-only phase and the final removal of the billing-events style streams.",
		Tags:    []string{"runbook", "stream", "governance"},
		Body: `# Stream Deprecation Runbook

Retiring a stream is a four-phase operation and no phase may be skipped.

**Discovery.** Every consumer is identified from the contract registry and from
the last ninety days of read activity, because a consumer that reads monthly
does not appear in a week of logs.

**Parallel.** The replacement runs alongside the original and both are written.
Consumers move at their own pace and confirm they have moved.

**Read-only.** Producers stop writing. The stream stays readable so a consumer
that missed the move fails loudly on stale data rather than silently on absent
data.

**Removal.** The stream and its contracts are removed. The announcement and the
notice that preceded all of this are governed elsewhere; this runbook covers the
mechanics only.`,
	},
	{
		Key:     "consumer-contract-registry",
		Slug:    "consumer-contract-registry",
		Title:   "Consumer Contract Registry",
		Summary: "What the consumer contract registry records for each stream: who reads it, which schema version they pinned, and who is notified when it changes.",
		Tags:    []string{"reference", "stream", "contracts"},
		Body: `# Consumer Contract Registry

Every consumer of a published stream registers a contract. The contract records
the consuming service, the schema version it reads, an owner who can be reached,
and whether the consumer is idempotent.

The registry is what an announcement is sent to. A consumer that never
registered is not announced to, which is the reason the registration is a
condition of read access rather than a courtesy.

Contracts are re-confirmed when a consumer changes the version it reads.
A contract that has not been confirmed for a year is flagged in the quarterly
review as a probable orphan; flagged contracts are confirmed or removed, never
left flagged.`,
	},
	{
		Key:     "change-announcement-channels",
		Slug:    "change-announcement-channels",
		Title:   "Change Announcement Channels",
		Summary: "Where a change to a published interface is announced, which channel is the authoritative one, and what an announcement has to contain.",
		Tags:    []string{"reference", "change", "communication"},
		Body: `# Change Announcement Channels

An announcement goes to three places at once: the platform mailing list, the
platform channel, and the registry entry for the interface being changed. The
registry entry is the authoritative one, because it is the only channel a
consumer onboarded after the announcement will still see. Who the mailing
reaches is decided by the contracts registered against the interface
({{page:consumer-contract-registry|in the contract register}}).

An announcement contains what is changing, the window it will be applied in, who
signed it off, and how a consumer says it is not ready. It does not contain a
justification: consumers act on the change, not on the reason for it.

How far ahead of the window an announcement has to go out is not a channel
question and is not set here.`,
	},
	{
		Key:     "billing-events-replay-guide",
		Slug:    "billing-events-replay-guide",
		Title:   "Billing Events Replay Guide",
		Summary: "How to replay the billing-events stream safely: choosing the offset, pausing non-idempotent consumers, and confirming the replay landed.",
		Tags:    []string{"runbook", "billing", "stream"},
		Body: `# Billing Events Replay Guide

A replay re-delivers records the stream still holds. It is not a repair: a
record that was never written cannot be replayed.

**Choose the offset by timestamp, not by count.** Counting back is wrong
whenever a producer has been backfilling.

**Pause the consumers that are not idempotent.** The contract registry says
which ones those are. A paused consumer resumes from its own committed offset,
so nothing is lost by pausing.

**Confirm afterwards.** Compare the record count in the replayed window against
the count the consumer processed. A replay that is not confirmed is a replay
nobody can distinguish from a no-op.`,
	},
	{
		Key:     "ledger-extract-delivery",
		Slug:    "ledger-extract-delivery-runbook",
		Title:   "Ledger Extract Delivery Runbook",
		Summary: "How the nightly general-ledger extract is delivered, how a missing or truncated extract is recognized, and who is contacted upstream.",
		Tags:    []string{"runbook", "ledger"},
		Body: `# Ledger Extract Delivery Runbook

The general-ledger extract arrives nightly from the finance system as a single
file with a manifest. The manifest carries the row count and a checksum, and the
extract is not considered delivered until both match.

## Missing

A missing extract is the usual reason the reconciliation cannot run. Check the
manifest first: a manifest without its file means the transfer failed, while no
manifest at all means the upstream job did not run, and the two go to different
people.

## Truncated

A file whose row count is below its manifest is quarantined rather than loaded.
A truncated extract that is loaded looks exactly like a night on which finance
posted very little, and the difference is only discovered a period later.`,
	},
	{
		Key:     "incident-communication-guide",
		Slug:    "incident-communication-guide",
		Title:   "Incident Communication Guide",
		Summary: "What is said during an incident and to whom: the first notice, the rhythm of updates, the language to avoid, and who speaks to people outside the platform group.",
		Tags:    []string{"reference", "incident", "communication"},
		Body: `# Incident Communication Guide

The opening notice says what is affected, what is not, and when the next update
will come. It does not say what the cause is, because at that point nobody knows
and a guess in an opening notice outlives the incident.

Updates go out on the cadence the opening notice promised, including the ones
that say nothing has changed. A silent incident is read as a resolved one.

Only the incident lead speaks to anyone outside the platform group. This is not
about secrecy: it is that two well-meaning people describing the same partial
picture produce three versions of it.

Language to avoid: "should be fixed", "briefly", "minor". Say what was observed
and what was done.`,
	},
	{
		Key:     "night-shift-handover",
		Slug:    "night-shift-handover-notes",
		Title:   "Night Shift Handover Notes",
		Summary: "What the overnight shift hands to the morning: which jobs ran, which failed, what was left running, and what the morning review is expected to pick up.",
		Tags:    []string{"reference", "on-call", "batch"},
		Body: `# Night Shift Handover Notes

The handover is written, not spoken, and it is written as the night goes rather
than at the end of it.

It records which jobs completed, which failed and whether they were retried,
anything left running deliberately, and anything the morning review should look
at even though it did not fail. That last category is the useful one: a job that
completed while doing something odd is invisible unless someone writes it down.

An open incident is never handed over in the notes alone. The outgoing engineer
speaks to the incoming one, and the notes record that they did.`,
	},
	{
		Key:     "reconcile-failure-history",
		Slug:    "reconcile-failure-history",
		Title:   "Reconcile Failure History",
		Summary: "The record of past ledger-reconcile failures: how often the job has failed, what the causes were, and which nights required a manual run.",
		Tags:    []string{"reference", "ledger", "history"},
		Body: `# Reconcile Failure History

The reconciliation has failed on eleven nights in the last two years. The causes
group into three.

**Late extract.** By far the most common: the upstream file had not landed when
the job started. Every one of these cleared on the next schedule.

**Schema drift.** The extract gained a column and the loader rejected the file.
Two occurrences, both after an upstream release.

**Consecutive failures.** Once, on three consecutive nights, caused by a
transfer credential that had expired and was renewed only after the third
morning. That run is the reason the consecutive-failure case is now handled
explicitly rather than as three unrelated single failures.

Nothing here is a substitute for the current night's run log.`,
	},

	{
		Key:     "raw-zone-layout",
		Slug:    "raw-zone-layout",
		Title:   "Raw Zone Layout",
		Summary: "Where the nightly raw exports land: the bucket per environment, how each export's area is laid out, and the rule against writing into another export's area.",
		Tags:    []string{"reference", "storage", "export"},
		Body: `# Raw Zone Layout

The raw zone holds one bucket per environment and one prefix per export.
Underneath the prefix, partitions are laid out by the export's declared
partition columns in declaration order, so the same path shape works for every
export in the zone.

Nothing writes outside its own prefix. A job that needs to read another export's
output reads it in place; copying it under a second prefix produces two datasets
that drift apart and are both believed.

The zone is not browsable by hand at any useful scale. Use the catalog entry for
an export to find its prefix rather than listing the bucket, which on the larger
exports takes minutes and returns tens of thousands of keys.`,
	},
	{
		Key:     "export-rerun-procedure",
		Slug:    "export-rerun-procedure",
		Title:   "Export Re-run Procedure",
		Summary: "How to re-run a nightly export safely: single-hour re-runs, whole-night re-runs, what a re-run overwrites, and what has to be told downstream.",
		Tags:    []string{"runbook", "export"},
		Body: `# Export Re-run Procedure

A re-run replaces what it writes. Exports here are idempotent per partition, so
re-running one hour rewrites that hour and leaves the rest of the night alone.

**Single hour.** The usual case, after a collector was late. Re-run the hour and
tell the downstream jobs that read it; the sessionization job needs a re-run of
the whole business date, not of the hour.

**Whole night.** Only when the export ran against the wrong business date. A
whole-night re-run writes every hour again and is safe, but it does not remove
partitions written under the wrong date: those are removed by hand, and until
they are, both dates look complete.

A re-run never changes an object's original write date, so it does not extend
anything's life in the raw zone.`,
	},
	{
		Key:     "duty-manager-rota",
		Slug:    "duty-manager-rota",
		Title:   "Duty Manager Rota",
		Summary: "Who holds the platform duty manager role and when: the rota shape, the handover, the standing delegation, and how the rota is corrected when someone is unavailable.",
		Tags:    []string{"reference", "on-call", "duty-manager"},
		Body: `# Duty Manager Rota

The duty manager role rotates monthly among the platform leads. The rota is
published a quarter ahead so the person on it can plan around it, and it changes
only by the holder arranging a swap and recording it.

## Delegation

A duty manager who will be unreachable names a standing delegate for that
window. The delegate holds the role rather than assisting in it: there is no
state in which two people hold it and no state in which nobody does.

## Corrections

A rota corrected less than a day ahead is announced in the platform channel as
well as recorded, because the rota is read once at the start of a shift and not
re-read afterwards.`,
	},
	{
		Key:     "overnight-failure-review",
		Slug:    "overnight-failure-review",
		Title:   "Overnight Failure Review",
		Summary: "The morning review of overnight job failures: what is reviewed, how a repeat failure is recognized across nights, and what the review hands on.",
		Tags:    []string{"reference", "batch", "review"},
		Body: `# Overnight Failure Review

Every morning one engineer reviews the night's failures. The review is a
reading, not a repair: anything that needs fixing is handed to the owning team
with what the review found.

## Repeats

The review reads three nights at a time rather than one. A job that failed once
is noise; the same job failing on consecutive nights is the thing the review
exists to notice, and it is noticed only by someone looking across nights.

## What it hands on

For each failure: the job, the nights involved, whether it recovered on its own,
and whether anything downstream ran on partial input. The last of those is the
one that gets missed, because a job that succeeded on incomplete input leaves no
failure anywhere.`,
	},

	{
		Key:     "reconcile-manual-run",
		Slug:    "reconcile-manual-run-procedure",
		Title:   "Reconcile Manual Run Procedure",
		Summary: "How to run the ledger reconciliation by hand for a named business date after a failed night, and what a manual run does and does not settle.",
		Tags:    []string{"runbook", "ledger"},
		Body: `# Reconcile Manual Run Procedure

A manual run takes one business date and both of that date's inputs. It refuses
to start if either input is absent, which is the same refusal the scheduled run
makes and for the same reason: a reconciliation against one side is not a
reconciliation.

Run it from the platform host with the date, wait for the break count, and read
the breaks rather than the exit code. A run that produces an enormous break
count almost always means the extract was for a different date.

A manual run settles the numbers for that date. It does not settle whether the
scheduled job is healthy, and it is not evidence that the next night will run.`,
	},
	{
		Key:     "incident-open-and-close",
		Slug:    "opening-and-closing-an-incident",
		Title:   "Opening and Closing an Incident",
		Summary: "The mechanics of an incident record: who may open one, what has to be filled in at the start, what closing requires, and why an incident is never deleted.",
		Tags:    []string{"reference", "incident"},
		Body: `# Opening and Closing an Incident

Anyone may open an incident. Opening one costs almost nothing and not opening
one costs the timeline, so the bias is deliberate.

## At the start

An incident needs a one-line statement of what is wrong, the system it is wrong
in, and whoever opened it. Everything else is filled in as it is learned. An
incident that waits for a complete picture before it exists has no early
timeline, which is the part that is hardest to reconstruct later.

## Closing

Closing needs evidence that the condition has gone, not an absence of further
reports. For a scheduled job that means a clean scheduled run.

An incident is never deleted. One opened by mistake is closed as such, because
the record that somebody thought this was wrong is itself worth keeping.`,
	},

	// ---- neighbors ---------------------------------------------------------
	{
		Key:     "batch-window-calendar",
		Slug:    "nightly-batch-window-calendar",
		Title:   "Nightly Batch Window Calendar",
		Summary: "The overnight maintenance and batch windows: which jobs own which slot, how the windows are ordered, and how a window move is requested.",
		Tags:    []string{"reference", "batch"},
		Body: `# Nightly Batch Window Calendar

The overnight schedule is divided into windows, each owned by one job family. A
job that overruns its window is not killed, but the window behind it starts late
and the delay accumulates across the night.

| Window | Owner |
| --- | --- |
| 22:00 - 23:15 | collector drain |
| 23:15 - 00:45 | raw exports |
| 00:45 - 02:00 | maintenance and vacuum |
| 02:00 - 04:00 | aggregation |
| 04:00 - 05:30 | reconciliation |

Moving a window is a scheduling change, not a job change: the request goes to the
platform group with the reason and the earliest night it can take effect. A move
that pushes any window past 05:30 needs the aggregation owner's agreement,
because the morning reports read what aggregation writes.`,
	},
	{
		Key:     "oncall-rotation-handbook",
		Slug:    "on-call-rotation-handbook",
		Title:   "On-Call Rotation Handbook",
		Summary: "How the platform on-call rotation works: shift length, handover, the batch review shift, and how a swap is arranged.",
		Tags:    []string{"reference", "on-call"},
		Body: `# On-Call Rotation Handbook

The platform rotation runs in week-long shifts, handed over on Tuesday mornings
so a weekend never falls across a handover. The outgoing engineer walks the
incoming one through anything still open; an incident with an open clock is never
handed over silently.

## The batch review shift

Separate from the pager rotation, one engineer each morning reviews the overnight
run logs and the deltas the jobs reported. The review shift is a daytime duty and
carries no pager.

## Swaps

A swap is arranged directly between the two engineers and recorded in the roster
before the shift starts. An unrecorded swap is the most common reason a page
reaches the wrong person, so the roster is the source of truth and not the
conversation that preceded it.`,
	},
	{
		Key:     "vacuum-tuning-notes",
		Slug:    "vacuum-tuning-notes",
		Title:   "Vacuum Tuning Notes",
		Summary: "Parameters for the warehouse vacuum: parallelism, statistics sample rate, and the observed cost of each setting on the current cluster.",
		Tags:    []string{"reference", "warehouse", "tuning"},
		Body: `# Vacuum Tuning Notes

The vacuum's cost is dominated by the statistics pass rather than by the space
reclamation. Two parameters matter.

**Parallelism.** The default is four workers. Eight workers finishes roughly a
third faster on the current cluster and leaves noticeably less headroom for the
aggregation window that follows, so it is used only after a backlog.

**Statistics sample rate.** The default samples one row in a thousand. Raising
the sample improves the planner's estimates on the widest tables and costs
roughly linear time; lowering it is a false economy, because the planner then
chooses a scan the aggregation window cannot absorb.

Neither parameter changes what the job reports at the end of a run, so a tuning
change is never a reason to reinterpret a run log.`,
	},
	{
		Key:     "export-naming-convention",
		Slug:    "export-job-naming-convention",
		Title:   "Export Job Naming Convention",
		Summary: "How export jobs and their output paths are named: the segment order, the partition layout, and the rules for renaming an existing export.",
		Tags:    []string{"convention", "export"},
		Body: `# Export Job Naming Convention

An export job is named ` + "`<subject>-<grain>`" + `, lower case, hyphen separated, with
no environment segment: the environment is a property of the deployment, not of
the job. The output path repeats the job name and then the partition columns in
declaration order, and lands inside the raw zone, whose layout is fixed for
every export ({{page:raw-zone-layout|and documented once}}).

Renaming an existing export is a two-step change. The new name is created as an
alias first and both names are written for one full window, then the old name is
retired once no consumer has read it for a week. A rename that skips the alias
step breaks every consumer that resolves the path by string rather than by
catalog lookup, which in practice is most of them.`,
	},
	{
		Key:     "clickstream-schema-notes",
		Slug:    "clickstream-schema-notes",
		Title:   "Clickstream Schema Notes",
		Summary: "What the clickstream-raw partitions contain: the event envelope, the properties map, the identity fields and the known gaps in older partitions.",
		Tags:    []string{"reference", "clickstream", "schema"},
		Body: `# Clickstream Schema Notes

Every record carries an envelope and a properties map. The envelope holds the
event name, the emitted timestamp, the received timestamp and the identity
fields; everything product-specific sits in the properties map and is not typed.

## Identity

Two identity fields travel with each event: the device identifier, present on
every record, and the account identifier, present only after sign-in. Joining on
the account identifier therefore silently drops the pre-sign-in part of a
session, which is the single most common error in clickstream analysis here.

## Known gaps

Partitions written before the collector upgrade carry the received timestamp in
local time rather than in UTC. The sessionization jobs correct for it; anything
reading the raw partitions directly must correct for it too.`,
	},
	{
		Key:     "egress-approval-policy",
		Slug:    "data-egress-approval-policy",
		Title:   "Data Egress Approval Policy",
		Summary: "When data may leave the platform boundary, who approves it, and what an approved egress route is obliged to record.",
		Tags:    []string{"policy", "governance"},
		Body: `# Data Egress Approval Policy

Any route that writes platform data to a destination the platform does not
operate is an egress, whether it runs once or nightly. An egress needs a named
recipient, a stated purpose and an end date before it is approved.

Approval sits with the data governance group. A route without an end date is not
approved; a route whose end date has passed is disabled automatically rather
than reviewed, and re-enabling it is a new request.

Every approved route records, per run, what it sent and how much of it. The
record is the point of the approval: an egress nobody can describe afterwards is
indistinguishable from one that was never approved.`,
	},
	{
		Key:     "stream-onboarding-checklist",
		Slug:    "stream-onboarding-checklist",
		Title:   "Stream Onboarding Checklist",
		Summary: "The steps for putting a new producer onto an existing stream: contract registration, throughput declaration, dead-letter wiring and the first-week watch.",
		Tags:    []string{"checklist", "stream"},
		Body: `# Stream Onboarding Checklist

1. Register the producer's contract against the stream's current schema version.
2. Declare an expected throughput. The declaration sizes the partitions; a
   producer that exceeds its declaration by an order of magnitude is throttled
   rather than allowed to starve the others.
3. Wire a dead-letter destination. A producer with nowhere to put a rejected
   record will retry it forever.
4. Name an owner who can be paged.
5. Watch the first week. Most contract mismatches appear on the first weekend,
   when the traffic mix changes and fields that were always present stop being
   present.

The checklist covers putting a producer on a stream. It does not cover changing
what the stream carries.`,
	},
	{
		Key:     "billing-events-field-guide",
		Slug:    "billing-events-field-guide",
		Title:   "Billing Events Field Guide",
		Summary: "Field-by-field meaning of the billing-events records: the state enumeration, the money fields and their units, and the fields that are only populated for refunds.",
		Tags:    []string{"reference", "billing", "schema"},
		Body: `# Billing Events Field Guide

## State

The state field takes one of five values: created, activated, moved, settled,
refunded. States are not ordered: a subscription can move several times between
activation and settlement, and a refund can follow a settlement by months.

## Money

Two money fields travel on each record, both integers in minor units, both in the
account's own currency. There is no converted amount on the record; conversion is
a reporting concern and is done against the rate table for the settlement date.

## Refund-only fields

The reason code and the original reference are populated only on refunded
records. Treating an absent reason code as "no reason given" therefore
misreports every non-refund record as a refund without a reason.`,
	},
	{
		Key:     "schema-registry-operations",
		Slug:    "schema-registry-operations",
		Title:   "Schema Registry Operations",
		Summary: "How schema versions are registered and staged: compatibility checking, staged rollout of an incompatible version, and rollback of a registered version.",
		Tags:    []string{"reference", "schema", "operations"},
		Body: `# Schema Registry Operations

A schema version is registered before a producer writes it, and the registry
refuses a version that is incompatible with the stream's declared compatibility
mode.

## Staging an incompatible version

An incompatible version is staged rather than registered: the new version is
written to a shadow subject, consumers are moved across one at a time, and the
subject is promoted once the last consumer reads the new version cleanly. The
staging mechanics say nothing about when the change may happen; that is the
change class's business.

## Rollback

A registered version is never deleted, only superseded. Rolling back means
registering the previous definition as a new version, so the history reads
forward and a consumer that pinned a version still resolves it.`,
	},
	{
		Key:     "reconcile-break-triage",
		Slug:    "reconcile-break-triage-guide",
		Title:   "Reconcile Break Triage Guide",
		Summary: "How to work the breaks the nightly reconciliation writes: the break categories, which ones self-clear, and when a break becomes an accounting question.",
		Tags:    []string{"runbook", "ledger", "triage"},
		Body: `# Reconcile Break Triage Guide

A break is one discrepancy between the settled-invoice view and the ledger
extract. Breaks fall into three categories.

**Timing.** The two sides saw the same event on different days. Timing breaks
clear themselves on the next run and are only worth investigating when the same
key breaks on three separate nights.

**Sign.** The two sides agree on the magnitude and disagree on the direction.
Almost always a refund recorded as a charge upstream; it does not self-clear.

**Orphan.** One side has a record the other has never seen. An orphan older than
one close period stops being a data question and becomes an accounting one, and
is handed to finance with the record rather than resolved in the platform.`,
	},
	{
		Key:     "ledger-close-calendar",
		Slug:    "ledger-close-calendar",
		Title:   "Ledger Close Calendar",
		Summary: "When each ledger extract is due, how the close periods are dated, and what a close period means for platform changes.",
		Tags:    []string{"reference", "ledger", "calendar"},
		Body: `# Ledger Close Calendar

The general ledger extract is due nightly at 03:40 and by 06:00 on the first two
working days of a month, when the close batch runs ahead of it.

## Close periods

A close period runs to the last working day of the month. Records dated inside a
closed period are never restated in place; a correction is posted into the open
period with a reference back.

## Freeze

Platform changes that touch anything the ledger reads are frozen from the last
working day of the month to the second working day of the next. The freeze does
not stop an incident response, and a change applied during a freeze for incident
reasons is recorded as such.`,
	},
	{
		Key:     "incident-postmortem-template",
		Slug:    "incident-postmortem-standard",
		Title:   "Incident Postmortem Standard",
		Summary: "What every incident record must contain when it closes: the timeline, the conditions that allowed it, the corrections, and how the record is reviewed.",
		Tags:    []string{"standard", "incident"},
		Body: `# Incident Postmortem Standard

Every closed incident leaves a record, whatever its band. The record has four
parts.

**Timeline.** What was observed and when, in the order it was observed, including
the observations that turned out to be irrelevant.

**Contributing conditions.** The conditions that had to hold for the incident to
happen. Written as conditions rather than as causes, because the useful ones are
usually the conditions nobody chose.

**Corrections.** What changed as a result, each with an owner. A correction with
no owner is a wish and is not recorded as a correction.

**Review.** The record is read by someone who was not involved. The reviewer's
job is to find the part of the timeline that only makes sense to the people who
were there.`,
	},
	{
		Key:     "paging-channel-directory",
		Slug:    "paging-channel-directory",
		Title:   "Paging Channel Directory",
		Summary: "How each role is reached out of hours: the primary channel, the fallback, and what a silent page falls through to.",
		Tags:    []string{"reference", "on-call", "paging"},
		Body: `# Paging Channel Directory

Each role has a primary channel and one fallback. A page that is not
acknowledged on the primary within its own window repeats on the fallback; a page
unacknowledged on both is treated as unreachable and the sender moves on rather
than waiting.

| Role | Primary | Fallback |
| --- | --- | --- |
| Shift lead | pager app | phone |
| Duty manager | phone | pager app |
| Head of platform | phone | assistant's phone |
| Executive on call | phone | none |

Channel changes are made in the directory and take effect on the next shift, not
immediately, so a change made mid-incident does not silently redirect a page that
is already in flight.`,
	},
	{
		Key:     "platform-glossary",
		Slug:    "platform-glossary",
		Title:   "Platform Glossary",
		Summary: "Terms used across the platform handbook: break, drop, window, rung, partition, and the difference between a job failing and a job reporting a problem.",
		Tags:    []string{"reference", "glossary"},
		Body: `# Platform Glossary

**Break.** One discrepancy written by a reconciliation, not a failure of the
reconciliation itself.

**Drop.** One scheduled delivery of an export. A missing drop is a delivery that
did not arrive; a late drop is one that arrived outside its window.

**Partition.** The unit a dataset is written and purged in. Nothing is ever
written or removed in units smaller than a partition.

**Rung.** One position on an escalation route. An incident occupies exactly one
rung at a time.

**Window.** A named span of the overnight schedule owned by one job family.

**Failing versus reporting.** A job fails when it cannot complete. A job reports
a problem when it completes and does not like what it saw. The two are worked by
different people on different clocks, and conflating them is the reason most
overnight pages reach the wrong person.`,
	},
}
