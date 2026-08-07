# convention-correct-sonnet, first attempt: kept, not used

This arm ran to completion (24 episodes, 23/24 correct) and then failed its
own store-constancy check, because the arm script took the 7.3 baseline
BEFORE the plant rather than after it. The two records reported as drift are
the plant's own:

- insight `ab10c6cbd1b7caad8d3fa9edbafd51ae`, `captured_by
  bench-agent-200@apikey.local` (the teacher identity)
- changeset `c1854878ce9bbb03ee2806f9cb6ed75a` on the orders entity

Both ids match `planted.json` exactly, and no evaluator identity appears in
the drift at all, so the invariant the check exists to protect was never
violated: the store was constant across the 24 evaluation episodes. Protocol
7.2 excludes the plant's capture, approval and apply from the arm's
accounting, and the baseline should have been taken after the plant and its
settle.

The script now takes three snapshots -- `store-clean.json` before the plant,
`store-before-eval.json` after it, `store-after-eval.json` after the eval,
with the comparison between the last two. The cell is re-run under the fixed
script so its archive carries the same evidence shape as every other arm.
This directory is kept because the episodes it produced are real data.
