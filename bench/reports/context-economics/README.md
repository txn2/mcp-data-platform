# Context-economics toolchain

The recompute for the context-economics study (epic [#1164](https://github.com/txn2/mcp-data-platform/issues/1164)).
It decomposes what one agent episode costs against an MCP platform, and where
the cost goes: how much context each turn re-reads, how much of that is the
static surface the server publishes before any work happens, and how much
arrives as tool-result payload.

```bash
python3 bench/reports/context-economics/decompose.py          # every table, then the pins
python3 bench/reports/context-economics/decompose.py --emit   # the decomposition as JSON
```

Stdlib python3, offline, no API key, no network — the same contract
[`knowledge-pollution/pollution_tables.py`](../knowledge-pollution/pollution_tables.py)
and [`knowledge-use/pk_tables.py`](../knowledge-use/pk_tables.py) honor. `make
bench-report-check` runs it, in `make verify` and in CI's harness job.

## What it reads, and whose data it is

Today the only archives it reads belong to another study. The four arms of
`bench/results/phase2-anthropic-k3/` are the knowledge-layer study's phase-2
matrix, published at
[`docs/reference/benchmark-report.md`](../../../docs/reference/benchmark-report.md),
DOI [10.5281/zenodo.21438044](https://doi.org/10.5281/zenodo.21438044). They
are read in place, never copied or modified, under the cross-study re-analysis
convention in [`bench/README.md`](../../README.md); the provenance table is
[`bench/results/context-economics/probe/README.md`](../../results/context-economics/probe/README.md).

Re-analysis motivates and never confirms. Every number this script prints today
describes a build from 2026-07-14 (`v1.102.0-9-gadfb9d90-dirty`) under an
instrument defect since fixed ([#1176](https://github.com/txn2/mcp-data-platform/issues/1176):
every arm persona denied `fetch`, so every body an agent read had to arrive
inside a search result). The study's own runs land with
[#1171](https://github.com/txn2/mcp-data-platform/issues/1171), and this script
reads them through the same functions — `results.json` and the transcript
schema are the harness's, not the archive's.

## What it computes

| Table | Content |
| --- | --- |
| T1 | Token components per attempt: total, cache creation, cache read, input, output, tool calls. |
| T2 | Median total tokens by suite, so a single hard suite is visible rather than folded into one median. |
| T2b | Mean tool calls per episode by suite: the work an arm needed, beside what it paid. |
| T3 | Per-turn context under both denominators, and the static-prefix bound. |
| T4 | Tool-call counts and median result size per tool. |
| T5 | Search payload by federated group, and the shape of the result set. |
| T6 | Per-episode spend: median, mean, arm total, and mean by suite. |
| T7 | The frozen probe decomposition, reproduced from the archives, every key and every value. |
| T8 | The numbers the probe `SUMMARY.md` states in prose that these archives can produce. |
| T9 | The numbers the protocol quotes from the archives, including every one this toolchain derived rather than inherited. |

### The definitions that are not obvious

**A missing token counter is zero.** 18 of 261 attempts in `full-a0` and
`full-a1`, 2 in `full-a2`, and 1 in `full-a3` carry no `cache_creation_tokens`
key. Excluding them instead moves a0's median cache creation from 1,683 to
1,898 and its median total from 23,696 to 24,288.

**`median_ctx_per_turn` divides by tool calls.** That is the frozen probe's
definition and T7 reproduces it. T3 reports the same quantity divided by
assistant turns beside it, because the two are not the same number: an
assistant turn can carry several tool calls, and on these archives the counts
differ on more than half the attempts in every arm. A cost claim should name
which denominator it used.

**The static-prefix bound is a bound, not an estimate.** `cache_read` on an
attempt is the sum over that episode's requests of the prefix each request
re-read, because `input_tokens` is negligible throughout (median 12–18, maximum
52 across all four arms) — essentially the whole prefix is served from cache
every turn. Dividing by the request count gives the average prefix that episode
re-read; the smallest such value in an arm is the tightest upper bound on the
static prefix, the system prompt and tool definitions every request carries
before any conversation accumulates. No attempt can average below it.

**Cache creation understates the static surface.** It counts tokens written to
cache, and a prefix shared by hundreds of attempts in one run is written once
and read thereafter, so a large static surface shows up as a large `cache_read`
and a small `cache_creation`. The bound above is what makes the static
component visible.

**A search call answers in two shapes.** The federated envelope groups hits from
every reachable source across a display budget (default 10; `pkg/knowledge/router.go`).
Passing exactly one source with no intent enumerates that source instead, and
the answer is a paged envelope the display budget never touches, at five times
the page size. T5 counts the two apart, and counts tool errors as neither —
folding an error into "returned nothing" would overstate how often a populated
store had nothing to say.

**Dollars, not tokens.** Cache read bills at a tenth of input and output at five
times it, so an arm that multiplies tokens mostly through cache read multiplies
spend by much less. On these archives the a0→a3 token ratio is 6.2x and the
spend ratio is 3.4x. Every cost claim in this study is stated in dollars for
that reason. Rates are pinned in the script with the date they were in effect.

Spend is computed per attempt and then summarized, never as a sum of component
medians: the median of sums is not the sum of medians. Both the median and the
mean are reported, because they answer different questions and differ by a
factor of two on some arms — a typical-episode claim needs the median, and a
budget estimate needs the mean, since a block's total is n times the mean.

## Applying this to another server

The decomposition is not specific to this platform. It needs one thing from a
harness: **per-attempt usage, recorded separately per counter.** Given that,
the procedure below runs against any MCP server.

1. **Record per attempt**: `input_tokens`, `output_tokens`, `cache_read_tokens`,
   `cache_creation_tokens`, the tool-call count, and a transcript of
   `(role, text | tool_calls | tool_results)` with a call id linking each result
   to its call. A summed "total tokens" is not enough — the four counters bill
   at four different rates, and the whole method is separating them.
2. **Hold the arm's configuration fixed and vary one thing.** Everything below
   is a difference between two arms; nothing is interpretable from one.
3. **Compute the per-turn context** as `cache_read / requests`. State the
   denominator. This is the number multiplied by episode length, and it is
   usually the largest line in the bill.
4. **Bound the static prefix** as the minimum per-attempt `cache_read /
   requests` in the arm. The gap between that bound and the arm's median
   per-turn context is what the conversation accumulated; the bound itself is
   what the server charged for existing.
5. **Attribute the accumulated part** by mapping every tool result back to its
   tool through the call id, then summing result sizes per tool. Where one tool
   returns a structured envelope (a federated search, a paged listing),
   decompose it further by the field that names its origin — here, the group
   source.
6. **Price it.** Multiply each counter by its own rate, at rates dated in the
   script, and report the ratio in dollars beside the ratio in tokens.
7. **Pin what you publish.** Every number a committed page states goes in a pin
   list the recompute checks, and the recompute runs in the build. A report
   toolchain no gate exercises is the defect
   [#1168](https://github.com/txn2/mcp-data-platform/issues/1168) fixed.

Step 4 is the one that needs the caveat repeated: it is a bound only while
uncached input is negligible. Check `input_tokens` before trusting it. If a
harness does not cache prompts at all, the same decomposition still works with
`input_tokens` in place of `cache_read_tokens`, and the static prefix is then
billed at full rate rather than a tenth.

## The gate

`make bench-report-check` runs this script and fails the build on any pin
mismatch. Three artifacts are pinned: the frozen `decomposition.json` (compared
whole, every key and every value), the numbers the probe `SUMMARY.md` states in
prose, and the numbers the protocol
[`bench/docs/context-economics-study-design.md`](../../docs/context-economics-study-design.md)
quotes. So a drift between the archives, this script, and a committed page
fails in CI instead of reaching a reader.

The frozen file is compared as canonical JSON — the same bytes for the same
values, with key sequence normalized on both sides. Its own key order is the
directory-iteration order of the uncommitted script that produced it on
2026-08-01, which varies by filesystem and which no portable recompute
reproduces.
