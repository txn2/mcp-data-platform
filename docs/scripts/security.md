# Managed Scripts: Security Model

This document is the threat model for managed scripts: what the feature adds to
the platform's attack surface, what it deliberately does not, and where the
residual risk sits. It is the security counterpart to the platform-wide
[Threat Model](../security/threat-model.md), which this document assumes rather
than repeats, and it is revised in step with the feature: every change to the
managed-scripts surface updates it in the same change.

Every claim below carries a package or file citation a reviewer can check
against the source. Where a protection does not exist, this document says so.

Reviewed at the state of the tree that introduced approved execution: the
capability grant an approval binds, the script principal a run executes as, the
run queue, and the `run_script` tool. The revision before it introduced the
script domain, the Starlark engine, and the authoring loop.

## What a managed script is

A managed script is a small Starlark program the platform stores, versions, and
governs, so that a process whose logic is already solved (a KPI report, a
recurring export) can be re-run without re-deriving it through a model. Scripts
are authored by an agent through the `manage_script` MCP tool, executed by an
embedded interpreter, and constrained to a small, enumerable set of host
functions.

The state of the feature at this revision:

- A script can be created, edited, validated, and dry-run.
- A version can be **approved**, which binds the capability set that version may
  use and points the execution gate at it. Approval is the load-bearing control
  of everything below.
- An approved version executes on demand through `run_script`: the tool queues a
  run and a worker on some replica executes it, unattended, as the script's own
  principal. Nothing else executes a script; there is still no scheduler.
- An approved run **writes**: `platform.export` persists a portal asset version.
  A draft run still writes nothing — it reports the shape an output would have
  (`internal/platform/scriptrun/host.go`, `hostState.export`,
  `persistOrPreview`).

## The claim this feature makes about authority

**A managed script can never do what the person who wrote it could not do.**

That is a structural property, not a policy, and it holds across both execution
paths.

**A draft run authenticates as the caller.** `run_draft` reads the caller's own
identity off the `PlatformContext` of the `manage_script` call that reached it
and injects exactly that identity into the run's session
(`internal/platform/scriptlayer/rundraft.go`, `connectAuthorSession`). A caller
can do nothing through `run_draft` that they could not do by calling the same
tools directly. The proof is an integration test against the real assembled
server asserting that the audit row for a script's query carries the author's
user id, email, and persona
(`internal/platform/scriptlayer/rundraft_integration_test.go`,
`TestIntegration_DraftRunCarriesTheAuthorIdentityAndIsAudited`).

**An approved run authenticates as `script:<name>`, carrying the roles the
version's AUTHOR held.** An unattended run happens with nobody present — no
token, no session, no live identity to resolve — so the authority it presents
has to have been captured earlier. It is captured from the author, at the moment
they write the version (`pkg/script/version.go`, `Author`;
`internal/platform/scriptlayer/scriptlayer.go`, `callerAuthor`), stored on the
immutable version row (`script_versions.author_roles`), and copied into the
grant by the approval itself (`internal/platform/scriptstore/approve.go`,
`ApproveVersion`). **The approval request cannot name roles.** A reviewer
decides what a script may *reach*; they cannot decide what authority it holds.

Both paths cross the full middleware chain. Each host call is one MCP tool call
over a per-run in-memory session against the assembled server
(`internal/platform/scriptrun/session.go`, `SessionCaller.CallTool`), so
authentication, persona and connection authorization, rate limiting, and audit
all apply exactly as they do to an agent's call. None of it is re-implemented,
which is what keeps it from drifting.

What approved execution DOES add, and what this document does not minimize:

- **Standing authority.** The roles a version captured keep working after the
  author stops working — over a weekend, and after they leave. That is inherent
  to unattended automation, and the controls over it are the approval gate, the
  script lifecycle (disable, deprecate, supersede — each refused by the gate at
  execution time), and audit.
- **Definer rights.** Anyone who can *see* a script can run it, and it runs with
  its own authority rather than theirs, so its output can reach a caller who
  could not have produced it. Visibility is therefore the control: a global
  script is runnable by everyone who can see it, and scope is part of what a
  reviewer approves.

`middleware.SourceScript` is a label on a call, not a capability. It records how
the call arrived so audit can separate populations, and it selects three
behaviors described below; it grants nothing (`pkg/middleware/mcp.go`).

## Assets

