# Context economics: a pre-registered study of what an MCP platform's context surface costs per turn, and what it buys

> **Status: pre-registration in review.** Hypotheses, arms, decision rules, and
> minimum-effect thresholds are fixed here, before the controlled runs in
> #1171. Nothing in this document reports a result. The separation analysis in
> section 4 is work item zero and requires user sign-off on issue #1170 before
> the protocol merges; the banner changes to "pre-registered, runs pending
> (#1171)" at that point.

Study protocol for issue #1170, sub-ticket of epic #1164. It follows the
knowledge-pollution pre-registration (`knowledge-pollution-study-design.md`) in
structure, and like that one it is written after its premise probe rather than
before, which is the findings register's lifecycle rule: probe before protocol,
so a study is pre-registered only once its phenomenon is known to exist. What
is pre-registered here is the confirmatory matrix and its decision rules, not
the existence of the effect.

The measurement machinery is committed with this protocol:
[`bench/reports/context-economics/decompose.py`](../reports/context-economics/decompose.py)
and its [README](../reports/context-economics/README.md). Every number this
document quotes from the archives is recomputed by that script and pinned by
`make bench-report-check`, so the protocol cannot drift away from the data it
cites.

**Framing rule, binding on every artifact in this study** (protocol, run
archives, report, register rows, commit messages, PR bodies). This is a cost
and benefit study of our own architecture, published with its mitigation story.
The benefit side is already published and is treated here as a manipulation
check, not re-litigated. No security-negative framing, and no vocabulary that
casts the platform's own context as an attack surface: the words this study
uses are *cost*, *payload*, *per-turn context*, *static surface*, and
*population*.

## 1. The scientific object

An MCP server publishes a surface before any work happens: tool definitions,
instructions, and whatever its discovery tools return. A client re-sends that
surface, plus everything accumulated so far, on every turn. The object of this
study is the bill that produces, decomposed into parts a deployment can act on
separately, and the boundary at which each part starts paying for itself.

The knowledge-layer report established the benefit side: delivered business
context lifts trap accuracy by 56 points and is accuracy-neutral where it is
not needed
([`docs/reference/benchmark-report.md`](../../docs/reference/benchmark-report.md),
DOI 10.5281/zenodo.21438044). It did not price that benefit. This study
supplies the price, per component, at current code, and states where the two
curves cross.

The unit of interest is **per-turn context, not per-episode tokens**. Those are
different quantities with different levers, and conflating them is the mistake
this study exists to correct. A token written once when a session's prefix is
established is billed once. A token in the prefix is billed again on every
turn, multiplied by episode length. Section 6 defines the decomposition; the
short form is that the second quantity is where the money is, and that it
splits again into a static part the server charges for existing and an
accumulated part the conversation earns.

### 1.1 Position in the field, and the seam

The survey is the epic's (#1164), restated rather than re-derived. The
context-cost conversation in the literature is framed almost entirely around
**tool-definition count**: RAG-MCP (arXiv 2505.03275) and LongFuncEval
(2505.10570) both measure degradation as the published tool list grows, and
client-side tool search reports selection accuracy rising from 49 to 74 percent
when the list is retrieved rather than sent whole. The closest controlled work
on result content varies tool *descriptions* (2602.14878: median +5.85 points
of success, +67 percent execution steps, regressions in 16.7 percent of cases).
The MCP specification's own discussion of result metadata cites no behavioral
data at all.

The seam, stated precisely: **the server-side result-payload axis, decomposed
into the components that bill differently, measured against the benefit the
same platform's published study already established, with the decomposition
procedure published as a reusable instrument.** No surveyed work separates
static surface from accumulated payload, and none prices either.

The honest scope is empirical and methodological. Absolute figures here are
properties of this platform, this model, and this fixture. Section 9 states
which claims travel.

## 2. Motivating evidence: the archival re-analysis

The premise probe was archival and cost nothing: a re-analysis of the
knowledge-layer study's four-arm phase-2 matrix (raw API, Sonnet, n=261
attempts per arm), read in place under the cross-study re-analysis convention
in [`bench/README.md`](../README.md). Provenance table:
[`bench/results/context-economics/probe/README.md`](../results/context-economics/probe/README.md).

Re-analysis motivates and never confirms. Everything in this section describes
a build from 2026-07-14 (`v1.102.0-9-gadfb9d90-dirty`) under an instrument
defect since fixed (#1176: every arm persona denied `fetch`, so every body an
agent read had to arrive inside a search result). No number here is cited as a
property of current code. Each one is a reason to go and measure.

### 2.1 What the archives show

Median per attempt, and the same components priced at Sonnet 5 rates:

