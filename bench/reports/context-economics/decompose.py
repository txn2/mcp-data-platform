#!/usr/bin/env python3
"""Decompose the per-episode context cost of an MCP platform arm from its run archive.

Reads run archives in place and prints the context-economics decomposition:
token components per arm, per-turn context, the static prefix bound, tool-call
and result-size distributions, search payload by federated group, and the same
components priced in USD. No network, no API key, stdlib only -- the contract
every report toolchain in this series honors (pollution_tables.py, pk_tables.py).

    python3 bench/reports/context-economics/decompose.py          # tables + pins
    python3 bench/reports/context-economics/decompose.py --emit   # the decomposition as JSON

The archives read here belong to the knowledge-layer study
(docs/reference/benchmark-report.md, DOI 10.5281/zenodo.21438044) and are read
in place under the cross-study re-analysis convention in bench/README.md. The
provenance table is bench/results/context-economics/probe/README.md.

The final section is the gate: every number this script computes that a
committed artifact also states -- the frozen decomposition.json, the frozen
probe SUMMARY.md, and the protocol bench/docs/context-economics-study-design.md
-- is recomputed here and compared. `make bench-report-check` runs it, so a
drift between the archives, this script, and a committed page fails the build
instead of reaching a reader. Exits nonzero on any mismatch.

The procedure is documented for reuse against any MCP server that records
per-attempt usage in README.md, section "Applying this to another server".
"""
import glob
import json
import os
import statistics
import sys

_d = os.path.dirname(os.path.abspath(__file__))
while os.path.basename(_d) != "bench" and os.path.dirname(_d) != _d:
    _d = os.path.dirname(_d)
if os.path.basename(_d) != "bench":
    sys.exit("decompose.py must live under bench/; it resolves the archives relative to that directory")
BENCH = _d
RESULTS = os.path.join(BENCH, "results")

# The four archived arms of the knowledge-layer study's phase-2 matrix, in
# increasing platform surface: raw toolkits, + semantic enrichment, + the
# discovery layer, + the lifecycle tools. Verified deltas are in the protocol,
# section 3.
ARMS = ("a0", "a1", "a2", "a3")
FAMILY = os.path.join(RESULTS, "phase2-anthropic-k3")
FROZEN = os.path.join(RESULTS, "context-economics", "probe", "decomposition.json")

# USD per million tokens for the model every archived arm ran on, at the rates
# in effect on the recompute date (2026-08-07; Sonnet 5 introductory pricing
# through 2026-08-31). Same table and same convention as
# reports/knowledge-pollution/pollution_tables.py: cache read is 0.1x input,
# 5-minute cache write is 1.25x input.
PRICE_IN, PRICE_OUT = 2.00, 10.00
CACHE_READ_MULT, CACHE_WRITE_MULT = 0.1, 1.25

# The platform's search display budget: hits allocated across all federated
# sources for one search (pkg/knowledge/router.go, defaultLimit and maxLimit).
# A search that returns as many hits as its budget allowed was bound by the
# budget rather than by what the stores hold, which is the distinction T5
# reports. A caller may raise it per call, so the effective budget is resolved
# from the call's own arguments (display_budget).
DISPLAY_BUDGET = 10
MAX_DISPLAY_BUDGET = 50

TOKEN_FIELDS = ("input_tokens", "output_tokens", "cache_read_tokens", "cache_creation_tokens")


_CACHE = {}


def load(arm):
    """Return (attempts, transcripts) for one archived arm, read once.

    Transcripts are returned in directory-iteration order rather than sorted,
    because the frozen decomposition.json carries the insertion order of that
    iteration in its per-tool and per-source maps. Nothing downstream depends
    on the order for a value, only for key sequence, and the frozen-file
    comparison is order-insensitive (see check_frozen)."""
    if arm in _CACHE:
        return _CACHE[arm]
    base = os.path.join(FAMILY, f"full-{arm}")
    with open(os.path.join(base, "results.json")) as f:
        attempts = json.load(f)["attempts"]
    transcripts = []
    for p in glob.glob(os.path.join(base, "transcripts", "*.json")):
        with open(p) as f:
            transcripts.append(json.load(f))
    _CACHE[arm] = (attempts, transcripts)
    return _CACHE[arm]