| Asset | Why it matters |
|---|---|
| Script source and its version history | Executable code under governance; the artifact a reviewer approves |
| `scripts.approved_version_id` | The execution gate. The one pointer that decides what the platform executes on its own |
| `script_versions.author_roles` | The authority ceiling an approval may bind. Whoever can write a version decides what approving it can grant |
| `script_versions.grants` | What an approved version may reach: roles, connections, capabilities, destinations |
| Data reachable through `platform.query` | Whatever the running identity's persona and connections allow |
| Run records (`script_runs`) | Parameters, timings, output ids, and the log a run printed; readable by anyone who can see the script |
| Output assets | Portal assets a run writes, with the script principal as owner and the script's owner as the accountable person |
| Run logs | Bounded free text a script chooses to emit; may echo queried data |
| Connection credentials | Never reachable from a script; held by the platform and used by the toolkit |

## Actors

| Actor | Capability at this revision |
|---|---|
| Script author (any authenticated caller) | Creates and edits their own personal scripts; runs drafts as themselves |
| Admin persona | The above at every scope, plus scope and lifecycle changes |
| Reviewer (admin API) | Approves a version, binding the connections, capabilities, and destinations it may use; cannot widen its authority beyond the version author's roles |
| Script principal (`script:<name>`) | Exists only inside an approved run; holds the grant bound at approval and nothing else |
| The script itself | Only the host functions listed below, only through the running identity |

## Trust boundaries

```mermaid
graph LR
    subgraph Authoring["Authoring (agent, interactive)"]
        AGENT[MCP agent] -->|manage_script| TOOL[scriptlayer]
    end
    subgraph Review["Review (admin REST)"]
        ADMIN[Reviewer] -->|approve + grant| GATE
    end
    subgraph Governed["Governed state"]
        TOOL -->|ApplyEdit| STORE[(scripts +<br/>script_versions<br/>author_roles, grants)]
        STORE --> GATE[approved_version_id<br/>written only by approval]
    end
    subgraph Sandbox["Sandbox (per run)"]
        TOOL -->|run_draft, caller identity| ENGINE[Starlark interpreter<br/>step + wall-clock limits]
        GATE -->|approved source + grant| QUEUE[(script_runs<br/>lease + fencing)]
        QUEUE -->|worker claim, script:name| ENGINE
        ENGINE --> STDLIB[platform.query / platform.export / print<br/>json / date / run]
    end
    subgraph Platform["Existing platform"]
        STDLIB -->|one tool call each| SESSION[in-memory MCP session]
        SESSION --> MW[middleware chain<br/>auth / authz / rate limit / audit]
        MW --> TRINO[query toolkit]
        MW --> ASSETS[portal assets + object storage]
    end
```

Two boundaries matter. The first is between the interpreter and the platform: a
script reaches the outside world only by calling a host function, and every host
function crosses the middleware chain as the running identity. There is no other
edge out of the interpreter.

The second is the execution gate. Everything to the left of it is authoring,
where a script is inert; everything to the right runs with nobody watching. One
pointer, written by one action, separates them.

## Controls

### The execution gate

`scripts.approved_version_id` is the whole of it. The runner loads code from
that pointer and from nothing else, and one action writes it
(`internal/platform/scriptstore/approve.go`, `ApproveVersion`) inside one
transaction under the script row lock, which also stamps the approval, binds the
grant, applies an approved draft onto the live record, and supersedes the other
pending drafts — so the code being served and the code being executed cannot
diverge.

The gate is re-read at execution, not merely at queueing (`pkg/script/run.go`,
`RefuseRun`). Between a run being requested and a worker picking it up, a script
can be disabled, deprecated, superseded, or re-approved onto a different
version; each of those refuses the run rather than executing a version whose
approval has moved. One function decides this for every path into execution, so
a second producer of runs cannot arrive with a slightly different idea of what
is executable.

The edit funnel is the other half: once a version is approved, a change to a
script's substance becomes a pending draft rather than an edit to what executes
(`pkg/script/edit.go`, `RequiresReview`), and the store re-validates that under
the row lock so an edit racing an approval is refused rather than applied.

### Grants: what an approved version may reach

A grant is bound to a version at approval and carries four lists
(`pkg/script/grants.go`):

