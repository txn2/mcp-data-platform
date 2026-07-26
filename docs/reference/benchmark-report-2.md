# When do agents use stored knowledge? Derivability, capability, and the limits of a knowledge layer

*A neutral evaluation report for the mcp-data-platform knowledge layer. Every
statistic below is recomputed from raw run data committed under
`bench/results/` by the script `bench/report/pk_tables.py`; each claim cites
the run directory it comes from. This is the second report in the series;
the first is [the knowledge-layer benchmark report](benchmark-report.md).*

| | |
| --- | --- |
| **Author** | Craig Johnston (cj@imti.co), Deasil Works, Inc. / txn2 |
| **Published** | 2026-07-25 (draft, pending review) |
| **Report version** | 2.0-draft |
| **DOI** | pending |
| **Subject under test** | Whether an agent holding platform-delivered knowledge acts on it: the conditions under which stored knowledge is used, re-derived, or replaced by invention. |
| **Platform builds** | Runs are pinned per-manifest to commits on the `feat/1054-perishable-knowledge-bench` branch (v1.113.4 lineage); manifests carry the exact commit, seed-set hash, model, and driver for every run. A release-tagged rerun is the remaining step before a versioned DOI, mirroring report 1's practice. |
| **How to cite** | [Section 11](#11-how-to-cite-this-report) |

## Abstract

We ask when an agent, delivered knowledge by the platform it runs on,
actually uses that knowledge. The study began as a pre-registered
benchmark of a different hypothesis: that agents under-verify perishable
stored beliefs, and that epistemic metadata (volatility class, observation
age, recheck cost) would correct the deficit. The pre-registered premise
was falsified at first empirical contact and the falsification held under
every mechanism check we could construct: on the stronger model tier,
verification of checkable stored beliefs sits at ceiling regardless of a
recheck cost swept from one to eleven calls, regardless of the belief's
direction, and regardless of whether a belief was delivered at all, with
matched no-knowledge controls showing a median effort delta of exactly
zero calls. What emerged instead is a two-factor structure. First,
**derivability**: the same model that ignores deliverable-and-checkable
knowledge relies completely on knowledge it cannot re-derive (an internal
reporting convention was used in 8 of 8 attempts, and its absence produced
confident fabrication of a plausible substitute in most control attempts).
Second, **capability**: the weaker model tier inverts the derivable-note
result, trusting delivered state in 29 of 32 attempts instead of 0 of 32.
The strong-tier results replicate exactly on the raw model API with no
agent client in the loop, and the tier inversion occurs within a single
client, so neither result is a client artifact. A companion probe of the
knowledge lifecycle found the supersede mechanism at ceiling but strict
capture at 33%, decomposing into deterministic mis-filing against the
semantically nearest entity and silent non-capture. The practical
conclusion for knowledge-layer design: the value of delivered knowledge
concentrates in what agents cannot re-derive — conventions, definitions,
policies — where it is both used and fabrication-suppressing; delivered
observations of checkable world state are re-derived by strong models and
are a staleness liability for weak ones.

## 1. Relation to the pre-registered protocol

This report is the section-14 outcome of the pre-registered protocol in
`bench/docs/perishable-knowledge-study-design.md` (issue #1054). The
protocol fixed hypotheses, decision rules, and falsifiers before data; its
primary hypothesis H1a (agents under-verify perishable beliefs, revealed
threshold biased high) was falsified by the power pre-run, and the
pre-registered confirmatory matrix was therefore never executed. Per the
protocol's own commitment, the falsified premises are recorded on the
issue, and no confirmatory claims are made here: **every finding in this
report is exploratory**, reported with Wilson 95% intervals rather than a
Holm-corrected confirmatory family. What survived of the protocol is its
apparatus (fixture, worlds, minimal-pair seeds, derived cells,
deterministic grading, pre-analysis verification definition) and its
discipline: each follow-up run was gated by a decision rule stated before
the data, and the two runs that killed their own hypotheses are archived
alongside the runs that succeeded.

## 2. Apparatus

The fixture is a deterministic HTTP service (`bench/apisvc -surface
perishable`) serving a social-analytics-shaped catalog behind the
platform's API gateway: listening monitors (perishable state), monitor
trend series (volume and a sentiment score), owned-profile metrics, and a
workspace structure. A harness-only control plane changes the account's
**world** between sessions without a reset, so a belief planted in one
world can be queried in another, and the access log spans the change: a
recheck after the world moved is detectable as a decision. Worlds live in
a committed registry, minimal-paired per dimension. The cost of
re-establishing the perishable state is a world property: one call on an
unscoped account, up to eleven on a workspace-scoped one (the account
must be cleared workspace by workspace).

Beliefs are frozen, committed strings (`bench/specs/pk-seeds.json`)
composed from a factual core plus audited fragments; their provenance is a
capture corpus produced by driving the platform's own capture tool over
the fixture, with one declared exception (the reporting convention below,
which is study-authored because capture never produces conventions the
scenarios did not teach). A cell pairs a question, a belief (or none), and
a query world; its correct behavior — answer, refuse, verify-then-answer,
verify-then-refuse, probe-then-refuse — is derived from two computed
facts (is the question answerable in this world; is the belief true in
it), never hand-assigned. Grading is deterministic: verification is read
off the fixture access log under a pre-analysis definition fixed before
any data (only operations that ask about the state count; operations that
presuppose it are a separately-reported sensitivity measure), refusals key
on the sentinel the scaffold prescribes, and numeric answers grade against
ground truths computed from the same fixture data the service serves.

Episodes run as fresh platform identities over MCP: the belief is planted
through the real capture tool as the identity that will later be asked
(insight search is caller-scoped, which also makes this the faithful model
of an agent holding its own prior session's note), the world moves, and
the question is asked under a scaffold that licenses answering from saved
knowledge and never names the measured action. Two episode drivers are
used: the Claude Code CLI (subscription, `claude -p`, per-attempt MCP
config), and an in-process tool loop against the raw Messages API with no
agent client, added specifically to strip client behavior from headline
results. Models: `sonnet` (Sonnet 5 via either driver) and `haiku`
(Haiku 4.5, CLI). k=8 per cell throughout; every run archives its
manifest, per-attempt records, and full transcripts.

## 3. Results: the strong model does not use checkable delivered knowledge

Three run families establish the null, each closing an escape route the
previous one left open. All are sonnet via claude-cli unless noted; run
directories in Section 9.

**Verification at ceiling (pre-run).** Delivered a true belief ("zero
monitors provisioned") where refusing was correct and checking optional,
agents re-checked in 8/8 attempts (CI [68, 100]); delivered the same
belief where the world had moved and only checking could answer, 8/8.
Several transcripts state the redundancy plainly: "Verified directly
against the API … This matches a prior-session finding already on
record."

**Cost does not move it (recheck-cost sweep).** With the price of
establishing "nothing is provisioned" swept across 1, 3, 6, and 11 calls
by workspace scoping, verification held at 8/8 at every level — and
agents paid the full price rather than sampling: median calls made equal
cost plus one at every scoped level (the extra call is the refused
unscoped attempt), with all ten workspaces enumerated at the top.

**Delivery does nothing at all (answer sweep with controls).** Delivered
a true note that states the answer outright ("three monitors
provisioned"), agents verified 32/32 and trusted 0/32 (CI [0, 11]);
matched no-knowledge controls verified 16/16. The median effort delta
between being handed the answer and being handed nothing is zero calls at
both ends of the cost range. The note is not missed: it appears in every
belief transcript and is cited in most final answers — as corroboration
of a result the agent had already re-derived.

## 4. Results: the derivability bridge

The null above admits a deflationary reading — perhaps this model ignores
notes entirely. The bridge tests that with a belief whose content cannot
be re-derived: an authored internal reporting convention ("a monitor day
counts as positive coverage when its sentiment score for that day is 70
or higher") that no endpoint states, paired with a question requiring
real work plus the convention (count Brand mentions' positive-coverage
days in June). Thresholds 50 through 80 all yield distinct day counts, so
a stated answer betrays the threshold that produced it; the note-only
answer is 11, and the no-note control doubles as the leakage check.

With the note, sonnet used it 8/8 (CI [68, 100]) — every attempt fetched
the trend and counted at threshold 70. Without it, no control ever
produced 11 (zero leakage), 2/8 correctly declined, and 6/8 **fabricated a
definition** — adopting 50 as a "neutral midpoint" and answering
confidently. The same model that re-derives every checkable claim relies
completely on unfetchable testimony, and in its absence invents a
plausible substitute. A delivered convention therefore does double duty:
it is used, and it suppresses confident invention of institutional
definitions (75% fabrication without it, 0% with it).

## 5. Results: capability inverts the derivable regime

The identical answer-sweep cells on the weaker tier (haiku, same client,
same client version, same fixture and scaffold) invert the headline:
haiku trusted the delivered note in 29/32 attempts (CI [76, 97]),
verifying in 3/32, while its no-knowledge controls probed 16/16. Haiku
also uses the convention (5/6 clean attempts; four episodes lost to API
529s are recorded as failures), and its no-convention controls fabricated
6/6 with zero declines — worse than sonnet.

Haiku's trust is shaped, not blanket. On the stale *unavailability* note
(belief says nothing exists; the world now has data; the note cannot
answer the question), haiku still mostly probed (7/8), because the note
offers no answer to adopt. The exposure is answer-bearing notes, tested
directly in the stale-answer cell — the note says three monitors, the
account has been emptied, the truthful answer is zero:

| Driver | Condition | n | Verified | Trusted | Correct |
| --- | --- | --- | --- | --- | --- |
| sonnet, claude-cli | stale note | 8 | 8 | 0 | 8 (CI [68, 100]) |
| sonnet, claude-cli | no note | 8 | 8 | 0 | 8 |
| haiku, claude-cli | stale note | 8 | 2 | 6 | **0 (CI [0, 32])** |
| haiku, claude-cli | no note | 8 | 8 | 0 | 8 |

Sonnet is staleness-immune here as everywhere: it re-derived the state and
answered zero, 8/8. Haiku reported the note's stale value in 8/8 attempts.
Six trusted it with zero catalog calls. The other two are the sharpest
observation in the study: they made the call, received the empty listing
(`{"items":[]}` is in the transcript), and still answered three — the
stored note overrode fresh contradictory evidence. Measured against its
own perfect no-knowledge control, the delivered stale note took haiku from
8/8 to 0/8: **on the weak tier, a stale note is strictly worse than no
note at all.** This is the quantified staleness exposure that the strong
tier's re-derivation habit eliminates, and it prices the risk for
deployments that pair small models with a knowledge layer holding
world-state observations.

## 6. Results: neither headline is a client artifact

Two independent controls eliminate the agent client as the cause. Within
one client: sonnet and haiku behave oppositely through the same claude-cli
version, so the client cannot be forcing either behavior. Without any
client: the raw-API runs (in-process tool loop, `api:claude-sonnet-5`)
reproduce sonnet's results exactly — answer sweep 48/48 verified, 0
trusted, zero effort delta; bridge 8/8 conventions used, zero leakage,
4/8 control fabrication. The one visible driver difference is benign:
raw-API control fabrication (4/8) sits below the CLI's (6/8), a
non-significant gap on these n.

## 7. Results: the lifecycle probe

A companion probe reran the platform's isolated supersede sub-benchmark
at k=3 (30 protocol-runs, a3 arm, dedicated database) to test whether
report 1's wide supersede intervals concealed a defect. They did not:
conditional on capture, supersede ran 8/8 clean, 0/8 duplicates, 8/8
update correctness. The loss is upstream. Strict capture ran 10/30 (CI
[17, 50]), bimodal by protocol, and decomposes completely: roughly 11/30
captured the fact but filed it against the semantically nearest dataset
rather than the canonical one (every replicate of a given protocol
mis-filing the same way), and roughly 5/30 never invoked capture at all
despite an explicit instruction. Mis-filing matters because promotion,
URN-scoped delivery, and supersede matching all key on the linked entity;
it is a candidate mechanism for report 1's 46.7% cross-identity transfer.

## 8. Synthesis and design consequences

The results form a two-factor structure:

| | Strong tier (Sonnet 5) | Weak tier (Haiku 4.5) |
| --- | --- | --- |
| **Derivable delivered knowledge** (checkable world state) | Re-derived at any cost; delivery redundant; staleness-immune | Trusted when it answers the question; delivery efficient; staleness-exposed |
| **Non-derivable delivered knowledge** (conventions, definitions) | Used; suppresses fabrication | Used; suppresses fabrication |
| **No knowledge delivered** | Probes; on convention questions, mostly fabricates | Probes; on convention questions, always fabricates |

Read together with report 1 — whose +56-point knowledge-trap lift came
precisely from non-derivable facts (units conventions, fiscal calendars,
deprecations) — the picture is consistent across both studies: **the
value of a knowledge layer concentrates in what agents cannot re-derive.**
Delivered conventions are used by every tier tested and prevent confident
invention of institutional facts. Delivered observations of checkable
state are re-derived by strong models (making their delivery redundant
and their staleness harmless) and trusted by weak ones (making their
delivery efficient and their staleness a live risk whose size the
stale-answer cell measures directly).

The platform decisions follow. Retired, with the evidence above:
volatility/valid-until schema fields, freshness-and-recheck-cost
enrichment, and capture steering toward dated observations (capture
already writes dated, self-refreshing notes; the corpus shows it). Live,
with new evidence: capture filing (deterministic mis-attribution and
silent non-capture are the measured loss stages of the lifecycle), and
strict input validation at tool boundaries (#1057, surfaced by an agent
burning calls on a silently-ignored misnamed field). And one product
implication cuts across tiers: since weak tiers trust answer-bearing
notes, deployments that pair small models with the knowledge layer inherit
a staleness exposure that strong-model deployments measurably do not have.

## 9. Data availability

| Run family | Directory under `bench/results/` | Contents |
| --- | --- | --- |
| Capture corpus | `pk-corpus/` | 20 real capture episodes, 2 runs, transcripts |
| Power pre-run | `pk-prerun/` | 3 sonnet runs (incl. superseded scaffold v1, kept), 1 haiku |
| Recheck-cost sweep | `pk-costsweep/` | 32 attempts across 4 cost levels |
| Answer sweep | `pk-answersweep/` | sonnet CLI, haiku CLI, sonnet raw-API |
| Derivability bridge | `pk-bridge/` | sonnet CLI, haiku CLI, sonnet raw-API |
| Stale-answer cell | `pk-staleanswer/` | sonnet CLI, haiku CLI |
| Lifecycle probe | `s5-supersede-probe/` | 30 protocol-runs, transcripts, decomposition |

Every table above regenerates via `python3 bench/report/pk_tables.py`
from these directories, offline. Run manifests pin commit, model, driver,
client version (CLI runs), seed-set hash, and k.

## 10. Threats to validity

- **Model coverage.** Two models, one family, one pair per tier: the
  capability axis is a two-point contrast and "tier" is confounded with
  everything else that differs between Sonnet 5 and Haiku 4.5. Claims are
  about these models, not about capability in general.
- **Task breadth.** The headline cells rest on a handful of questions
  over one fixture family. The derivability contrast is within-fixture
  and controlled, but breadth is the cost of the controls.
- **Authored convention.** The bridge belief is study-authored (declared
  in the audit); the capture corpus contains no natural convention. Its
  leakage control is structural (distinct counts per threshold), but its
  phrasing is ours.
- **Exploratory status.** Decision rules were fixed before each run, but
  hypotheses were revised between runs as premises fell; nothing here is
  a pre-registered confirmatory result, and the intervals are per-rate
  Wilson bounds, not a corrected family.
- **Driver artifacts.** claude-cli inserts its own behaviors; the raw-API
  replication covers the sonnet headlines but not haiku, whose results
  are CLI-only. Four haiku bridge episodes were lost to API 529s and are
  excluded as failures, not graded.
- **Spend records.** The two metered raw-API runs predate per-attempt
  usage recording; their token spend is estimated (single-digit dollars),
  not archived. Later runs record usage per attempt.
- **Build pinning.** Runs are commit-pinned on a feature branch, not
  release-tagged; a tagged rerun precedes any versioned DOI.

## 11. How to cite this report

Draft; citation block and DOI to be assigned at publication, following
report 1's format.

## 12. Related work

Storage-side machinery for perishable knowledge exists across recent
memory systems (temporal validity edges in Zep; deterministic supersession
in MemStrata; forgetting-aware accuracy in FAMA; shelf-life estimation
from edit histories), but none measures whether a delivered signal changes
an agent's decision to trust or verify. Staleness detection has been
studied in purely conversational memory (STALE), where no verification
action exists; knowledge-conflict benchmarks (ConFiQA, ConflictQA,
WikiContradict) study context-versus-parametric conflicts the model cannot
check. The trust-in-automation literature studies a human relying on
machine advice; here the subject is the machine and the testimony is a
prior agent's. This report's contribution to that seam is empirical: given
a cheap verification action against a live world, the over-trust prior
inverts on a strong model — reliance concentrates where verification is
impossible — and persists on a weak one.
