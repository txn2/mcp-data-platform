# Graph-completion study: separation validation record (#1250)

**Status: the study is published** as
[`docs/reference/benchmark-report-graph-completion.md`](../../../docs/reference/benchmark-report-graph-completion.md)
(version 1.0, 2026-08-10; DOI 10.5281/zenodo.21881798). The confirmatory
matrix (#1251, [`../graph-completion-confirmatory/`](../graph-completion-confirmatory/))
later showed that what this record certifies is unreachability for
task-derived queries only — stripped-arm agents defeated it with
read-derived queries, the study's pre-registered instrument kill and the
report's headline. This record is unchanged: it is the archived proof that
both instruments passed before any episode, which is what makes the kill a
finding about the construct rather than a harness leak.

The stage-3 deliverable of
[`../../docs/graph-completion-study-design.md`](../../docs/graph-completion-study-design.md):
both discontinuity certification gates run against the generated study corpus
at every scale, before any confirmatory episode exists. Corpora are
regenerable from the plant records' Spec (Seed 1250, EdgeDensity 3); the
platform is the gt stack (`make bench-gt-up`, `nomic-embed-text` via ollama),
and each scale was planted in the graph arm, embedded, swept, and reset in
one driver sequence over the final corpus generation
([`driver.log`](driver.log), 2026-08-10, ~14 minutes end to end).

## Readings

| Scale | Offline certification (embedding rank) | Live sweep gate (platform `search`) |
| ---: | :--- | :--- |
| 50 | FAIL, `horizon_exceeds_corpus`: the top-25 horizon covers half the store; discontinuity pages rank 9-25 for task phrasings | FAIL as pre-stated: 5 of 6 discontinuity pages appear in hit lists at limits 25/100; zero signature leaks |
| 500 | PASS, 12/12 phrasings (horizon top-25): no discontinuity page inside the horizon, entries rank 1-4 for prompt queries | PASS: 27 sweep rows, zero leaks, zero discontinuity hits, entries surface |
| 5000 | PASS, 12/12 phrasings (horizon top-100) | PASS: 27 sweep rows, zero leaks, zero discontinuity hits, entries surface |

Separation appears exactly where the design predicts: within the enumeration
ceiling (50 pages) the discontinuity manipulation does not exist and both
instruments say so; at 500 and 5000 the six institutional pages are
unreachable from every task-derived phrasing at every swept limit while the
cells' topical pages remain reachable.

## The enumeration profile, per scale

From the gate reports' `page_ranks` (constraint-set pages surfacing across
the whole sweep) and `entry_rank` at the modal limit 25 for each cell's
prompt-derived query:

| Scale | gs-change-plan | gs-incident | gs-feed-onboarding |
| ---: | :--- | :--- | :--- |
| 50 | entry@1; 9 of 9 set pages surface | entry@1; 9 of 9 | entry@4; 7 of 7 |
| 500 | entry@1; 7 of 7 non-discontinuity set pages | entry@1; 7 of 7 | entry@4; 6 of 6 |
| 5000 | entry@1; 7 of 7 | entry@1; 2 of 7 | entry@5; 5 of 6 |

Two boundary observations for the confirmatory design:

- **Entry pages stay findable at every scale** for the prompt-derived query
  (the search arms keep their entry point), while broad phrasings decay: at
  5000, two of the incident cell's three phrasings no longer surface the
  entry at all, and its spread mass has fallen to 2 of 7 reachable pages.
  The affordability boundary the study reads coverage against is visible in
  the discovery instrument itself.
- **The platform's deliverable list caps near 25 hits** regardless of the
  requested limit (max observed hit-list length 25 at limit 100, at every
  scale). At 42 pages the probe saw limit-100 return the whole store; at
  study scales an episode's practical horizon is ~25 results per query,
  which is what the offline certification's scaled horizon models.

## Files

| File | What it is |
| --- | --- |
| `graph-study-cert-<scale>.json` | Offline embedding certification (per-phrasing entry rank, enumeration profile, discontinuity violations) |
| `graph-study-gate-<scale>.json` | Live sweep gate (3 phrasings x limits 5/25/100 per cell, hits as corpus keys, leaks, discontinuity hits) |
| `graph-study-plant-<scale>.json` | Plant record: generation Spec plus every page's platform id for that sequence's plant |
| `driver.log` | The sequence log, embedding waits included |

Reproduce offline against any of the specs:

```bash
make bench-gs-certify BENCH_GS_SCALE=500
```

and live per scale with the gt stack up:

```bash
bash bench/scripts/graph-separation-driver.sh
```

Earlier sequences the same day ran against superseded corpus generations
(the certification consumed one discontinuity candidate and reshaped the
filler; a planter race aborted one 5000-page plant mid-sequence) and their
readings were regenerated rather than kept: every artifact above is from the
one final sequence over the frozen corpus.