| Arm | Total tokens | cache creation | cache read | Static prefix bound | Median spend | xa0 |
| --- | --- | --- | --- | --- | --- | --- |
| a0 raw toolkits | 23,696 | 1,683 | 21,373 | 2,585 | $0.0169 | 1.0x |
| a1 + enrichment | 26,396 | 2,889 | 22,476 | 2,508 | $0.0199 | 1.2x |
| a2 + discovery | 57,329 | 4,201 | 52,443 | 5,127 | $0.0323 | 1.9x |
| a3 + lifecycle tools | 148,079 | 5,906 | 141,911 | 9,809 | $0.0573 | 3.4x |

Four observations shape the design, and two of them correct the probe's own
reading of its numbers. The probe is archived unedited; the corrections live in
its README and here.

**The token ratio is not the cost ratio.** a0 to a3 is 6.2x in tokens and 3.4x
in dollars, because cache read bills at a tenth of input while output bills at
five times it. Every cost claim this study makes is stated in dollars, with the
token figure beside it. Spend is computed per episode and then summarized,
never as a sum of component medians -- the median of sums is not the sum of
medians, and on these archives the shortcut moves the headline ratio.

**Cache creation does not measure the static surface.** The probe read the
modest growth in `cache_creation` (1,683 to 5,906) as evidence that the static
tool surface is not the multiplier. That inference does not hold: cache
creation counts tokens *written* to cache, and a prefix shared by hundreds of
attempts in one run is written once and read thereafter. The static surface
shows up instead as the floor of per-turn context, and that floor nearly
quadruples across the arms (2,585 to 9,809 tokens, section 6.2). It is
multiplied by turn count, so it is not a rounding error: on the a3 arm the
static prefix alone accounts for roughly 60 percent of the median per-request
context.

**On the tasks that need knowledge, the cheap surface is the expensive arm.**
Pooled figures hide a reversal the whole study turns on. On the trap suite the
median episode costs $0.0665 at a0 and $0.0419 at a2 (means $0.0849 and
$0.0469): the arm with the smallest published surface costs 59 percent more per
episode than the arm with the largest, because without discovery an agent takes
a mean of 19.0 tool calls against a2's 11.4 and re-reads a growing conversation
on each one. On the suite where knowledge is not needed the order is the
expected one, a0 at $0.0138 against a2 at $0.0215.

The cost of a context surface is therefore not the surface. It is the surface
minus the work the surface saves, and both terms have to be measured on the
same tasks. This is also where the minimum effects in section 4 come from: the
archived a0-to-a1 difference is $0.0116 per episode on the traps and $0.0004 on
the knowledge-not-needed suite, so a threshold of $0.005 sits between the two
by a comfortable margin in both directions.

**The a2-to-a3 gap is not attributable, and population is not the leading
candidate.** The probe named a confound (store population versus arm
configuration) and treated population as the likely explanation. The archives
do not support that ranking. The two runs differ in at least four ways, and one
of them is decisive-looking on its own: a3's config declares a
`memory.embedding` block and a2's does not, and the platform hands that
provider to the search router
(`knowledge.NewRouter(cfg.Embedding, ...)`, `internal/platform/searchfed/searchfed.go:157`),
so it is a search-*ranking* lever and not only a memory feature. The
transcripts show exactly that split: a2 ranked `lexical` on 1,027 of its 1,028
federated searches and a3 ranked `hybrid` on 979 of 982. Under lexical ranking
the `endpoints` group matched **zero** candidates across the entire a2 run,
against 12,700 across a3, and `knowledge_pages` matched 853 against 7,716.
Whether the stores also differed cannot be recovered from these archives.

The consequence for the design is direct: an RQ1 that varies population while
letting ranking mode vary with it would reproduce the confound rather than
close it. Section 4.1 holds the embedder constant.

### 2.2 The result-set shape, which sets a ceiling

The search tool allocates a display budget across sources (default 10 hits,
caller-settable to 50; `pkg/knowledge/router.go`). What the arms did with that
budget differs qualitatively:

| Arm | Federated searches | Returned nothing | Median hits | At the display budget |
| --- | --- | --- | --- | --- |
| a2 | 1,028 | 398 | 1 | 22 |
| a3 | 982 | 262 | 10 | 637 |

a2's searches mostly came back nearly empty; a3's mostly came back full. That
is the difference behind the payload figures (median search result 601 chars at
a2, 3,393 at a3), and it carries a pre-registrable prediction: **payload growth
with population has a ceiling at budget saturation.** Once a search fills its
display budget, adding more material to the stores changes which hits are shown
but not how many, so cost stops tracking population and starts tracking per-hit
size. An RQ1 whose cells both sit above saturation would measure that ceiling
and report a null that says nothing about the effect. Section 4.1's cells
straddle it deliberately.

