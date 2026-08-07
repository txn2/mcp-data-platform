# Platform-tax probe (archival, 2026-08-01)

Probe of the headline effect for a candidate "platform tax" study: what does the
semantic-platform machinery cost on tasks that do not need it, and is the cost
attributable? Purely archival re-analysis of
`bench/results/phase2-anthropic-k3/` (raw-API path, sonnet, n=261 attempts per
arm, k=3). No new episodes were run. Full numbers in `decomposition.json`
(regenerable from the archived results.json + transcripts; script inline in the
session that produced this file).

## Headline decomposition (median per attempt, total tokens incl. cache)

| Arm | Total | s1 | s3 | cache_creation | cache_read | ctx/turn | tool calls |
| --- | --- | --- | --- | --- | --- | --- | --- |
| a0 raw tools | 23.7k | 18.3k | 73.5k | 1,683 | 21,373 | 3,360 | 7 |
| a1 +enrichment | 26.4k | 19.1k | 53.5k | 2,889 | 22,476 | 3,741 | 7 |
| a2 +discovery | 57.3k | 46.0k | 73.8k | 4,201 | 52,443 | 5,954 | 9 |
| a3 +lifecycle tools | 148.1k | 108.4k | 177.2k | 5,906 | 141,911 | 15,831 | 9 |

Accuracy context (published, report v1.1/v2.0): s3 traps 42.7 / 57.3 / 98.7 /
98.7; s1-s2 statistically indistinguishable across arms.

## Findings

1. **Enrichment is nearly free where irrelevant and pays where it binds.**
   a0→a1: `trino_describe_table` median result grows 1,074 → 2,601 chars
   (the semantic enrichment payload), yet median total tokens move 23.7k → 26.4k
   (+11%), s1 +4%, and on s3 tokens FALL 73.5k → 53.5k (-27%) with +14.6pp
   accuracy and fewer mean tool calls (10.15 → 8.95). The enrichment payload
   more than doubles per-describe size and still reduces end-to-end cost on the
   tasks it serves.

2. **The static tool surface is not the multiplier.** cache_creation grows only
   1.7k → 5.9k a0→a3. The a3 blowup is context re-read per turn: 3.4k → 15.8k
   (4.7x) at the same median turn count.

3. **The multiplier is search-result payload, and it tracks platform
   population, not the arm's tool list.** a3 median search result = 3,393 chars
   vs a2 = 601 chars (5.6x; totals 2.43M vs 0.92M chars). By federated group:
   knowledge_pages hits 791 → 2,549, plus two groups a2 barely returns —
   prompts (1,759 hits / 410k chars) and endpoints (1,903 hits / 347k chars).
   Insights are negligible (14 hits) and memory tools were essentially unused
   during eval (1 memory_manage, 13 apply_knowledge, 0 captures), so the cost
   is NOT lifecycle-tool behavior. The a2/a3 config diff is only the persona
   allow-list (memory_*, apply_knowledge, manage_feedback) — the federation
   breadth difference reflects what was populated in the shared platform at run
   time. CONFOUND, must be controlled in any study: a dedicated arm varying
   store population (empty vs populated prompts/endpoints/knowledge stores)
   under one config is required to separate config from state. If confirmed,
   the deployment-relevant claim is: search-context tax grows with platform
   population and is paid on every turn of every task, relevant or not.

## Verdict

Probe HOLDS. Cost metrics are continuous with large observed deltas (no
ceiling failure mode); the benefit side is already published; the one confound
(population vs config) is cheap to close and is itself the study's most
deployment-relevant arm. Candidate mitigation levers already in the product:
per-group result caps / relevance thresholds, persona-scoped federation,
`enrichment.column_context_filtering`.

## Open decision (user)

The spine re-analyzes archived artifacts of the knowledge-layer study; the
series rule says every artifact belongs to exactly one study. Either the new
study runs its own arms (clean, more spend) or the series adopts a stated
convention for cross-study re-analysis of published archives.