def total_tokens(a):
    """Billable tokens for one attempt: every counter the API reports.

    A missing counter is zero. That is not a formality: 18 of 261 attempts in
    full-a0 and full-a1, 2 in full-a2, and 1 in full-a3 carry no
    cache_creation_tokens key at all, and excluding them instead of coercing
    them moves a0's median cache creation from 1,683 to 1,898."""
    return sum(a.get(f, 0) or 0 for f in TOKEN_FIELDS)


def median(values):
    return statistics.median(values) if values else 0


def median_int(values):
    """Median truncated to a whole number.

    Result sizes are counted in characters and reported as integers, so an
    even-length sample whose two middle values differ by an odd amount is
    truncated rather than left at a half character. This is also what
    reproduces the frozen probe file: median_low would round the other way on
    five of its entries."""
    return int(median(values))


def iter_results(t):
    """Yield (tool_name, result_text) for every tool result in one transcript."""
    for name, _, text in iter_calls(t):
        yield name, text


def iter_calls(t):
    """Yield (tool_name, call_args, result_text) for every answered tool call.

    The args travel with the result because a search result cannot be read
    without them: the caller's `limit` sets the display budget the platform
    allocated, so whether a result set was bound by the budget or by the stores
    is only answerable against the call that produced it."""
    names, args = {}, {}
    for turn in t["transcript"]:
        for tc in turn.get("tool_calls") or []:
            names[tc["id"]] = tc["name"]
            args[tc["id"]] = tc.get("args") or {}
    for turn in t["transcript"]:
        for tr in turn.get("tool_results") or []:
            call_id = tr.get("call_id")
            name = names.get(call_id)
            if name:
                yield name, args.get(call_id, {}), (tr.get("text") or "")


def display_budget(args):
    """The display budget the platform allocated for one search call.

    Mirrors clampInt in pkg/knowledge/router.go: a limit at or below zero (or
    absent) takes the default, anything above the maximum is clamped to it, and
    anything between is honored as given."""
    limit = args.get("limit")
    if not isinstance(limit, int) or limit <= 0:
        return DISPLAY_BUDGET
    return min(limit, MAX_DISPLAY_BUDGET)


def assistant_turns(t):
    """Model requests that produced content: the denominator for per-request cost.

    Distinct from the tool-call count, which the frozen file uses. One assistant
    turn can carry several tool calls, so the two diverge; on these archives the
    counts differ on more than half the attempts in every arm."""
    return sum(1 for turn in t["transcript"] if turn["role"] == "assistant")


def search_shape_of(text):
    """Classify one search result payload: "federated", "browse", or "error".

    The search tool answers in two shapes. The usual one is the federated
    envelope: groups of hits from every source the caller can reach, allocated
    across a display budget. Passing exactly one source with no intent instead
    enumerates that source, and the answer is a paged envelope (source, total,
    offset, limit, items) that the display budget never touches -- its page
    size is the browse limit, five times larger.

    Anything else is an error: a rejected argument type, an unbrowsable source,
    an exhausted call budget. The three are counted apart because they cost
    differently and because folding errors into "returned nothing" would
    overstate how often a populated store had nothing to say."""
    try:
        payload = json.loads(text)
    except (ValueError, TypeError):
        return "error", None
    if not isinstance(payload, dict):
        return "error", None
    if "groups" in payload:
        return "federated", payload
    if "items" in payload:
        return "browse", payload
    return "error", None


def search_groups(text):
    """Yield (source, hits) for every group in one federated search payload."""
    kind, payload = search_shape_of(text)
    if kind != "federated":
        return
    for group in payload.get("groups") or []:
        yield group.get("source"), (group.get("hits") or [])