| Axis | Meaning | Enforced by |
|---|---|---|
| `roles` | The authority the run presents. Copied from the version's author; not accepted from the approval request | The authorization middleware, which resolves them to a persona exactly as for a person |
| `connections` | Which named connections the script may query | The host facade, and independently the persona's connection rules |
| `capabilities` | Which host bindings the script may call | The host facade |
| `destinations` | Where output may be written | The host facade |

Three properties are deliberate:

- **No wildcards, anywhere.** A reviewer must be able to read a grant and know
  exactly what was approved; `*` would make the review a formality.
- **Deny by default.** An empty list grants nothing, so a version approved with
  no destinations may compute and not write — a useful, safe state rather than
  an accident (`hostState.allowDestination`).
- **An unnamed connection is refused.** `platform.query` without a connection
  would resolve to whichever connection the deployment defaults to, which is a
  connection no approval named and which can change underneath an approved
  script (`hostState.allowConnection`).

Enforcement is layered, and neither layer is load-bearing alone. The host facade
refuses an ungranted call inside the interpreter, with a message naming what was
granted, so an author reads why their script stopped. The middleware chain
enforces the persona the run's roles resolve to, independently of the grant and
whatever it says. An integration test proves both by removing one at a time
(`internal/platform/scriptlayer/execution_integration_test.go`,
`TestIntegration_UngrantedConnectionIsRefusedAtBothLayers`).

An approval is also refused when the grant does not cover what a static read of
the source shows the code calling (`internal/httpserver/scripthttp`,
`refuseUnreachableGrant`): approving a script that will refuse itself on its
first query is not a governance decision anybody meant to make.

### The script principal

An approved run authenticates as `script:<name>`
(`pkg/script/script.go`, `Principal`), injected with
`middleware.WithPreAuthenticatedUser` and tagged `middleware.AuthTypeScript`
(`internal/platform/scriptexec/runner.go`, `connect`). It is a distinct
principal for every gate, rate limiter, and audit row, so a governed automation
and the person who owns it are never confused for one another; the owner's
address rides alongside on the same call, which is what keeps a run attributable
to an accountable human.

The principal holds no authority of its own. Its roles are the grant's roles,
and the middleware resolves them to a persona exactly as it does for a person —
so the persona, not the grant struct, is the authority of record.

### The run queue: leases, fencing, and exactly-once output

Runs are claimed with the queue shape the platform already uses elsewhere: an
`UPDATE ... FOR UPDATE SKIP LOCKED` that marks the row running, counts the
attempt, and stamps a lease (`internal/platform/scriptstore/runs.go`, `Claim`).
Crashed-worker recovery is part of the claim predicate — a lease that expired
makes the row claimable again — so there is no reaper and no leader election,
and every replica can run a worker.

Two properties protect a reclaimed run:

- **Fencing.** Every write against a run carries the lease it was taken under
  (`leaseClause`), so a worker whose run was reclaimed writes to nothing and is
  told so, rather than overwriting the result of the worker that took over.
- **No double-write.** A run records each output as it lands, and a reclaimed
  run reads that record and skips what it already produced
  (`internal/platform/scriptexec/export.go`, `Export`). Output identity is
  stable — one asset per (script, output name), a new version per run — so a
  recurring report accumulates versions rather than assets.

Retry classification is decided by **where** a failure happened, never by
reading an error message (`internal/platform/scriptexec/worker.go`, `attempt`).
Everything outside the interpreter — opening the run's session, reading the
script or its version — is the platform's own fault and is retried with backoff
under a small attempt budget. Everything the interpreter reports is final: a
Starlark error on the same inputs reproduces exactly, and a script that has
already queried or written must not be replayed on the chance that its last call
was a transient fault. A query engine being unreachable therefore fails a run
rather than retrying it, because from outside the interpreter it is
indistinguishable from bad SQL — and guessing between them by matching strings
would trade a visible failure for a silent double-write.

### Isolating execution from serving

Which replica executes a run is a security control, not only a capacity one.
`scripts.worker.enabled` (`*bool`, default on) decides whether a replica claims
from the queue. Left alone, one process serves and executes, which is the right
shape for the deployments most platforms start as. Set false, the replica keeps
serving MCP and portal traffic and enqueueing runs, and never claims; a separate
deployment of the same binary with the worker on executes them.

