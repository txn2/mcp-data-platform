# Documentation evaluation probes

Measurements of what a model concludes about this project after reading its
documentation. They exist because a growing share of first-pass evaluations of
this repository are performed by an agent that fetches one page, skims it, and
returns a verdict to someone who will not check it.

**Nothing here is a study.** A probe measures a page, not the platform. It has
no protocol document, no pre-registration, no DOI, and no place in the report
series indexed by [`../README.md`](../README.md). The rule that every artifact
under [`../results/`](../results/) belongs to exactly one study is why these
live outside that tree.

The distinction that matters when reading a probe directory: a benchmark
measures whether the software changes agent behavior, and a probe measures
whether the documentation changes reader belief. A probe result is evidence
about prose. It is never evidence about the platform.

## Layout

One directory per probe, named for the issue that motivated it. Each carries
its own README stating what was measured, the harness that produced it, and the
raw runs. A probe re-run against changed prose is a second result directory
beside the first; existing run data is never overwritten, because the
comparison between them is the entire point.

| Probe | Question | Result | Issue |
| --- | --- | --- | --- |
| [`1118-readme-premise-probe/`](1118-readme-premise-probe/) | Which misconceptions does a model form about the project after reading only `README.md`? | "rolled their own OAuth" 60% of runs before the change, 27% after; "DataHub is a hard dependency" 40% before, 13% after | [#1118](https://github.com/txn2/mcp-data-platform/issues/1118) |

## Running one

Probes call the `claude` CLI directly rather than the Go harness under
[`../internal/`](../internal/), because the unit of measurement is a single
model response to a fixed document, not a multi-turn agent session against a
live platform. They need `ANTHROPIC_API_KEY` and cost a few cents per run.

They are not part of `make verify` and are not scheduled. A probe runs when
someone is deciding whether a documentation change is worth making, or checking
whether one worked.