One shape must not be confused with the other. A `search` call that passes
exactly one source and no intent *enumerates* that source, and the answer is a
paged envelope the display budget never touches, at five times the page size.
a2 issued 32 such calls and a3 issued 8; the toolchain counts them apart, and
so must any arm that reports "search payload".

## 3. Apparatus

### 3.1 The four archived arms, and what actually differs between them

Verified by diffing the committed configs at commit `a373ff7f`, which every
archived manifest pins. The probe's claim that a2 and a3 differ only in the
persona allow-list is wrong, and the correction is load-bearing for section 2.1.

| | a0 | a1 | a2 | a3 |
| --- | --- | --- | --- | --- |
| Persona allow-list | `trino_*`, `s3_*` | same as a0 | + `search`, `fetch`, `list_connections`, `datahub_*` | + `memory_*`, `apply_knowledge`, `manage_feedback` |
| Semantic provider | none | DataHub | DataHub | DataHub |
| `enrichment.*` | the four cross-enrichment switches off | defaults (on) | defaults (on) | defaults (on) |
| `workflow.require_search` | false | false | default (on) | default (on) |
| `memory.embedding` | absent | absent | absent | Ollama, `nomic-embed-text` |
| `knowledge` block | absent | absent | absent | `enabled`, `apply.enabled` |

### 3.2 Mechanisms this study may vary, each verified present

An arm may only name a mechanism the platform has. The list below is the whole
inventory; anything outside it is a product candidate, not an arm.

| Mechanism | Where | Default |
| --- | --- | --- |
| `enrichment.column_context_filtering` | `pkg/platform/config.go:736` | on (`*bool`, nil = enabled) |
| `workflow.require_search` | `pkg/platform/config.go:1271` | on (`*bool`, nil = enabled) |
| Persona tool allow-lists | arm config `personas:` | per arm |
| Search display budget | `pkg/knowledge/router.go`, caller `limit` | 10, max 50 |
| Embedding provider (search ranking) | `memory.embedding`, wired at `internal/platform/searchfed/searchfed.go:157` | absent = lexical ranking |
| Knowledge-page population | `bench/seed/postgres/knowledge_pages.sql` (4 pages), skipped by `BENCH_SEED_PAGES=0` | seeded by `make bench-up` |
| Catalog population | `make bench-seed-datahub`, empty baseline `bench-seed-datahub-empty` | seeded |
| API-catalog population | `bench/apisetup` registers a spec as a connection | none beyond the built-in |
| Prompt population | platform prompt store: `POST /api/v1/portal/prompts` or the `manage_prompt` tool | built-ins only |

Two floors are not removable and must be recorded rather than eliminated. The
platform seeds its own `platform-admin` API catalog from the OpenAPI document
embedded in the binary whenever an API-gateway toolkit and a catalog store are
both present (migration `000058_api_catalog_embedded_source`), and it registers
its own built-in prompts. So "empty" in this study means *no study-seeded
material*, and every cell's actual store counts are dumped into the archive.

**Not an arm: a server-side per-group result cap.** It does not exist. The
allocator gives each matching source a floor of one slot and caps any one
source at half the display budget during the balanced fill, then relaxes the
ceiling to spend leftover budget (`pkg/knowledge/allocator.go`), but none of
that is operator-configurable and there is no relevance threshold. If RQ1
confirms a population effect, a product ticket may propose one on this study's
evidence. It is never an arm here.

### 3.3 Instrument differences from the archives, disclosed