def decompose(arm):
    """The per-arm decomposition, in the schema of the frozen probe file.

    Token components come from results.json (what the API billed); call counts,
    result sizes, and federated-group payload come from the transcripts (what
    the platform actually returned). Keeping the two sources separate is the
    point of the method: the bill and the payload are independently recorded,
    so an explanation that links them can be checked rather than assumed."""
    attempts, transcripts = load(arm)
    totals = [total_tokens(a) for a in attempts]
    suites = sorted({a["suite"] for a in attempts})

    calls, chars, hits, hit_chars, searches = {}, {}, {}, {}, 0
    for t in transcripts:
        for turn in t["transcript"]:
            for tc in turn.get("tool_calls") or []:
                calls[tc["name"]] = calls.get(tc["name"], 0) + 1
        for name, text in iter_results(t):
            chars.setdefault(name, []).append(len(text))
            if name != "search":
                continue
            searches += 1
            for source, group_hits in search_groups(text):
                hits[source] = hits.get(source, 0) + len(group_hits)
                hit_chars[source] = hit_chars.get(source, 0) + sum(
                    len(json.dumps(h)) for h in group_hits
                )

    return {
        "n": len(attempts),
        "median_total_tokens": median(totals),
        "mean_total_tokens": round(statistics.mean(totals)),
        "per_suite_median_tokens": {
            s: median([total_tokens(a) for a in attempts if a["suite"] == s]) for s in suites
        },
        "median_cache_creation": median([a.get("cache_creation_tokens", 0) for a in attempts]),
        "median_cache_read": median([a.get("cache_read_tokens", 0) for a in attempts]),
        "median_input": median([a.get("input_tokens", 0) for a in attempts]),
        "median_output": median([a.get("output_tokens", 0) for a in attempts]),
        "median_tool_calls": median([a["tool_calls"] for a in attempts]),
        "median_ctx_per_turn": median(
            [a.get("cache_read_tokens", 0) / a["tool_calls"] for a in attempts if a["tool_calls"]]
        ),
        "tool_calls_by_name": dict(sorted(calls.items(), key=lambda kv: -kv[1])),
        "median_result_chars_by_tool": {k: median_int(v) for k, v in chars.items()},
        "total_result_chars_by_tool": {k: sum(v) for k, v in chars.items()},
        "search_results": searches,
        "search_hits_by_source": hits,
        "search_chars_by_source": hit_chars,
    }


def per_request(arm):
    """Per-request context and the static-prefix bound for one arm.

    cache_read on an attempt is the sum over that episode's requests of the
    prefix each request re-read, because input_tokens is negligible on every
    attempt in these archives (median 12-18, maximum 52 across all four arms):
    essentially the whole prefix is served from cache on every turn. Dividing
    by the request count therefore gives the average prefix that turn re-read.

    The smallest such value in an arm is the tightest upper bound on the arm's
    STATIC prefix -- the system prompt, platform instructions, and tool
    definitions that every request carries before any conversation accumulates.
    It is an upper bound, not an estimate: every request's prefix is at least
    the static prefix, so no attempt can average below it."""
    attempts, transcripts = load(arm)
    by_key = {(a["task_id"], a["attempt"]): a for a in attempts}
    per = []
    for t in transcripts:
        a = by_key[(t["task_id"], t["attempt"])]
        turns = assistant_turns(t)
        if turns:
            per.append(a.get("cache_read_tokens", 0) / turns)
    return {
        "median_ctx_per_request": median(per),
        "static_prefix_bound": min(per) if per else 0,
    }


def search_shape(arm):
    """Hits per search and how often the display budget bound the result.

    The platform allocates a display budget across sources (default 10, caller
    settable up to 50; pkg/knowledge/router.go). Whether an arm's searches
    reach that budget is what separates "the stores had nothing to return" from
    "the stores saturated the response", and the two have different cost
    curves: below saturation payload tracks population, at saturation it stops
    growing with population and tracks per-hit size instead."""
    _, transcripts = load(arm)
    counts, browse, errors, at_budget = [], 0, 0, 0
    ranking, matched = {}, {}
    for t in transcripts:
        for name, args, text in iter_calls(t):
            if name != "search":
                continue
            kind, payload = search_shape_of(text)
            if kind == "browse":
                browse += 1
                continue
            if kind == "error":
                errors += 1
                continue
            hits = sum(len(h) for _, h in search_groups(text))
            counts.append(hits)
            at_budget += 1 if hits >= display_budget(args) else 0
            mode = payload.get("ranking")
            ranking[mode] = ranking.get(mode, 0) + 1
            for cov in payload.get("coverage") or []:
                src = cov.get("source")
                matched[src] = matched.get(src, 0) + (cov.get("matched") or 0)
    return {
        "searches": len(counts) + browse + errors,
        "federated": len(counts),
        "browse": browse,
        "errors": errors,
        "median_hits": median(counts),
        "empty": sum(1 for c in counts if c == 0),
        "at_budget": at_budget,
        "ranking": dict(sorted(ranking.items(), key=lambda kv: -kv[1])),
        "matched_by_source": dict(sorted(matched.items(), key=lambda kv: -kv[1])),
    }


