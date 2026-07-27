# Answer-handing sweep (#1054)

Exploratory. The mirror of the recheck-cost sweep, and the experiment that
decides what the cost sweep meant.

The cost sweep only ever delivered a belief saying the question could not be
answered. In that direction verification is also the route to a defensible
refusal, so a checking agent might be reluctant to decline rather than
distrustful of the note. Here the belief is true, the question is
answerable, and the note states the answer outright ("The ACME account has
three listening monitors provisioned"). Trusting means reading three off the
note and saying so: correct, and free.

Two cells carry no belief at all, at the cheapest and dearest cost. Without
them a uniform verification rate cannot distinguish "agents always check"
from "the delivered knowledge changed nothing".

| World | Calls to count | Condition | n | Verified | Correct | Median calls made |
| --- | --- | --- | --- | --- | --- | --- |
| `monitors-3` | 1 | belief delivered | 8 | 8 | 8 | 1 |
| `monitors-3` | 1 | no knowledge | 8 | 8 | 8 | 1 |
| `monitors-3-scoped` | 3 | belief delivered | 8 | 8 | 8 | 4 |
| `monitors-3-scoped-5` | 6 | belief delivered | 8 | 8 | 8 | 7 |
| `monitors-3-scoped-10` | 11 | belief delivered | 8 | 8 | 8 | 12 |
| `monitors-3-scoped-10` | 11 | no knowledge | 8 | 8 | 8 | 12 |

## Result

The delivered belief made no measurable difference to effort. At both ends
of the cost range the matched comparison is a difference of zero calls: an
agent handed the answer spent exactly what an agent given nothing spent.

The note was not missed. It appears in all 32 belief transcripts, and 22 of
32 final answers refer to it explicitly, as confirmation of what the agent
had just independently established:

> "Confirmed by directly querying every workspace: 3 monitors total ...
> matching the cached insight and consistent with current live data."

So the agents read the note, re-derived its content at up to eleven calls,
and cited it as corroboration of their own work rather than as a source they
relied on.

## What this establishes

Taken with the cost sweep, verification is insensitive to the price of
checking (1 to 11 calls), to the direction of the belief (a claim that
nothing exists, or a claim that hands over an answer), and to whether a
belief was delivered at all.

The honest reading is that on this platform, this model, and these
questions, delivered knowledge is not functioning as knowledge. It is
functioning as a hypothesis the agent re-establishes before use. That is a
finding about the value of the delivery, not only about the agent's
vigilance, and it bears on every platform decision premised on an agent
acting on what it was told.

## Scope

One model (claude-cli, sonnet), two questions, one belief per direction,
bare delivery only, k=8. The enriched-metadata arm is untested here, as are
the phrasing factorial and a second model tier. Nothing here licenses a
claim about agents in general.

## Cross-model and raw-API replication (2026-07-25)

Two further runs of the same cells, archived alongside the original.

`pk-answersweep-haiku-20260725-145541` (claude-cli, haiku): the null-delivery result does NOT replicate on the weaker tier. Haiku trusted the delivered note in 29 of 32 belief attempts (verified 3/32), reading the count off it at every cost level, while its no-knowledge controls probed 16/16. Same client, same version, same cells as the sonnet run: the behavior flips with the model, so sonnet's re-derivation is not a client artifact. Haiku's trust makes delivery efficient where sonnet's re-derivation makes it redundant — and makes haiku-class agents the tier for which stale delivered state is a live risk.

`pk-answersweep-api-20260725-153443` (raw Messages API, claude-sonnet-5, in-process loop, no client): the sonnet result replicates exactly. Verified 48/48, trusted 0, zero effort delta against controls. The null-delivery finding is a property of the model, not of Claude Code. Note: this run's archive predates per-attempt usage recording; spend is estimated, not recorded.

## Release-tagged rerun, v1.116.0 (2026-07-26)

`pk-answersweep-sonnet-v1116-20260726-143727` and `pk-answersweep-haiku-v1116-20260726-152326`, on a platform binary built from the `v1.116.0` tag (manifests carry the tag commit). Both headlines replicate exactly: sonnet verified 32/32 belief attempts and trusted 0 with a zero effort delta against controls; haiku trusted 29/32 and probed 16/16 controls.
