# API-connection study pilot data (internal validation, not publication grade)

Complete k=1 matrix for the #1027 API-connection architecture study: 4
arms x 3 catalog tiers x 50 tasks, run 2026-07-23 via claude-cli
(Claude Code 2.1.218, Sonnet), 600 graded episodes, zero harness
failures. The study was **not published**; see the Outcome note at the
top of `bench/docs/api-connection-study-design.md` for why (accuracy
saturates by construction: single connection, uniformly well-named
operations, tasks that always need the API).

This data is retained as (a) a regression baseline for the harness and
the arms, (b) the empirical input for any successor study's separation
analysis, and (c) the measured basis for the search-is-sufficient
design note (fixed ~3.2k-token tool surface vs a measured 365,902
tokens of per-endpoint tool definitions at 2,503 operations).

## Accuracy by arm and tier (k=1, 50 tasks per cell)

| arm | t0 (53 ops) | t1 (501) | t2 (2,503) |
| --- | --- | --- | --- |
| b0 per-endpoint | 0.94* | 0.96 | 0.96 |
| b1-lex search+invoke | 0.96 | 0.94 | 0.96 |
| b1-hyb search+invoke (shipped default) | 0.96 | 1.00 | 0.96 |
| b2 code mode | 0.96 | 0.98 | 0.94 |

*b0 t0 is 0.94 as recorded; one of its three failures is a
write-detection false positive fixed after the run (POST `:search` was
counted as a write), regrading it to 0.96. Nearly every failure in the
matrix is a p5 irrelevance task graded by the strict lexical refusal
fallback; genuine task failures are on the order of 2-3 in 600.

Retrieval hit rate (b1 arms, gold operation surfaced in any
api_list_endpoints result): hybrid 100/100/98 percent across tiers,
lexical 93/88/90.

## Known caveats (read before using this data)

1. **The b0 arm is not naive all-tools-in-context.** Claude Code
   applies its own tool search / deferred loading over large MCP
   toolsets, so b0 here measures "per-endpoint plus client-side tool
   search". The naive condition (full catalog in the prompt) was never
   run.
2. **k=1, one model, one client.** No CIs; claude-cli numbers are not
   comparable across Claude Code versions (2.1.218 recorded in every
   manifest).
3. **Refusal grading is the lexical fallback** (`refusal_judged: false`
   on every p5 attempt); an LLM judge would likely lift several cells.
4. **Early b1-lex t0 attempts predate the retrieval-extraction fix**
   for claude-cli's `mcp__<server>__` tool-name prefix; retrieval for
   those is recomputable from transcripts.

## Layout

- `api-<arm>-<tier>-<timestamp>/results.json`: the 12 good cells
  (manifests pin arm, tier, task-set hash, seed, client version).
- `contaminated/`: the two first-attempt b1 t0 runs whose personas
  lacked `list_connections`; agents guessed connection names and burned
  their budgets. Kept as the evidence that found the config gap (the
  platform's not-found error steers to `list_connections`; the arm
  configs now allow it).
- `b2-t0-live-sanity.json`: the 3-episode live b2 smoke that validated
  code mode end to end.
- Full per-attempt transcripts (with fixture access logs) are not
  committed; they are archived locally as
  `~/bench-api-study-transcripts-20260724.tar.gz` (~4 MB).

Regenerate the fixtures and task set with `make bench-api-gen`; the
committed artifacts are drift-checked by
`bench/internal/apigen/drift_test.go`.
