# Context-economics run data

Run families for the context-economics study (epic #1164): what does the
semantic-platform machinery cost per turn when a task does not need it, what
does it buy when it does, and which existing configuration levers move the cost?

| Family | What it established |
| --- | --- |
| [`probe/`](probe/) | The premise probe, archival. Decomposes per-attempt cost in the knowledge-layer study's committed archives into cache creation, cache read per tool call, and result payload by tool and federated group. It establishes that the cost metrics are continuous with large deltas and that search-result payload is the largest per-turn component; it establishes nothing causal, and its own attribution question is open. |

The probe consumes another study's archives under the cross-study re-analysis
convention in [`../../README.md`](../../README.md), so it carries a provenance
table naming the source study, its DOI, and the exact paths read. It is
motivating evidence and is never cited as a confirmatory result.

No protocol document and no report exist yet. The protocol, the rerunnable
decomposition toolchain, and the measured arms land on later sub-tickets of the
epic; nothing here is pre-registered until that protocol is committed and
signed.
