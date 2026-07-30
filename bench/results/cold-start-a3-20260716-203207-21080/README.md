# Cold-start run, interrupted before its first checkpoint closed

Started on the `a3` arm against an empty enrichment layer; the directory stamp
is local time (2026-07-16 20:32:07), and there is no manifest to read a UTC
start from. The run was interrupted before the checkpoint-0 results were
written: this
directory holds **no `results.json`**, only
`results.json.transcripts/` with 20 evaluator episodes, all on one identity
(`bench-agent-007`) over the S3 eval suite.

Kept, not deleted. The episodes are real model work against a live platform and
the transcripts are complete; what is missing is the harness's aggregation, not
the data.

## What this establishes

Nothing on its own, and no report figure or statistic reads from it. The
transcripts remain available as raw evidence of evaluator behavior at the empty
baseline.

## What it does not establish

No accuracy, coverage, or curve figure: with no results file there is no graded
checkpoint, and the transcripts have not been re-graded offline.