- The archived runs denied `fetch` to every persona (#1176). Current configs
  grant it, and `bench/config/config_test.go` now refuses a config that grants
  `search` without `fetch` and `list_connections`. New runs are therefore
  fetch-capable where the motivating re-analysis was not, which is one reason
  the comparison stays motivating-only.
- Every archived arm ran with lexical search ranking except a3. Every arm in
  this study runs with the same embedding provider configured, so ranking mode
  is constant across all cells.

## 4. Hypotheses, decision rules, and the separation analysis

Separation analysis is work item zero and gates everything downstream. For each
research question this section names the divergence mechanism, the design
variable that produces it, confirmation that the design varies that variable
rather than holding it at its easy setting, the pre-stated decision rule with
its minimum effect, and what falsifies the premise. An arm with no nameable
divergence mechanism is dropped here rather than carried into a run; section
4.2 exercises that rule.

**Minimum effects are stated in dollars per episode, not in significance.** A
contrast whose interval excludes zero but whose median difference is below its
stated threshold is reported as "measurable but not material", and that is a
finding, not a redesign trigger.

### 4.1 RQ1: does store population alone reproduce the per-turn payload growth?

**Divergence mechanism.** Federated search payload exists only when a store has
material to return. A populated store fills display-budget slots that an empty
one leaves empty, the filled slots enter the conversation prefix, and the
prefix is re-read on every subsequent turn. The mechanism is mechanical, not
behavioral: it does not depend on the agent choosing to do anything.

**Design variable.** Study-seeded store population, varied across three cells
under a **byte-identical config file**. Everything section 3.2 lists as a
config lever is held fixed, the embedding provider among them, so all three
cells rank `hybrid`.

**Confirmation the design varies it, and does not hold it easy.** The probe's
a2 cell sat below display-budget saturation (median 1 hit) and its a3 cell sat
at it (median 10). Two cells both above saturation would measure the ceiling of
section 2.2 rather than the effect. The three cells therefore straddle it:

| Cell | Knowledge pages | DataHub | API specs | Prompts | Expected regime |
| --- | --- | --- | --- | --- | --- |
| P0 empty | none (`BENCH_SEED_PAGES=0`) | `bench-seed-datahub-empty` | built-in only | built-in only | below saturation |
| P1 typical | the 4 seeded pages | `bench-seed-datahub` | one registered fixture spec | 10 seeded | near saturation |
| P2 saturating | 40 pages | `bench-seed-datahub` | one registered fixture spec | 40 seeded | at saturation |

P1 is the shipped bench fixture. P2 adds more of the same *kinds* of material
through the same seeding paths -- further `portal_knowledge_pages` inserts
beside `bench/seed/postgres/knowledge_pages.sql`, further prompts through the
prompt store -- and no new kind, so a P1-to-P2 difference can never be
attributed to a federated group appearing that was absent before.

Two constraints on the added material, both of which #1171 must satisfy before
the cell counts:

- **It must not answer the tasks.** A page that helps changes accuracy as well
  as cost, which would confound the cost measurement with the benefit this
  study is not re-litigating. Added pages and prompts are about subjects the
  task set does not touch.
- **It must be reachable by the bench identities.** A prompt created as
  personal is visible only to its owner
  (`pkg/knowledge/provider_prompts.go`), so a personal prompt would inflate the
  store count without reaching a single search result. Seeded prompts are
  shared, and the cell's first search result is inspected to confirm the group
  appears.

Store counts per cell are dumped into the archive before the first episode. An
RQ1 cell whose population is undocumented is unusable, and the run ticket
treats a missing dump as a failed arm.

**H1a (population moves per-turn cost below saturation).** Median per-episode
spend at P1 exceeds P0.

*Rule.* HOLDS if the paired median difference is at least **$0.005 per episode**
(roughly a third of the archived a0 baseline) and its bootstrap interval
excludes zero. FALSIFIED if the interval lies entirely below $0.005: population
would then be measurable, or not measurable at all, but not material. Either
outcome is a finding and neither reopens the design.

**H1b (the effect has a ceiling at budget saturation).** The P1-to-P2 spend
difference is materially smaller than the P0-to-P1 difference.

*Rule.* HOLDS if the P1-to-P2 median difference is under half the P0-to-P1
difference **and** the share of federated searches at the display budget rises
from P1 to P2. FALSIFIED if P1-to-P2 is at least as large as P0-to-P1, which
would mean per-hit size grows with population faster than the budget bounds it.

**H1c (the static prefix does not move).** The static-prefix bound is
indistinguishable across the three cells.

*Mechanism.* Config is identical and the tool list is identical, so the
published surface is identical. This is a **manipulation check on the design**,
not a finding: a moving static prefix means something other than population
changed between cells, and the arm is invalid.

*Rule.* The primary check is exact rather than statistical: #1171 dumps the
`tools/list` response for each cell before its first episode, and the arm is
INVALID if the three dumps are not byte-identical. The static-prefix bound is
reported beside them as a secondary read, with the caveat that it is a minimum
over episodes and so can move a little when the shortest episode in a cell
carries a differently sized first result; a bound differing by more than 15
percent between cells is investigated before the cell's numbers are used. This
check takes precedence over H1a and H1b.

**Premise falsifier.** If P0, P1, and P2 are indistinguishable in per-episode
spend while their store counts differ by the pinned amounts and their search
payloads differ, then on this platform population does not reach the bill, RQ1
publishes as a negative finding with a register row, and the epic's
deployment-relevant claim is retired.

### 4.2 RQ2: the enrichment boundary

**H2a (enrichment is cheap where it is not needed and pays where it binds)**,
tested as an a0-versus-a1 contrast at current code with per-component
decomposition.

*Mechanism.* Enrichment adds semantic context to `trino_describe_table` and
`trino_query` results, which enters the prefix and is re-read. The archives put
the payload effect at a doubling of the median describe result (1,074 to 2,601
chars) and the pooled end-to-end effect at +11 percent in median tokens and +19
percent in spend, with the trap suite going the other way: median tokens down
27 percent (73,534 to 53,467) and published trap accuracy up 14.6 points (42.7
to 57.3). Two opposed effects of that size cannot both be artifacts of the same
nuisance.

*Design variable.* The a0/a1 config pair, unchanged. Neither persona grants
`search`, so the #1176 delivery-surface correction does not touch them and the
contrast is the published one re-run on current code.

*Rule.* Two conditions, both on paired median spend differences. HOLDS if on
s1 (knowledge not needed) a1 costs no more than a0 by **$0.005 per episode**,
and on s3 (traps) a1 costs *less* than a0 by at least **$0.005 per episode**.
FALSIFIED if a1 costs more than a0 on s3 by at least $0.005: enrichment would
then be paying nothing back where it is supposed to bind. A result that meets
one condition and not the other is reported as the split it is, and the arm's
conclusion is stated per suite rather than pooled.

*Manipulation check.* s3 accuracy must reproduce the published direction (a1
above a0). If it does not, the cost result is reported without the benefit
claim and the divergence from the published report is stated.

**H2b (`column_context_filtering` reduces enrichment payload) — DROPPED as an
episode arm, with a survivor.**

The rule is that an arm with no nameable divergence mechanism at the fixture in
hand is dropped before it costs a run, and the drop is recorded. This is that
record.

The flag limits column-level enrichment to the columns a query references, and
its documented purpose is a **wide** table whose queries touch a subset. The
bench warehouse has no wide table: `memory.bench.customers` has five columns
and `memory.bench.orders` has six (`bench/seed/trino/setup.sql`). The most the
flag can remove on this fixture is the context for four columns of a six-column
table, a few hundred characters per describe call against a median episode of
tens of thousands of tokens. An episode-level arm here is a guaranteed null
that would say nothing about the mechanism, which is precisely the failure the
separation-analysis rule exists to prevent.

**The survivor is a payload measurement with no model in the loop.** The same
scripted tool sequence (`-llm scripted`, no API key, no spend) runs against two
configs differing only in the flag, and the difference in describe-result size
is reported as an exact bound on what the flag can save on this fixture. That
is a real number, it is cheap, and it is honest about its scope.

*Rule.* The measurement is reported whatever it shows. It is never generalized
past this fixture. The wide-table question is filed as a deferred study
extension in `findings-register.md`, with the fixture work it would need.

### 4.3 RQ3: what the discovery workflow costs

**H3a (the search-first gate has a measurable cost where discovery is not
needed).** On the s1 suite, per-episode spend is lower with
`workflow.require_search: false` than with the shipped default.

*Mechanism.* The gate refuses query tools until a discovery tool has been
called, so every episode pays for at least one search result in its prefix,
re-read on every later turn, whether or not the task needed discovery. With the
gate off the agent may skip search entirely.

*Design variable.* One config pair differing only in that flag, the same
single-deviation pattern as `bench/config/platform.bench.pk-gateoff.yaml`. The
suite is s1, whose tasks are answerable from the raw catalog, so the mechanism
has room to act.

*Rule.* HOLDS if the median spend difference is at least **$0.003 per episode**
with an interval excluding zero. FALSIFIED if gate-off is not cheaper.

*The live uncertainty, stated rather than assumed.* With the gate off the
search tool is still published and still described as the way to discover, so
agents may call it anyway. If they do, the gate's marginal cost is near zero
and that is the finding. The observed search-call rate per cell is reported
beside the spend difference in either case.

*Manipulation check.* s3 accuracy with the gate off must not fall materially.
A cost saving bought with accuracy is reported as such, never as a saving.

**H3b (persona scope sets the static prefix).** A narrower persona allow-list
lowers the static-prefix bound proportionally to the tool definitions it
withholds.

*Mechanism.* Persona filtering removes tools from the published list, and the
list is part of the prefix every request re-reads. The archives already show
the size of this effect across arms that differ in their tool lists (2,508
tokens at a1 against 5,127 at a2), though those arms differ in other ways too.

*Design variable.* One config, two personas, differing only in the allow-list.
The narrow persona keeps `search`, `fetch`, and `list_connections` together,
which `bench/config/config_test.go` enforces and which #1176 is the reason for.

*Rule.* HOLDS if the static-prefix bound differs between the two personas by at
least 15 percent, in the direction of the narrower list. FALSIFIED if it does
not move, which would mean the published tool list is not what dominates the
static prefix and would redirect the mitigation story to instructions instead.

This arm needs very few episodes: the static-prefix bound is a property of the
published surface, not of what an agent does. The bound is looser the fewer
episodes it is taken over, so both cells run the same tasks at the same k and
the comparison is between two equally loose bounds rather than between a tight
one and a loose one.

### 4.4 RQ4: the decomposition as a reusable instrument

**Claim.** The decomposition procedure -- separating cache creation from
per-turn cache read, bounding the static prefix inside the latter, and
attributing the remainder to tool results and their federated groups -- is a
general method for any MCP server that records per-attempt usage, not a
property of this platform.

*Deliverable.* The committed toolchain and the "Applying this to another
server" procedure in its README, both landing with this protocol.

*Rule.* HOLDS if the committed script reproduces the frozen probe
decomposition from the archives (already demonstrated: `decompose.py` table T7
compares every key and every value and `make bench-report-check` fails the
build otherwise) **and** the same functions run unmodified against #1171's
archives, changing only the family path. FALSIFIED if #1171's data needs a
different estimator, in which case the method claim narrows to this harness and
the report says so.

RQ4 is the only research question that can be settled without spending
anything, and half of it is settled by this PR.

## 5. Design, cells, and episode counts

### 5.1 Configs

New configs use a `ce-` prefix rather than extending the `a*` family. This is
not cosmetic: `bench/internal/pool/pool_test.go` globs `platform.bench.a*.yaml`
and requires exactly four matches, so a config named `platform.bench.a2-gateoff.yaml`
would break the identity-pool guard. Extending that guard to cover the `ce-*`
configs is a pre-run requirement on #1171, because the failure it prevents is a
paid run dying partway through on an identity nobody defined.

| Config | Derived from | Single deviation |
| --- | --- | --- |
| `platform.bench.ce-pop.yaml` | a2 | adds `memory.embedding` (Ollama, `nomic-embed-text`) so ranking is hybrid |
| `platform.bench.ce-gateoff.yaml` | `ce-pop` | `workflow.require_search: false` |
| `platform.bench.ce-narrow.yaml` | `ce-pop` | persona allow-list narrowed to the discovery surface plus `trino_*` |
| `platform.bench.ce-nocolfilter.yaml` | a1 | `enrichment.column_context_filtering: false` |

`make bench-up BENCH_ARM=ce-pop` resolves the config path by convention
(`BENCH_CONFIG ?= bench/config/platform.bench.$(BENCH_ARM).yaml`), so no
Makefile change is needed to run them.

### 5.2 The matrix

| RQ | Cells | s1 | s3 | Episodes per cell | Total | Driver |
| --- | --- | --- | --- | --- | --- | --- |
| RQ1 | P0, P1, P2 on `ce-pop` | k=3 (51) | k=1 (25) | 76 | 228 | raw API |
| RQ2 H2a | a0, a1 | k=3 (51) | k=3 (75) | 126 | 252 | raw API |
| RQ2 H2b | a1, `ce-nocolfilter` | scripted tool sequence | — | — | 0 metered | scripted |
| RQ3 H3a | `ce-pop`, `ce-gateoff` | k=3 (51) | k=1 (25) | 76 | 152 | raw API |
| RQ3 H3b | `ce-pop`, `ce-narrow` | k=1 (17) | — | 17 | 34 | raw API |

s1 is 17 tasks and s3 is 25 (`bench/tasks/`). The s2 suite is omitted
throughout: it is the largest at 45 tasks and adds no contrast any hypothesis
here needs.

**Why s3 runs at k=3 in one place and k=1 elsewhere.** On RQ2 the trap-suite
cost *is* the hypothesis, so it needs the same replication as s1. Everywhere
else s3 is only an accuracy manipulation check, and the published effect it
checks for is large (42.7 against 98.7 points). A k=1 check over 25 tasks is
coarse: it detects a collapse and would miss a few-point drift, and the report
states that limit rather than implying a sensitivity the design does not have.
This is also where most of the spend saving comes from — s3 costs three to five
times an s1 episode on every archived arm.

Every cell outside RQ1 runs at the P1 population, the shipped bench fixture, so
a gate or persona contrast is never also a population contrast.

### 5.3 Pairing

Every contrast is paired by task and attempt index: the same task set, the same
seed, the same k, so a per-task difference is a difference in the platform and
not in the task mix. The analysis in section 7 is on paired differences for
that reason.

## 6. Measurement specification

The instrument is `bench/reports/context-economics/decompose.py`. Definitions
below are the ones it implements; its README carries the full argument for each.

### 6.1 What is recorded per attempt

`input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_creation_tokens`,
`tool_calls`, `wall_ms`, and a transcript path. The `-llm anthropic` path
already records all of these
(verified in `bench/results/phase2-anthropic-k3/full-a0/results.json`). #1171
verifies them present and non-zero in a one-episode dry run before each metered
block, because a summed total is not recoverable into components after the
fact.

A missing counter counts as zero. This is not a formality: 18 of 261 attempts
in `full-a0` carry no `cache_creation_tokens` key, and excluding them instead
moves that arm's median cache creation from 1,683 to 1,898.

### 6.2 The static-prefix bound, and why it is a bound

`cache_read` on an attempt is the sum, over that episode's model requests, of
the prefix each request re-read. That reading depends on uncached input being
negligible, which holds throughout these archives: `input_tokens` has a median
of 12 to 18 and a maximum of 52 across all four arms, so essentially the whole
prefix is served from cache every turn.

Dividing by the request count gives the average prefix that episode re-read.
The smallest such value in a cell is the tightest **upper bound** on the
static prefix, the system prompt and tool definitions every request carries
before any conversation accumulates: no episode can average below the static
prefix, because every request's prefix contains it.

It is reported as a bound and never as an estimate. #1171 checks
`input_tokens` per cell before using it, and a cell where uncached input is not
negligible reports the bound with that caveat attached.

### 6.3 Denominators

Per-turn context is reported under both denominators, because they are not the
same number: an assistant turn can carry several tool calls, and on the
archives the two counts differ on more than half the attempts in every arm. The
frozen probe divided by tool calls; the request count is the one that
corresponds to what was billed. Any cost claim names which it used.

### 6.4 Payload attribution

Every tool result is mapped back to its call through the call id, and result
sizes are summed per tool. Search results are decomposed further by federated
group, counting the three response shapes apart: the federated envelope, the
paged browse envelope, and tool errors. Folding an error into "returned
nothing" would overstate how often a populated store had nothing to say.

### 6.5 Pricing

Each counter is multiplied by its own rate, at rates dated in the script.
Ratios are reported in dollars with the token ratio beside them.

### 6.6 Accuracy

Accuracy is graded by the existing deterministic graders and is a
**manipulation check only**. It references the published knowledge-layer
results and never appears as a headline of this study. Where a cost saving
coincides with an accuracy drop, both are reported together.

## 7. Analysis plan

Per contrast: the paired per-task median difference in per-episode spend, with
a 95 percent percentile-bootstrap interval from the shared
`bench/internal/stats` package at a fixed seed, compared against the arm's
pre-stated minimum effect. The components are reported beside the total --
median cache read, median cache creation, median output, and the cell's
static-prefix bound -- so a total that fails to move because two components
moved against each other is visible rather than read as "no effect".

No p-values, no significance thresholds. An interval and a minimum effect are
what a cost decision needs.

Every table in the report is produced by `decompose.py` from the committed
archives, offline. Numbers are not transcribed by hand into prose: the pin
list in the script is extended to cover every figure the report states, exactly
as it already covers this protocol's section 2.

## 8. Confounds and threats to validity

**Ranking mode.** The confound section 2.1 identifies in the archives. Closed
by configuring the same embedding provider in every cell and verified by the
recorded `ranking` field, which must read `hybrid` on the overwhelming majority
of federated searches in every cell. A cell that ranks lexical is invalid.

**Cross-episode cache state.** Attempts in a run share a cached prefix, so
`cache_creation` depends on run order and cache TTL and is not a per-episode
property. It is reported, never used as the basis of a claim about surface
size. The static-prefix bound is the quantity used for that.

**Store drift within a cell.** A run that writes to the stores it is measuring
changes its own population mid-block. The RQ1 cells use personas without
capture or apply tools, so no episode writes to the knowledge stores, and store
counts are dumped again after the block; a post-count that differs from the
pre-count invalidates the cell.

**Population is not one dimension.** Pages, prompts, catalog entities, and API
endpoints federate through different providers with different per-hit sizes.
P2 multiplies only pages and prompts, so the study measures depth of population
and not breadth. A breadth claim would need a cell that adds a federated group
the other cells lack, which is a different design and is out of scope here.

**Task mix.** s1 and s3 are what this study measures; s2 is omitted. Costs are
reported per suite as well as pooled, so a pooled figure is never read as a
property of an unmeasured task distribution.

**Model.** One model, Sonnet 5, on the raw-API path. Absolute dollars are
properties of that model's rates. Component *ratios* are more portable than
absolute figures, and the report says which is which.

**The archives cannot be compared to the new runs.** Different build, different
delivery surface (#1176), different ranking mode. Section 2 is motivating
evidence; no result in this study is stated as a change from it.

## 9. Generalization and external validity

Travels: the decomposition method, the finding that static surface is billed
per turn and that cache creation does not measure it, the existence of a
saturation ceiling in a budgeted federated search, and the gap between token
ratios and cost ratios.

Does not travel: absolute dollars, the specific saturation point (a property of
the display budget and this fixture's hit sizes), the enrichment payoff on the
trap suite (a property of these tasks), and anything about wide-table column
filtering, which this fixture cannot reach.

## 10. Client surface and driver policy

**Every token-fidelity arm runs on the raw-API in-process path**
(`benchrun -llm anthropic`, default model `claude-sonnet-5`). The claude-cli
adapter inserts client-side context the harness does not control, which
confounds exactly the totals this study measures, so it is never used for a
cost claim. If an arm needs the client surface for some other reason it is
labelled and excluded from every cost table.

Build provenance is decided before the first arm, not after: either a tag-build
worktree (the knowledge-use v1.116.0 precedent) or commit pinning with
per-block provenance recorded in every manifest (the knowledge-pollution
precedent). If commit-pinned, main is not merged into the run branch mid-block.

## 11. Spend plan

Every figure below is an estimate: episodes multiplied by the archived
per-suite **mean** spend of the closest arm. The mean, not the median, because
a budget needs the mean and these distributions are heavy-tailed enough that
the median would understate a block by roughly half. The rates behind them live
in the toolchain, dated.

**#1171 may not run an arm whose cap box below is unchecked.** The boxes are
checked by the user on issue #1170, not here.

| Arm | Episodes | Closest archived arm | Estimate | Cap | Approved |
| --- | --- | --- | --- | --- | --- |
| RQ1 P0 | 76 | a2 (sparse stores, hybrid ranking) | $2.50 | $4 | [ ] |
| RQ1 P1 | 76 | a3 (populated stores) | $4.41 | $7 | [ ] |
| RQ1 P2 | 76 | a3 | $4.41 | $7 | [ ] |
| RQ2 H2a a0 | 126 | a0 | $7.18 | $10 | [ ] |
| RQ2 H2a a1 | 126 | a1 | $7.01 | $10 | [ ] |
| RQ2 H2b | 0 metered | scripted, no model | $0 | n/a | n/a |
| RQ3 H3a `ce-pop` | 76 | a3 | $4.41 | $7 | [ ] |
| RQ3 H3a `ce-gateoff` | 76 | a3 (an upper bound; the arm should be cheaper) | $4.41 | $7 | [ ] |
| RQ3 H3b, both cells | 34 | a3, s1 only | $1.67 | $3 | [ ] |
| **Total** | **666** | | **$36** | **$55** | |

For scale: the four archived arms cost $9.23, $9.49, $9.45, and $16.64 for 261
episodes each, so this whole matrix is roughly the cost of one archived arm and
a half.

A cell that exhausts its cap stops and reports what it has. Partial archives
are kept, never discarded or overwritten, and the family README records where
the block stopped.

## 12. Platform decisions this study settles

- Whether store population reaches the bill, and whether it keeps doing so past
  display-budget saturation. If it does and the ceiling is real, the display
  budget is already the control and no new knob is needed; if it does and there
  is no ceiling, a per-group cap or relevance threshold becomes a justified
  product proposal with evidence behind it.
- Whether the search-first gate costs anything where discovery is unnecessary,
  which is the evidence a persona-scoped or task-scoped gate would need.
- Whether persona scope is a real cost lever, which is what a deployment can act
  on today without any code change.
- Whether `enrichment.column_context_filtering` can matter at all on a narrow
  fixture, which decides whether a wide-table study extension is worth filing.

## 13. Reproducibility, artifacts, and publication commitment

- Protocol: this document, pre-registered before #1171's data.
- Toolchain: `bench/reports/context-economics/`, stdlib python3, offline,
  exercised by `make bench-report-check` in `make verify` and in CI.
- Run data: `bench/results/context-economics/<family>/`, each family with a
  README stating what it does and does not establish, manifests carrying build
  pin, config file, model, k, and the store-count dump for RQ1 cells.
- Report: `docs/reference/benchmark-report-context-economics.md` with its own
  concept DOI (#1172). It revises no published report.
- Findings register: one row per finding including every null against a
  pre-stated threshold and every arm dropped by rule, section 4.2 among them.
- The published report states negative and null results with the same
  prominence as positive ones. A cost study that only reported the costs it
  found would be an advertisement.

## 14. Work plan

1. This PR (#1170): protocol, decomposition toolchain, `bench-report-check`
   wiring, series-table column. No runs.
2. User sign-off on the separation analysis and the spend caps, as a comment on
   #1170. The status banner changes on sign-off.
3. #1171: the configs in section 5.1, the pool-guard extension, the arms in
   section 5.2, archives and register rows. No report claims.
4. #1172: report page, DOI, register backfill.