What that buys is blast-radius containment for the one limit the interpreter
does not enforce. There is no hard per-script memory cap (see [Resource limits,
stated honestly](#resource-limits-stated-honestly)), so a pathological approved
script pushes on the memory of whichever pod runs it. On a combined pod that
pressure lands on the process an agent is talking to; on a split deployment the
worst case is a restarted worker, while sessions, the portal, and the admin API
are untouched — and the run itself is not lost, because a killed worker's lease
expires and another claims it.

The worker adds no attack surface of its own: it accepts no request and takes
work only from the queue, and its calls go through the same assembled MCP
server, the same middleware chain, and the same grant they would on a combined
pod. The pod is the same binary, so it still starts the HTTP listener and its
health and metrics endpoints — what changes is that a worker deployment needs no
Service and no Ingress, so nothing routes to it. Reachability, not the process,
is what shrinks: the pod running unattended code is the one pod nothing outside
the cluster can address.

Two things do not move with the worker. `run_draft` stays in process on whatever
replica the author is talking to: it is bounded interactive authoring under the
author's own identity, not queue work. And the approval gate is unchanged —
a worker executes `approved_version_id` and nothing else, so where a run
executes never affects what may execute.

Shutdown is bounded on both sides. A draining worker stops claiming
immediately, gives a run in flight a short capped window out of the shutdown
budget — a few seconds, and never more than half of what is left, because that
budget belongs to every component the lifecycle stops — and releases anything
that does not finish back onto the queue rather than recording a verdict on it (`internal/platform/scriptexec/worker.go`,
`Stop`). A released run is claimable at once and its recorded outputs are not
written twice, so a rolling deploy neither strands a lease until it expires nor
kills a run mid-write.

### Audit under the script principal

Two kinds of row, joined by one key:

- The per-capability tool-call rows the middleware already writes, carrying
  `user_id = script:<name>`, the owner's email, `source = script`, and the run
  id as the session.
- One `script_run` lifecycle event per run
  (`pkg/audit/event.go`, `EventTypeScriptRun`), written off the execution path
  following the `prompt_serve` pattern, carrying the script, its id, the version,
  the run id, the owner, the trigger, who requested it, and the attempt.

Both carry the run id as their session id, so a run and every call it made join
on one key. Audit failures are logged and never fail a run.

### The language is the sandbox

Starlark is the engine because determinism and isolation are properties of the
language rather than of a blocklist the platform must maintain
(`internal/platform/scriptrun/scriptrun.go`). Starlark has no ambient clock, no
randomness, no filesystem, no network, and no module system; iteration order is
specified. A script can affect the world only through bindings the host chooses
to predeclare, and the predeclared set is exactly `platform`, `json`, `date`,
and `run` (`predeclared`, and `isPredeclaredName` in `validate.go`, which is the
same list validation checks against).

Two dialect switches are deliberately off:

- `while` and recursion, because both are unbounded control flow whose cost
  cannot be read off the source.

Two are deliberately on, and neither affects safety or determinism: top-level
control flow, and reassignment of a top-level name. Starlark's defaults for
those come from Bazel, where a file is a declaration loaded by other files; a
managed script is a procedure executed once, top to bottom, by one runner.

### Resource limits, stated honestly

| Limit | Mechanism | Where |
|---|---|---|
| CPU | Interpreter execution-step cap; a draft is capped tighter than an approved run, because somebody is waiting for a draft | `scriptrun.DraftMaxSteps`, `ApprovedMaxSteps` |
| Wall clock | Context deadline bridged to thread cancellation, covering time spent inside host calls | `scriptrun.DraftTimeout`, `ApprovedTimeout`, `watchCancel` |
| Result size | Hard row and byte caps on every `platform.query` result, with the row cap pushed down into the query | `scriptrun.DraftMaxRows`, `ApprovedMaxRows`, `DraftMaxResultBytes`, `hostState.queryResult` |
| Output size | Cap on one serialized output, matching the portal export ceiling | `scriptexec.maxOutputBytes` |
| Concurrency | One run at a time per replica, which is the only lever that bounds how much heap concurrent scripts can reach | `internal/platform/scriptexec/worker.go` |
| Blast radius | Which replicas execute at all, so the memory a script can reach belongs to a pod nothing is talking to | `scripts.worker.enabled` |
| Truncation | A result the engine truncated at the cap FAILS the run rather than being handed over as complete | `hostState.queryResult`, `truncated` |
| Log size | Bounded capture, head kept, tail dropped with a marker | `scriptrun.MaxLogBytes`, `logBuffer` |
| Outputs per run | Capped | `maxExports` |
| Source size | Capped before the parser sees it | `script.MaxSourceBytes` |

**There is no hard memory cap.** Neither starlark-go nor any comparable embedded
interpreter offers one, and this document does not pretend otherwise. A
pathological script can grow the process heap despite the step limit, because
allocation per step is unbounded. The mitigations in place are the step limit,
the wall-clock deadline, the host-side result caps, one run at a time per
replica, and `GOMEMLIMIT` at the process level.

The risk changed shape when execution became unattended: a pathological script
now runs where nobody is watching it, and by default on a replica that is also
serving. What stands between that and an out-of-memory condition is the approval
— the code was read by a human before it could run this way — plus the limits
above. It is recorded in [Residual risks](#residual-risks), and the control that
bounds what an out-of-memory condition costs is
[isolating execution from serving](#isolating-execution-from-serving).

### SQL parameters are bound, never spliced

`platform.query` takes `:name` placeholders and a `params` dict, and the host
renders each value as a typed SQL literal before the statement is sent
(`internal/platform/scriptrun/bind.go`, `bindSQL`). Strings are single-quoted
with embedded quotes doubled; a NUL byte is refused rather than escaped;
numbers, booleans, null, and lists of scalars each have one rendering; anything
else is refused.

Substitution is state-aware: a `:name` inside a string literal, a quoted
identifier, or a comment is text, and `::` is a cast rather than the start of a
placeholder. Getting that wrong in either direction is a defect — a rewritten
literal, or an unbound placeholder reaching the engine — so both directions are
covered by tests (`bind_test.go`).

Binding is offered because the alternative is worse. Without a bound list an
author builds an `IN` clause by joining strings, which is exactly the
concatenation this path exists to remove. The engine that ultimately executes
the statement still applies its own read-only interception (`trino_query`
refuses writes); binding is defense in depth on top of that, not a replacement
for it.

### A partial result is a failure, not a result

The row cap is pushed down as the query's own limit, so the engine stops at
exactly that many rows. That makes a length check useless as a truncation
signal, and the failure it would have caught is the worst kind: a script that
sums the first N rows of a larger result reports a wrong total with nothing in
the output to show that anything was missing. `platform.query` therefore reads
the query tool's own truncation flag and fails the run
(`internal/platform/scriptrun/host.go`, `truncated`). Silently wrong is the one
outcome the determinism contract exists to exclude, so it is worth a hard
failure and a message that says to aggregate in SQL.

### Credentials never live in a script

Connections are named; their credentials stay in platform connection
configuration exactly as they do for every toolkit. `validate` scans source for
credential-shaped literals (`internal/platform/scriptrun/validate.go`,
`secretPatterns`), and severity follows confidence:

- A pattern matching a specific credential FORMAT — a private-key header, an AWS
  access key id, a GitHub or Slack token, a JWT, a URL with inline credentials —
  is an **error** and blocks the save. Nothing else looks like those.
- A pattern matching a NAMING convention (`password = "..."`) is a **warning**.
  That string is a credential in Go source and an ordinary predicate inside a
  SQL string, and a text scanner cannot tell them apart; blocking would refuse
  to store a legitimate query with no way around it. It is surfaced for the
  reviewer instead.

A pattern scan finds what it recognizes and nothing more; it is a tripwire that
catches the paste, not a proof of absence.

### Unparseable source is never stored

`create` and `patch` both validate before writing (`commands.go`, `content.go`).
A script that does not parse is not a draft, it is a typo, and refusing it means
every stored version is one a reviewer could meaningfully read.

### The three `SourceScript` middleware behaviors

Each is a structural consequence of a script run being a per-run in-memory
session with no model in it. All three are pinned by tests in
`pkg/middleware/script_source_test.go`.

1. **Exempt from the session and search-first gates**
   (`isStatelessShimSource`). A script cannot perform the `platform_info`
   handshake and cannot perform a discovery step, because there is no model in
   a script run to do either — the same structural reason the portal tool runner
   and the gateway REST shim are exempt. What the search-first gate steers an
   agent toward happened when a person authored the script. The function fails
   closed: an unknown source is not exempt.
2. **An isolated per-run session identity** (`connectAuthorSession`, `runner.connect`,
   `mintIsolatedRunSessionID`, `DiscoveryScopeKey`). One run is one session: the
   run id is minted with its own prefix (`pkgsession.ScriptSessionPrefix`) and
   threaded onto the run's session context, so every platform call the run makes
   records that same id and the id the author is handed back is the id in the
   audit rows. A run can never advance or read the gate, provenance, or dedup
   state of the person it runs for. The tool-call middleware mints one as a
   fail-safe when a run arrives without one, keyed on "does not already carry a
   run id" rather than on "has no session id": on a stdio deployment the
   resolved id is the per-process `stdio` sentinel rather than empty, and an
   emptiness test would leave every script run there sharing one scope. On the
   degenerate path where minting fails, the scope is empty rather than the
   user's, so isolation holds.
3. **Enrichment is skipped** (`pkg/middleware/mcp_enrichment.go`). Enrichment
   appends cross-service context that varies with catalog state, which is
   precisely the variation the determinism contract promises a script will not
   see. It is also pure cost: a script consumes structured content.

None of that reaches `run_script`, which is an ordinary agent-facing tool and
exempt from nothing: it is authenticated and authorized as the agent calling it.
It is deliberately not on the search-first gate's list of query tools, because
the agent is not querying — it is asking the platform to execute code a person
wrote and a reviewer approved, and the discovery the gate steers toward happened
while that person was authoring it.

### Determinism, precisely

The contract is:

> Same script version + same parameters + same underlying data produce the same
> output.

It is not "identical forever." The warehouse changes between runs, and that is
the point of re-running. What the platform eliminates is every source of
variation it controls: no clock or randomness is reachable, the fire time is a
pinned value on `run.fire_time` rather than a clock read, enrichment is off, and
map keys are converted in sorted order so Go's randomized map iteration cannot
leak into a script's own output (`internal/platform/scriptrun/convert.go`).

Determinism is a security property here as well as a correctness one: it is what
makes a run explainable after the fact from its own record, and what makes
"never retry a script error" safe (a failure on the same inputs will recur, so
retrying only multiplies the cost).

## Threats and mitigations

| Threat | Mitigation | Citation |
|---|---|---|
| A script reaches data its runner may not see | Every host call crosses persona and connection authorization as the running identity | `rundraft.go`, `pkg/persona/filter.go` |
| A draft escalates beyond its runner | The run authenticates as the caller; no identity is synthesized | `connectAuthorSession` |
| An approval hands a script more access than its author had | The approval copies the version author's roles and refuses roles from the request | `approve.go`, `ApproveVersion` |
| An approved script reaches a connection nobody approved | The grant refuses it in the interpreter, and the persona refuses it in the middleware | `host.go`, `allowConnection`; `pkg/persona/filter.go` |
| An unnamed connection resolves to the deployment default | An approved run must name its connection | `host.go`, `allowConnection` |
| Code is swapped under a live approval | A substance edit to an approved script becomes a draft; the gate re-validates under the row lock | `pkg/script/edit.go`, `version.go` |
| A run executes a version whose approval has moved | The gate is re-read at execution, not trusted from the queue row | `pkg/script/run.go`, `RefuseRun` |
| A retired script keeps executing on a queued run | Disabled, deprecated, and superseded are each refused by the same gate | `RefuseRun` |
| A crashed worker's run is executed twice concurrently | Lease-based claiming, with every write fenced on the lease it was taken under | `runs.go`, `Claim`, `leaseClause` |
| A reclaimed run writes its output twice | The run records each output as it lands; a reclaimed run skips what it wrote | `scriptexec/export.go` |
| A transient fault silently replays a script that already wrote | Retry is classified by where the failure happened; nothing the interpreter reports is retried | `scriptexec/worker.go` |
| A caller reads the runs of a script they cannot see | Run reads apply the script's own visibility rule, and answer the same way for "not yours" and "no such run" | `scriptlayer/runs.go`, `readableRunScript` |
| A script escapes the interpreter | No IO, filesystem, network, or module system is predeclared | `scriptrun.go`, `predeclared` |
| A script builds a statement out of untrusted values | Typed literal binding with a state-aware scanner | `bind.go` |
| A script carries an inline credential | Secret scan blocks it as an error at validate time | `validate.go`, `secretPatterns` |
| A script burns unbounded CPU or wall clock | Step limit and deadline, both bridged to interpreter cancellation | `scriptrun.go`, `Run` |
| A script pulls an unbounded result | Row and byte caps, row cap pushed into the query | `host.go`, `queryResult` |
| A script floods the log | Bounded capture with an explicit truncation marker, cut on a rune boundary | `host.go`, `logBuffer` |
| A script silently computes on a partial result | A truncated query result fails the run | `host.go`, `truncated` |
| A retired script keeps executing | `run_draft` refuses a disabled or superseded script | `rundraft.go`, `runnable` |
| An edit path skips the record checks | `create`, `update`, and `patch` all run `Script.Validate` on the final state | `commands.go`, `content.go` |
| A run pollutes its runner's session state | Per-run minted session identity | `pkg/middleware/mcp_session_handle.go` |
| A script edit slips past review | The edit funnel defers a substance change to a draft version once a version is approved, and refuses to mix it with unversioned changes | `pkg/script/edit.go` |
| A script's calls are invisible after the fact | Every host call is audited with the running identity and `source=script` | `pkg/middleware/audit.go` |

## Non-goals

- **A script is not a privilege boundary.** A draft runs with its runner's
  authority and an approved run with the authority its approval bound. A person
  who should not reach a connection must not be granted it; a script will not
  add a second gate that saves a persona misconfiguration.
- **Running a script is not restricted separately from seeing it.** Anyone who
  can see a script can run it, and it runs with its own authority. Scope is the
  control, and it is part of what a reviewer approves.
- **No content sanitization of outputs.** A run writes what the script produced;
  nothing inspects it for sensitive values before it becomes an asset.
- **The secret scan is not a proof.** It recognizes known credential shapes.
- **No content sanitization.** Data returned by a query and printed into a log
  is not scanned for anything.
- **No defense against a malicious admin.** As in the platform threat model.

## Residual risks

1. **No hard memory cap.** Described above under resource limits. Mitigated by
   the step limit, deadline, result caps, and `GOMEMLIMIT`; not eliminated. What
   bounds the damage rather than the allocation is
   [isolating execution from serving](#isolating-execution-from-serving):
   `scripts.worker.enabled: false` on the serving replicas plus a worker
   deployment of the same binary confines the pressure to pods nothing is
   talking to, so the worst case is a restarted worker and a reclaimed run. That
   is available today and is the recommended posture wherever scripts do real
   work. The remaining gap — an allocation ceiling the interpreter itself
   enforces — needs a WASM engine with a real memory bound.
2. **Approval is the load-bearing control, and there is no review UI yet.**
   A rubber-stamped approval is how a prompt-influenced agent gets code running
   unattended, and today approving is a REST call rather than a surface that
   shows a reviewer what they are agreeing to. The material exists — the review
   endpoint returns the version, the capabilities and connections its source
   reaches for, and what the grant does not cover — and the approval refuses a
   grant the code would outrun. The human surface over it is the next stage of
   the feature, deliberately sequenced before external delivery destinations.
3. **Approval is an API call, so "a human approved it" is policy, not
   mechanism.** The admin REST API is also reachable over MCP through the
   built-in `platform-admin` gateway connection, which means an agent holding
   the admin persona can call the approval endpoint. That is the platform's
   existing self-configuration posture rather than something scripts introduce
   (see the platform threat model's stance on a malicious or compromised
   admin), but it is worth stating plainly here: the control that stops a
   prompt-influenced agent from approving its own script is who holds the admin
   persona, not the shape of the endpoint.
4. **A version authored by an admin carries admin roles.** Roles are captured
   from whoever wrote the version, and an admin editing a shared script is the
   author of what they wrote. Approving that version binds those roles. The
   approval response and the version record both state the roles being bound, so
   the widening is visible rather than silent, but nothing refuses it: two
   deliberate admin actions can put a script on an admin's authority.
5. **Standing authority outlives the author.** The roles a version captured keep
   working after the person who held them stops. Disabling, deprecating, or
   superseding the script is what stops it, and each is enforced at execution.
6. **A computed connection is not statically knowable.** `validate` reports
   `dynamic_connections` when a `platform.query` call computes its connection
   instead of naming one, so a reviewer is told the list is incomplete rather
   than shown a list that quietly omits it.

## Maintenance contract

This document is revised in the same change as the code, not afterwards. Any
change that adds a host function, changes a limit, changes what identity a run
carries, adds an execution path, changes the grant model, or changes the
approval model must update the corresponding section here. A change to the managed-scripts surface
that leaves this document untouched is incomplete.
