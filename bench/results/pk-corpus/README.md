# Perishable-knowledge capture corpus (#1054, stage 1)

Real capture episodes over the perishable fixture, archived verbatim. This
is the evidence behind the frozen seed set in `bench/specs/pk-seeds.json`:
it is what lets the study say its phrasings are artifacts of this
platform's capture rather than strawmen the study wrote for itself.

Both runs are kept. Neither is a draft.

| Run | Replicates | Episodes | Captures | Notes |
| --- | --- | --- | --- | --- |
| `pk-corpus-20260724-170157` | 1 | 5 | 6 | First run, all scenarios captured. |
| `pk-corpus-20260724-170825` | 3 | 15 | 13 | Replicate 1 (5 episodes) captured nothing: it reused the first run's pool identities, and each agent's opening `search` found its own earlier insight and correctly declined to record it again. Replicates 2 and 3 are clean. |

The runner now refuses to start against identities that already hold
knowledge, so that contamination cannot recur silently. Clear the knowledge
store between corpus runs.

Each run holds `corpus.json` (manifest, scenario set, system scaffold, and
every episode with its world, tool accounting, final answer, and captured
insights) and `transcripts/` (one conversation per episode).

Stimulus provenance: the manifest's `scenarios_hash` covers the scenario
set and the system scaffold together, so an archived corpus names the exact
stimulus that produced it. Model and client version are in the manifest;
these runs are claude-cli and carry no metered cost.

What the corpus showed, and how it bears on the study's premise, is
recorded in `bench/docs/perishable-knowledge-estimator-audit.md`.