def attempt_usd(a):
    """What one attempt cost, at the pinned rates."""
    return (
        a.get("input_tokens", 0) * PRICE_IN
        + a.get("output_tokens", 0) * PRICE_OUT
        + (a.get("cache_read_tokens", 0) or 0) * PRICE_IN * CACHE_READ_MULT
        + (a.get("cache_creation_tokens", 0) or 0) * PRICE_IN * CACHE_WRITE_MULT
    ) / 1e6


def cost_usd(arm):
    """Per-episode spend for one arm, at the pinned rates.

    Reported because a token ratio is not a cost ratio: cache read bills at a
    tenth of input and output bills at five times it, so an arm that multiplies
    tokens mostly through cache read multiplies dollars by much less. Any cost
    claim this study makes is stated in dollars for that reason.

    Spend is computed per attempt and then summarized, never as a sum of
    component medians: the median of sums is not the sum of medians, and the
    difference is large enough here to move a headline ratio. The mean is
    reported beside the median because a budget estimate needs the mean (total
    = n x mean) while a typical-episode claim needs the median, and the
    distribution is heavy-tailed enough that the two differ by a factor of two
    on some arms.

    Both are also reported per suite. Pooling hides the effect this study exists
    to find: on these archives the cheap-surface arm is the expensive one
    wherever the tasks need knowledge, and a pooled median averages that
    reversal away."""
    attempts, _ = load(arm)
    per = [attempt_usd(a) for a in attempts]
    by_suite = {}
    for suite in sorted({a["suite"] for a in attempts}):
        sub = [attempt_usd(a) for a in attempts if a["suite"] == suite]
        by_suite[suite] = {"median": median(sub), "mean": statistics.mean(sub)}
    return {
        "median": median(per),
        "mean": statistics.mean(per),
        "total": sum(per),
        "by_suite": by_suite,
    }


def workload(arm):
    """Mean tool calls per episode, by suite.

    The work an arm needed, beside what it paid. A surface that costs more per
    turn can still cost less per episode by removing turns, and the two suites
    here move in opposite directions, so a pooled tool-call figure would hide
    exactly the effect worth reporting."""
    attempts, _ = load(arm)
    return {
        suite: statistics.mean([a["tool_calls"] for a in attempts if a["suite"] == suite])
        for suite in sorted({a["suite"] for a in attempts})
    }


def canonical(obj):
    """Canonical serialization: the same bytes for the same values, whatever
    order the keys were inserted in."""
    return json.dumps(obj, indent=2, sort_keys=True)


def section(title):
    print(f"\n=== {title} ===")


def table_tokens(dec):
    section("T1: token components per attempt (median; mean total in parentheses)")
    print("  arm   total            cache_creation  cache_read  input  output  tool_calls")
    for arm in ARMS:
        d = dec[arm]
        print(f"  {arm}    {d['median_total_tokens']:>7,} ({d['mean_total_tokens']:>7,})"
              f"  {d['median_cache_creation']:>14,}  {d['median_cache_read']:>10,}"
              f"  {d['median_input']:>5}  {d['median_output']:>6,}  {d['median_tool_calls']:>10}")

    section("T2: median total tokens by suite")
    suites = sorted(dec["a0"]["per_suite_median_tokens"])
    print("  arm   " + "  ".join(f"{s:>8}" for s in suites))
    for arm in ARMS:
        row = dec[arm]["per_suite_median_tokens"]
        print(f"  {arm}    " + "  ".join(f"{row[s]:>8,}" for s in suites))


def table_per_turn(req):
    section("T3: per-turn context, and how much of it is static")
    print("  (ctx/tool-call is the frozen probe's definition; ctx/request divides by model")
    print("   requests instead, and the static bound is the smallest per-request average in the arm)")
    print("  arm   ctx/tool-call  ctx/request  static prefix bound")
    for arm in ARMS:
        r = req[arm]
        print(f"  {arm}    {r['ctx_per_tool_call']:>13,.1f}  {r['median_ctx_per_request']:>11,.1f}"
              f"  {r['static_prefix_bound']:>19,.0f}")


