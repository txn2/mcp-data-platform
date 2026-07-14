# claude-cli adapter (#949) — run data

Data from the `claude-cli` benchmark adapter (real `claude -p`, subscription,
keyless) against the live **a3** platform, model `sonnet`, 2026-07-13.

Every run that produced output is kept here. Runs are labeled by the
audit-correlation METHOD they used, not by any judgment of their worth — the
episode data (transcripts, tool calls, answers, timings) is real in all of them.

## `smoke-2task-a3/` — functionality slice (`s1-customer-region`, `s2-completed-orders`, k=1)

Three runs of the same slice as the adapter's correlation approach was developed:

- `run1-audit-by-userid.json` — correlated by `user_id`, no time floor. The
  audit-row count reads 145/54: reused pool identities folded in earlier runs'
  rows. Both answers correct.
- `run2-audit-by-userid-timefloor.json` — `user_id` + a start-time floor; audit
  reads 3/6, both answers correct.
- `run3-audit-by-handle.json` — correlated by the `dps_` handle (the design that
  shipped): `audited_calls == tool_calls` exactly (4/4, 6/6), both correct.
- `run3-transcripts/` — full per-attempt transcripts for run3. (Runs 1 and 2
  reused the same transcript filenames during development and were overwritten;
  that data was lost. Future runs use unique per-run directories.)

## `full-a3-k1/` — full task-set run

Added when the full 87-task S1-S3 run on a3 completes (`results.json` +
`transcripts/`). benchrun flushes after every attempt, so this directory
accumulates data as the run proceeds.
