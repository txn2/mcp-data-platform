# Knowledge-use run data

Every run family behind
[`docs/reference/benchmark-report-knowledge-use.md`](../../../docs/reference/benchmark-report-knowledge-use.md).
The protocol is [`docs/knowledge-use-protocol.md`](../../docs/knowledge-use-protocol.md);
the pre-registration, kept unchanged, is
[`docs/perishable-knowledge-study-design.md`](../../docs/perishable-knowledge-study-design.md).

**Everything here is exploratory.** The pre-registered confirmatory matrix was
never executed: the power pre-run falsified the primary hypothesis, and every
family after it was designed in response to what the previous one showed. The
families are therefore best read in the order below, which is the order they
were run.

| Family | What it established |
| --- | --- |
| [`pk-corpus/`](pk-corpus/) | The delivered belief phrasings are artifacts of this platform's own capture, not strawmen the study wrote for itself. |
| [`pk-prerun/`](pk-prerun/) | Verification at ceiling on the primary contrast, which falsified H1a and ended the confirmatory plan. |
| [`pk-costsweep/`](pk-costsweep/) | The ceiling is not an artifact of a free check: verification held across an eleven-fold rise in recheck cost. |
| [`pk-answersweep/`](pk-answersweep/) | Sonnet re-derives an answer handed to it, at zero effort delta against no-knowledge controls; Haiku trusts it. The capability flip. |
| [`pk-bridge/`](pk-bridge/) | Non-derivable conventions are used by every model and driver tested, and suppress confident fabrication. |
| [`pk-staleanswer/`](pk-staleanswer/) | On the weak tier a stale note is strictly worse than no note: Haiku went from a perfect control to zero. |
| [`s5-supersede-probe/`](s5-supersede-probe/) | Supersede is at ceiling conditional on capture; the defect is upstream, in capture entity mis-filing. |

Headline cells were rerun on a `v1.116.0` tag build and replicate; those reruns
are archived inside the same family directories as the originals, never in
place of them.

Report tables and figures recompute from these directories offline via
`bench/reports/knowledge-use/pk_tables.py` and `figures.py`.