def table_payload(dec, shape):
    section("T4: tool calls and result size (top five tools by call count)")
    for arm in ARMS:
        d = dec[arm]
        top = list(d["tool_calls_by_name"].items())[:5]
        parts = [f"{n} {c}x/{d['median_result_chars_by_tool'].get(n, 0):,.0f}ch" for n, c in top]
        print(f"  {arm}: " + "  ".join(parts))

    section("T5: search payload by federated group (hits / chars over the arm)")
    for arm in ARMS:
        d, s = dec[arm], shape[arm]
        if not d["search_results"]:
            print(f"  {arm}: no search tool on this arm")
            continue
        groups = sorted(d["search_hits_by_source"].items(), key=lambda kv: -kv[1])
        parts = [f"{src} {n:,}/{d['search_chars_by_source'][src]:,}ch" for src, n in groups]
        print(f"  {arm}: {d['search_results']:,} search calls = {s['federated']:,} federated"
              f" + {s['browse']} browse + {s['errors']} error; median result"
              f" {d['median_result_chars_by_tool']['search']:,.0f} chars")
        print(f"       federated: median hits {s['median_hits']:.0f},"
              f" {s['empty']:,} returned nothing, {s['at_budget']:,} at the display budget;"
              f" ranking " + ", ".join(f"{m} {n:,}" for m, n in s["ranking"].items()))
        print("       shown:   " + "  ".join(parts))
        print("       matched: " + "  ".join(
            f"{src} {n:,}" for src, n in s["matched_by_source"].items()))


def table_workload(work):
    section("T2b: mean tool calls per episode, by suite")
    suites = sorted(work["a0"])
    print("  arm   " + "  ".join(f"{s:>8}" for s in suites))
    for arm in ARMS:
        print(f"  {arm}    " + "  ".join(f"{work[arm][s]:>8.2f}" for s in suites))


def table_cost(cost):
    section("T6: per-episode spend, USD at Sonnet 5 rates (2026-08-07)")
    print("  (input $2.00/MTok, output $10.00/MTok, cache read 0.1x input, cache write 1.25x input)")
    suites = sorted(cost["a0"]["by_suite"])
    print("  arm    median     mean   xa0(median)   arm total   "
          + "  ".join(f"{s} median/mean" for s in suites))
    base = cost["a0"]["median"]
    for arm in ARMS:
        c = cost[arm]
        print(f"  {arm}   ${c['median']:.5f}  ${c['mean']:.5f}   {c['median'] / base:>5.2f}x"
              f"      ${c['total']:>7.2f}   "
              + "  ".join(f"${c['by_suite'][s]['median']:.4f}/${c['by_suite'][s]['mean']:.4f}"
                          for s in suites))


def check_frozen(dec):
    """Reproduce the frozen probe decomposition from the archives.

    Compared as canonical JSON: the same bytes for the same values. The frozen
    file's own key order is the directory-iteration order of the uncommitted
    script that produced it on 2026-08-01, which no portable recompute
    reproduces (it varies by filesystem), so key sequence is normalized on both
    sides and every key and every value is compared exactly."""
    section("T7: frozen probe decomposition reproduced from the archives")
    with open(FROZEN) as f:
        frozen = json.load(f)
    failed = 0
    for arm in ARMS:
        if arm not in frozen:
            failed += 1
            print(f"  FAIL: decomposition.json has no {arm} entry to compare against")
            continue
        got, want = canonical(dec[arm]), canonical(frozen[arm])
        ok = got == want
        failed += 0 if ok else 1
        print(f"  {'PASS' if ok else 'FAIL'}: {arm} reproduces decomposition.json")
        if not ok:
            for key in sorted(set(dec[arm]) | set(frozen[arm])):
                a, b = dec[arm].get(key), frozen[arm].get(key)
                if a != b:
                    print(f"    {key}: got {a}, frozen {b}")
    extra = set(frozen) - set(ARMS)
    if extra:
        failed += 1
        print(f"  FAIL: decomposition.json carries arms this script does not recompute: {sorted(extra)}")
    return failed


