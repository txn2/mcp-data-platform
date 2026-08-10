# Graph-completion confirmatory matrix (#1251): instrument kill — the stripped arm's prose mentions are a search route

Ran 2026-08-10, 09:57-15:21 local, one driver sequence
(`graph-confirmatory-driver.log`; an earlier launch was interrupted about a
minute into its first episode and restarted from a reset store,
`graph-confirmatory-driver-attempt1.log`). 99 episodes pre-registered, 99
run, 1 harness failure (the episode client itself reported an error;
recorded, not graded). Model alias `opus`, client `2.1.226 (Claude Code)`,
commit `7e441e30-dirty` (this branch's instrument, pre-merge), corpora
regenerated from Spec (Seed 1250, EdgeDensity 3) and fingerprinted in every
manifest. The design doc
([`../../docs/graph-completion-study-design.md`](../../docs/graph-completion-study-design.md))
froze the matrix, the DVs and the kill conditions before any episode ran;
no protocol amendment was needed or made.

Every headline number recomputes offline from this directory alone:
`python3 graph-confirmatory-analyze.py` (stdlib only, no network, no key;
its output is archived as `graph-confirmatory-analysis.txt`, and it exits
non-zero because the instrument kill below is present in the archives).
`graphstudy -mode reread -out <run dir>` reproduces any single run's
readings from its transcripts, regenerating the exact corpus from the
manifest spec and refusing on fingerprint mismatch. The per-scale
`graph-study-gate-*.json` / `graph-study-plant-*.json` files are the last
arm's (stripped) at each scale — the driver overwrites them per arm — and
every run archive embeds the gate reading and plant record it actually ran
under, which is what the analyzer and reread consume.

## The pre-registered instrument kill fired

The design pre-states, checked before any kill condition is read: *"any
stripped-arm discontinuity grounding"* invalidates the run pair, because
the twofold certification was taken to prove *"no search route exists and
the stripped plant has no edges."* Stripped-arm episodes grounded the
discontinuity constraints at **both** certified scales:

- scale 500 stripped: discontinuity grounded coverage **1.00** (all 15
  episodes, all six discontinuity constraints)
- scale 5000 stripped: discontinuity grounded coverage **0.93** (14
  surviving episodes)

The route is visible in every stripped transcript and it is not a harness
leak: the sweep gates passed with zero discontinuity hits and zero
signature leaks at the certified scales (archived per run), and no
signature appeared without its source page being fetched. What happened is
that **a prose mention is itself a discovery route**. The stripped arm
renders each authored edge as its fallback prose ("work on systems that
feed the statutory accounts also observes the company close calendar"),
the agent reads that sentence on an ordinary closure page it found through
task-derived search, and then queries the institution in the institution's
own vocabulary — `"company close calendar finance change freeze windows"`
(gs-change-plan r1, scale 500, third search of the episode) — which ranks
the finance page instantly. Fetch provenance in the stripped arms is 1.00
search across all three scales: the agents ran the traversal in query
space, two hops (read the mention, search its vocabulary) instead of one
(follow the edge).

The certification instruments were both sound for what they measured and
both measured the wrong query distribution: offline embedding rank and the
live sweep gate cover the cell prompt and the authored task phrasings —
queries derived from the *task* — while the defeating queries are derived
from the *corpus text the agent has already read*. Semantic discontinuity
as authored ("far from every task-derived query in embedding space") was
real and certified twice; it just is not the same thing as unreachability
for an agent that reads before it searches again.

## The matrix, as measured

From `graph-confirmatory-analysis.txt` (episode means; coverage grounded —
signature in the document AND a source page fetched):

```
scale arm       search      n fail  off-cov  off-grnd    sd  disc-grnd  overclaim  srch/ep  ftch/ep  srch/grnd  trav-ep
   50 graph     search     15    0     1.00      1.00  0.00       1.00          0      5.3     15.3       0.59       10
   50 stripped  search     15    0     1.00      1.00  0.00       1.00          0      6.5     16.1       0.73        0
  500 graph     search     15    0     1.00      1.00  0.00       1.00          0      6.1     12.3       0.67       15
  500 stripped  search     15    0     1.00      1.00  0.00       1.00          0      9.2     13.3       1.02        0
 5000 graph     nosearch    9    0     1.00      1.00  0.00       1.00          0      0.0      9.0       0.00        9
 5000 graph     search     15    0     1.00      1.00  0.00       1.00          0      5.7     10.7       0.63       15
 5000 stripped  search     14    1     0.95      0.95  0.19       0.93          0     12.2     11.2       1.44        0
```

- **Coverage is at ceiling in every cell of the matrix.** At 5000 pages an
  episode makes ~11 fetches against the 5000-page store (0.2 percent of
  the corpus) and still grounds every constraint. The probe's enumeration-ceiling boundary
  ("reading budget against corpus size") did not bind at any tested scale
  for this configuration, because discovery is not enumeration: targeted
  search plus vocabulary learned from fetched pages collapses a 5000-page
  haystack as effectively as a 50-page one. Scale moved the *cost*, not
  the coverage.
- **The scale-50 within-ceiling control replicated exactly** (both arms
  1.00, delta 0.00), as the design predicted.
- **The cost signal is the only separation and it grows with scale.**
  Searches per grounded constraint, graph vs stripped: 0.59 vs 0.73 (50),
  0.67 vs 1.02 (500), 0.63 vs 1.44 (5000). The graph arm's cost is flat
  across two orders of magnitude of corpus; the stripped arm's roughly
  doubles, plus the matrix's only failed episode and its only sub-ceiling
  coverage (0.95, SD 0.19) sit in stripped/5000. Graph-arm page-provenance
  fetches rise with scale (0.09 → 0.28 → 0.34) while stripped stays 1.00
  search-provenance: agents do use the edges when they exist, increasingly
  as the haystack grows — the edges just were not the only route.
- **The overclaim channel was inert.** Of 98 surviving episodes, 0 claimed
  completeness, 91 declared open items, 7 omitted the section. Overclaim
  cannot separate arms when no episode ever claims completeness; at this
  configuration the elicited claim is uniformly conservative even at
  measured 1.00 coverage. The closure mechanism's "do they know they are
  done" reading is therefore unmeasured, not merely null.
- **The auxiliary manipulation check passed.** Graph/no-search at 5000:
  1.00 grounded coverage in all 9 episodes, every episode a full-depth
  pure-edge walk (9.0 fetches per episode ≈ the closure size, zero
  searches possible). Voluntary traversal survives a 5000-page store
  around the closure; the probe's traversal finding is scale-invariant, as
  designed.

## Kill conditions, applied in writing

Checked in the design's order:

1. **Instrument kills (checked before any condition is read): FIRED.**
   Stripped-arm discontinuity grounding at both certified scales (above).
   Certifications at 500 and 5000 passed both instruments before episodes;
   no sweep-gate signature leak; no plant verification failure; the
   scale-50 arms replicated the ceiling collapse (delta 0.00). Per the
   frozen text, the certified-scale run pairs are invalid for the
   discontinuity DV and **no kill condition is read as a confirmatory
   finding.**
2. Read informationally against the archived numbers (not findings): kill
   1 (graph discontinuity coverage below 0.25) would not fire — graph-arm
   discontinuity coverage is 1.00 everywhere, agents follow the edges;
   kill 2's coverage-and-overclaim collapse (deltas +0.00/+0.05 and
   0.00/0.00) is numerically present at both certified scales; condition 3
   (proceed on a ≥0.30 discontinuity advantage, ≥0.15 off-entry advantage,
   or ≥0.15 overclaim reduction) is not met — the largest advantage
   anywhere is +0.07.
3. **Condition 4 (boundary condition) is the recorded outcome**: numbers
   to the register row, next step argued from them.

## What the kill teaches

The discontinuity manipulation is unauthorable under the design's own
meaning-constant arm contrast. The stripped arm must render the edge as
prose that preserves page meaning; meaning-preserving prose necessarily
names the institution; naming the institution hands a competent searcher
the vocabulary that closes the semantic gap. The certification consumed
one candidate at authoring time (the duty rota, #1250); the run consumed
the construct itself. An arm contrast that actually removed the mention
would change what the page *says* across arms and grade two different
documents — the confound the design refused by construction.

So at these scales, for an agent configuration that searches competently
and re-searches with vocabulary it reads, **authored edges do not change
what a completion document contains; they change what it costs** — flat
search cost across two orders of magnitude of corpus versus roughly
doubling, the matrix's only failure and only sub-ceiling cell in the
stripped 5000 arm — **and they remain the only route when search is absent
or weak** (the auxiliary walk here; the probe's no-search floors, 0.00
stripped vs 0.96/0.42 graph). Completeness-delivery framing for the graph
candidate is retired with the discontinuity construct; what survives for a
report or a curation claim is the cost-and-robustness framing, and any
further completeness claim would need an instrument whose "unreachable"
holds against read-derived queries, which meaning-constant authored prose
cannot provide.
