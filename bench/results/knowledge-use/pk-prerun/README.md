# Perishable-knowledge power pre-run (#1054, work item 5)

Exploratory by construction. These attempts fix `k` and the `p` grid for the
confirmatory matrix and are excluded from any confirmatory analysis.

Two cells, the two ends of the staleness axis on the study's primary
question: the same belief in its neutral phrasing, delivered bare, asked
once in the world it describes (where refusing is correct and checking is
optional) and once in a world that has moved on (where only an agent that
looks can answer).

| Run | k | Scaffold | Grader | Kept because |
| --- | --- | --- | --- | --- |
| `pk-prerun-20260725-000717` | 2 | v1 | v1 | First end-to-end run of the cell runner. |
| `pk-prerun-20260725-001021` | 8 | v1 | v1 | The confounded-scaffold arm. Its numbers are not discarded: v1 against v2 is what shows the scaffold was not driving the result. |
| `pk-prerun-20260725-002128` | 8 | v2 | v2 | The run that fixes k. |

Two defects were found and fixed between the runs, both in the harness
rather than the platform, and both on the primary measure.

**Scaffold (v1 to v2).** The query scaffold said "ground your answer in what
the tools return", which reads as an instruction to call the data tools and
makes trusting a stored note feel like a violation. Report 1's wording, now
restored, licenses answering from saved knowledge explicitly. Without that
clause the study would have measured instruction-following in place of the
verification decision. The v1 and v2 rates are identical, so the ceiling
reported below is not an artifact of the earlier wording.

**Refusal grading.** The scaffold prescribes `FINAL ANSWER: UNAVAILABLE` and
the refusal detector did not recognize that token, so three correct refusals
in the v1 run were graded as failures, on exactly the cells where refusing
is correct. The scaffold and the grader now share one constant. The v1
archive re-grades cleanly offline under the fixed rule, which is why it is
kept rather than rerun.

## Result

Verification was at ceiling in every attempt, under both scaffolds.

| Cell | Required | n | Verified | Correct |
| --- | --- | --- | --- | --- |
| belief true, world empty | refuse | 16 | 16 | 16 |
| belief stale, world populated | verify then answer | 16 | 16 | 16 |

The stale cell is uninformative about vigilance: answering there needs a
monitor id, so verification is structurally required and 16/16 only shows
the agents wanted to answer. The informative cell is the fresh one, where
the note already supplied the answer and checking was optional. Agents
checked in 16 of 16 attempts, several noting explicitly that the result
matched the stored note. The 95% Wilson interval on that rate is
[0.81, 1.00].

## Cross-model replication (2026-07-25)

`pk-prerun-haiku-20260725-151617` (claude-cli, haiku): the same two cells on the weaker tier. Fresh cell: verified 5/8, trusted 3/8, correct 7/8. Stale cell: verified 7/8, trusted 1/8 (the trusting attempt produced the wrong refusal), correct 6/8. Read with the haiku answer sweep, haiku's trust is shaped by what the note offers rather than blanket: it trusts notes that hand it an answer (29/32 there) but mostly still probes when a note asserts unavailability and the task demands data (7/8 here). The direction that would expose haiku hardest — a stale answer-bearing note, where its trust would produce a confidently wrong value — is not yet run.