def pins(dec, req, shape, cost, work):
    """Every number a committed artifact states, recomputed from the archives.

    Two artifacts are pinned beyond the frozen file. The probe SUMMARY.md
    states numbers in prose that decomposition.json does not carry as fields.
    The protocol quotes the per-request and cost decompositions, which are new
    here -- pinning them is what stops the protocol's motivating section from
    drifting away from the archives it cites."""
    summary = [
        ("median total tokens 23.7k / 26.4k / 57.3k / 148.1k",
         [dec[a]["median_total_tokens"] for a in ARMS], [23696, 26396, 57329, 148079]),
        ("cache creation 1,683 -> 5,906 across the arms",
         [dec[a]["median_cache_creation"] for a in ARMS], [1683, 2889, 4201, 5906]),
        ("ctx per turn 3,360 -> 15,831 at the frozen definition",
         [round(dec[a]["median_ctx_per_turn"], 1) for a in ARMS],
         [3359.7, 3741.0, 5954.4, 15830.6]),
        ("a3 median search result 3,393 chars against a2 601",
         [dec[a]["median_result_chars_by_tool"]["search"] for a in ("a3", "a2")], [3393, 601]),
        ("search payload 2.43M chars at a3 against 0.92M at a2",
         [dec[a]["total_result_chars_by_tool"]["search"] for a in ("a3", "a2")], [2428524, 918132]),
        ("a3 prompts 1,759 hits / 409,850 chars",
         [dec["a3"]["search_hits_by_source"]["prompts"], dec["a3"]["search_chars_by_source"]["prompts"]],
         [1759, 409850]),
        ("a3 endpoints 1,903 hits / 347,353 chars",
         [dec["a3"]["search_hits_by_source"]["endpoints"], dec["a3"]["search_chars_by_source"]["endpoints"]],
         [1903, 347353]),
        ("knowledge_pages hits 791 at a2 against 2,549 at a3",
         [dec[a]["search_hits_by_source"]["knowledge_pages"] for a in ("a2", "a3")], [791, 2549]),
        ("a3 lifecycle tools barely used: 1 memory_manage, 13 apply_knowledge, 0 captures",
         [dec["a3"]["tool_calls_by_name"].get(t, 0)
          for t in ("memory_manage", "apply_knowledge", "memory_capture")], [1, 13, 0]),
        ("fetch refused on every arm but a3, where it was called 3 times (#1176)",
         [dec[a]["tool_calls_by_name"].get("fetch", 0) for a in ARMS], [0, 0, 0, 3]),
    ]

    protocol = [
        ("static prefix bound 2,585 / 2,508 / 5,127 / 9,809 tokens per request",
         [round(req[a]["static_prefix_bound"]) for a in ARMS], [2585, 2508, 5127, 9809]),
        ("median context per request 3,562 / 3,766 / 6,699 / 16,358 tokens",
         [round(req[a]["median_ctx_per_request"]) for a in ARMS], [3562, 3766, 6699, 16358]),
        ("a2 search calls 1,061 = 1,028 federated + 32 browse + 1 error",
         [shape["a2"]["searches"], shape["a2"]["federated"],
          shape["a2"]["browse"], shape["a2"]["errors"]], [1061, 1028, 32, 1]),
        ("a2 federated: median 1 hit, 398 returned nothing, 22 at the display budget",
         [round(shape["a2"]["median_hits"]), shape["a2"]["empty"], shape["a2"]["at_budget"]],
         [1, 398, 22]),
        ("a3 search calls 993 = 982 federated + 8 browse + 3 error",
         [shape["a3"]["searches"], shape["a3"]["federated"],
          shape["a3"]["browse"], shape["a3"]["errors"]], [993, 982, 8, 3]),
        ("a3 federated: median 10 hits, 262 returned nothing, 642 at the display budget",
         [round(shape["a3"]["median_hits"]), shape["a3"]["empty"], shape["a3"]["at_budget"]],
         [10, 262, 642]),
        ("a2 ranked lexical on 1,027 of 1,028 federated searches; a3 hybrid on 979 of 982",
         [shape["a2"]["ranking"].get("lexical", 0), shape["a2"]["ranking"].get("hybrid", 0),
          shape["a3"]["ranking"].get("hybrid", 0), shape["a3"]["ranking"].get("lexical", 0)],
         [1027, 0, 979, 0]),
        ("endpoints matched 0 candidates across the a2 run and 12,700 across a3",
         [shape["a2"]["matched_by_source"].get("endpoints", 0),
          shape["a3"]["matched_by_source"].get("endpoints", 0)], [0, 12700]),
        ("knowledge_pages matched 853 candidates at a2 against 7,716 at a3",
         [shape["a2"]["matched_by_source"]["knowledge_pages"],
          shape["a3"]["matched_by_source"]["knowledge_pages"]], [853, 7716]),
        ("median episode spend $0.0169 at a0 against $0.0573 at a3, a 3.4x ratio",
         [round(cost["a0"]["median"], 4), round(cost["a3"]["median"], 4),
          round(cost["a3"]["median"] / cost["a0"]["median"], 1)], [0.0169, 0.0573, 3.4]),
        ("the token ratio over the same pair is 6.2x, nearly twice the cost ratio",
         [round(dec["a3"]["median_total_tokens"] / dec["a0"]["median_total_tokens"], 1)], [6.2]),
        ("on the trap suite the raw arm is the expensive one: mean $0.0849 at a0 against $0.0469 at a2",
         [round(cost["a0"]["by_suite"]["s3"]["mean"], 4), round(cost["a2"]["by_suite"]["s3"]["mean"], 4)],
         [0.0849, 0.0469]),
        ("mean spend per s1 episode 0.0158 / 0.0147 / 0.0261 / 0.0491 across the arms",
         [round(cost[a]["by_suite"]["s1"]["mean"], 4) for a in ARMS],
         [0.0158, 0.0147, 0.0261, 0.0491]),
        ("mean spend per s3 episode 0.0849 / 0.0834 / 0.0469 / 0.0762 across the arms",
         [round(cost[a]["by_suite"]["s3"]["mean"], 4) for a in ARMS],
         [0.0849, 0.0834, 0.0469, 0.0762]),
        ("s3 mean tool calls 19.0 at a0 against 11.4 at a2, which is why a0 costs more there",
         [round(work["a0"]["s3"], 1), round(work["a2"]["s3"], 1)], [19.0, 11.4]),
        ("the archived arms cost $9.23 / $9.49 / $9.45 / $16.64 for 261 episodes each",
         [round(cost[a]["total"], 2) for a in ARMS], [9.23, 9.49, 9.45, 16.64]),
        ("median s3 episode $0.0665 at a0, $0.0549 at a1, $0.0419 at a2",
         [round(cost[a]["by_suite"]["s3"]["median"], 4) for a in ("a0", "a1", "a2")],
         [0.0665, 0.0549, 0.0419]),
        ("median s1 episode $0.0138 at a0, $0.0142 at a1, $0.0215 at a2",
         [round(cost[a]["by_suite"]["s1"]["median"], 4) for a in ("a0", "a1", "a2")],
         [0.0138, 0.0142, 0.0215]),
    ]

    failed = 0
    for title, group in (
        ("T8: probe SUMMARY.md prose, recomputed", summary),
        ("T9: protocol motivating section, recomputed", protocol),
    ):
        section(title)
        for label, got, want in group:
            ok = got == want
            failed += 0 if ok else 1
            print(f"  {'PASS' if ok else 'FAIL'}: {label}" + ("" if ok else f"  (got {got}, want {want})"))
    return failed


def main():
    dec = {a: decompose(a) for a in ARMS}
    if "--emit" in sys.argv[1:]:
        print(canonical(dec))
        return 0

    req = {}
    for arm in ARMS:
        req[arm] = per_request(arm)
        req[arm]["ctx_per_tool_call"] = dec[arm]["median_ctx_per_turn"]
    shape = {a: search_shape(a) for a in ARMS}
    cost = {a: cost_usd(a) for a in ARMS}
    work = {a: workload(a) for a in ARMS}

    table_tokens(dec)
    table_workload(work)
    table_per_turn(req)
    table_payload(dec, shape)
    table_cost(cost)
    failed = check_frozen(dec) + pins(dec, req, shape, cost, work)
    if failed:
        print(f"\n  {failed} check(s) FAILED: the archives no longer reproduce a committed artifact.")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
