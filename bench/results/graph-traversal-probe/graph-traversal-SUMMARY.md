# Graph-traversal probe summary (#1241 premise probe, 2026-08-09)

Question probed: when the answer to a task lives in a knowledge page reachable
only by following references from the page `search` surfaces, does the agent
follow them, and how deep does it get?

Kill conditions, stated in the ticket before any run:

1. **Floor.** If no tier ever fetches a referenced page across all cells, there
   is no traversal to study.
2. **Ceiling.** If every tier reliably reaches depth 3, depth does not
   discriminate at this scale.
3. **Ambiguity is a kill**, per the lifecycle in
   [`../../docs/findings-register.md`](../../docs/findings-register.md).

Timebox: half a day of hand-driven runs, k of 2 to 4 per depth cell, on the
capability-curve extremes, fetch-enabled personas.

## Design

A 42-page operations wiki planted through the platform's own knowledge-page API
(`bench/internal/graphfix`, `bench/graphprobe`), sized to a live deployment
rather than to a Wikipedia snapshot. Four cells put an operational question to
the agent whose answer sits 0, 1, 2 or 3 references from the page `search`
returns for it, with off-path sibling references at every hop.

Each hop hands over a token the next page is keyed on and that the question does
not contain: `clickstream-raw` is storage class SC-4, and the register that
prices SC-4 at 62 days never says "clickstream". So the page holding a ground
truth does not answer the question on its own even when it is read directly.

| Cell | Depth | Chain | Bridges | Answer |
| --- | ---: | --- | --- | --- |
| `gt-d0-vacuum` | 0 | vacuum runbook | none | 3400 rows |
| `gt-d1-clickstream` | 1 | export runbook, storage class register | SC-4 | 62 days |
| `gt-d2-billing` | 2 | stream runbook, regulated tier rules, change class reference | regulated-tier, CC-3 | 9 business days |
| `gt-d3-ledger` | 3 | job runbook, severity bands, escalation ladders, response matrix | band B, amber, second rung | 25 minutes |

The persona grants `platform_info`, `search`, `fetch` and `list_connections` and
nothing else: no warehouse, no catalog, no API surface, no memory. The episode
scaffold never mentions references, pages, or reading anything in full, so the
only channel that names `fetch` is the platform's own delivered instruction
(`reuseBullet`, `pkg/platform/instructions/instructions.go`), which is the
depth-1 asymmetry the ticket pre-stated rather than removed.

## Fixture gate

Run before any episode, as pre-stated: each cell's natural query through
`search`, keeping the cell only if the entry page surfaced and the answer page
did not. It passed on all four cells, at the tool's default limit.

| Cell | Entry | Answer page | Answer value in any hit text |
| --- | --- | --- | --- |
| `gt-d0-vacuum` | #1 | #1 (it is the entry page) | no |
| `gt-d1-clickstream` | #1 | absent | no |
| `gt-d2-billing` | #1 | absent | no |
| `gt-d3-ledger` | #2 | absent | no |

The gate is archived beside this file as `graph-traversal-gate.json`. Getting
there took three fixture revisions: the first two corpora leaked the answer page
into the top five for every cell above depth 0, and the neighbouring pages that
now sit between them were written for that reason.

## Result

**162 dereferences across 76 episodes. 160 of them used a reference `search`
had already returned. Two used a reference only a page could have supplied, both
of them the first hop, and no episode anywhere reached depth 2 or 3 through a
reference it learned by reading.**

| Run | Model | k | n | Correct | Episodes that fetched | Dereferences | Learned from a page |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `gt-opus-20260809-023739` | opus | 3 | 12 | 12 | 12 | 30 | 0 |
| `gt-haiku-20260809-022815` | haiku | 3 | 12 | 4 | 6 | 13 | 2 |
| `gt-opus-20260809-020914` | opus | 3 | 12 | 12 | 12 | 31 | 0 |
| `gt-haiku-20260809-015944` | haiku | 3 | 12 | 9 | 11 | 25 | 0 |
| `gt-opus-20260809-014735` | opus | 3 | 12 | 9 | 12 | 36 | 0 |
| `gt-haiku-20260809-013323` | haiku | 3 | 12 | 6 | 8 | 18 | 0 |
| `gt-haiku-20260809-013001` | haiku | 1 | 4 | 3 | 4 | 9 | 0 |

The bottom three runs used a fixture whose depth-3 cell was defective; see
below. Their traversal reading is unaffected by that defect and they are counted
here.

Opus read every page on the chain in every cell of the two current runs, to full
depth 3, and answered 24/24 correctly. It did not get there by following
references. It got there because its own search returned the whole chain.

## Why the depth manipulation collapsed

The agent chooses the search `limit`. The gate ran at the tool default; the
episodes did not.

- Opus passed `limit: 25` on 65 of its 67 searches and 20 on the other two.
  Haiku ranged from 1 to 100, most often 20.
- A search at those limits routinely returned 20 of the 42 corpus pages, and in
  one haiku episode all 42.
- The page holding the ground truth came back to the episode's own search in
  **75 of 76 episodes**, and at the episode's first search in nearly all of
  them.

At a corpus size the ticket itself argues is the ecologically valid one, tens of
pages rather than 549K, there is no such thing as a buried page: every page is
one query away, and an agent that raises the limit sees half the corpus in a
single call.

The two traversal hops are the exception that fixes the rule. Both are in
`gt-haiku-20260809-022815`, both at depth 1, and one of them is the single
episode out of 76 whose own search did not return the answer page. Traversal
appeared exactly where search had left something out, and nowhere else.

## Verdict: not a kill, an instrument defect

The pre-stated kill conditions were written against a fixture that could not
have shown traversal, so neither exit applies.

Every cell poses a single-fact lookup question. A reference graph is the cheaper
route, or the only one, when a task cannot be completed from the page search
returns and its missing pieces cannot be named by the asker and therefore cannot
be queried for. A lookup question has no such missing pieces. The design
therefore held its own discriminating condition at zero and put search and the
graph in competition on search's own ground, which is the failure the program
already recorded against the API-connection study: a discriminating variable
left at its easy setting produces a foregone conclusion.

What survives is narrower and worth keeping. For lookup-shaped questions on a
corpus of this size, search reaches the page holding the answer directly, an
agent reads three or four pages of a chain in one pass after a single query, and
reference following adds nothing search does not already do.

What does not survive is any claim about the candidate. It stays in the ledger,
and a second probe needs cells whose task cannot be completed from the entry
page, whose missing pieces cannot be named in a query, and whose dependent
variables are coverage of the answer and searches-per-answer rather than depth
reached.

## Evidence the appetite exists in real use

A separate reading of one production deployment's audit log records 31 `fetch`
calls on knowledge pages across 23 sessions, 6 of which read two or more pages
in one session. The audit row carries the reference that was fetched but not
where it came from, so this is dereference appetite rather than traversal. It is
still the appetite this fixture failed to elicit across 76 episodes, which is
the clearest single sign that what the runs measured is the fixture rather than
the platform.

## What it settles for the decision that motivated the ticket

Nothing yet. The ticket's floor branch said that if agents do not traverse, the
split steering (the #705 oversize suggestion at 16 KiB or 12 headings, and the
baseline instruction to prefer several focused cross-linked pages behind a thin
index) fragments answers behind links agents will not follow. These runs cannot
reach that conclusion, because they never posed a task where a link was the way
through.

The one thing they bear on is the cost side of that decision: at this corpus
size splitting a page strands nothing, since every piece stays one query away
and episodes read several of them per pass. Whether the graph earns its keep
when the task needs content the asker cannot name is what a second probe has to
answer.

## What it does not establish

- **Anything at corpus scales this fixture did not test.** The whole result is
  scale-conditioned. A corpus of thousands of pages, where a limit of 25 is a
  rounding error, might well force traversal; this probe says nothing about it,
  and neither does it license the assumption that the behavior would change.
- **That the reference graph is unused generally.** The portal's graph view, the
  reverse lookup, and backlinks are read by people and by other code paths. This
  measured one thing: whether an agent answering a question dereferences an edge
  it could only have learned from a page.
- **A capability claim.** Two tiers, one client (claude-cli), one model
  generation, k=3. The correctness columns (opus 33/36 across three runs, haiku
  22/40 across four) are a by-product, and haiku's variance between runs is
  large: it answered UNAVAILABLE without fetching anything in 11 of its 40
  episodes, including one depth-0 control.
- **A search-quality claim.** The corpus was authored until the gate passed, so
  the ranking it produced is not a sample of anything.

## Instrument lessons

**A fixture gate must be run at the limit the agent will use, not the tool
default.** The pre-stated gate checked one query per cell at the default limit
and passed; the episodes defeated it on their first call by asking for more
results. A future gate for any burial-shaped fixture has to sweep the limit, and
ought to sweep query phrasings too, since the agent writes its own.

**A one-query gate cannot certify a fixture whose subject the agent will
rephrase.** In the depth-2 cell the answer page never surfaced for the gate's
phrasing and surfaced immediately for the agent's.

## The depth-3 fixture defect, and the runs it superseded

The first three runs used a depth-3 cell whose ground truth was wrong. The
question asked how long before the incident must *reach* the duty manager, and
the matrix's amber second-rung figure that the cell named is how long it may sit
*with* the duty manager once there; the correct answer to the question as asked
was the first-rung figure. An overnight rule that doubled non-red clocks made it
ambiguous on top of that, and the nightly job puts every episode inside the
overnight window.

Opus answered 20 in 3/3 and was graded wrong; haiku answered 40, doubling for
the overnight rule, which was a better reading of the corpus than the cell's
own. The cell was rephrased to ask about the clock while the incident is with
the duty manager, and the overnight rule was narrowed to the standard route.

`gt-haiku-20260809-015944` and `gt-opus-20260809-020914` ran on the repaired
cell. `gt-haiku-20260809-022815` and `gt-opus-20260809-023739` ran on the
committed fixture, which differs from that pair by the US spelling of three
words in three page summaries, normalized by the repository's spelling gate.
They were re-run rather than footnoted so that the fixture in the tree is
exactly the fixture that produced the current numbers.

Every superseded run is kept. Their correctness columns are not interpretable
for `gt-d3-ledger`; their traversal readings are, and they are pooled above.

## Reproducing

```bash
cd bench/results/graph-traversal-probe
python3 graph-traversal-analyze.py
```

Offline, stdlib only: the analyzer reads the transcripts committed here and
recomputes every number above. `cd bench && go run ./graphprobe -mode reread -out
results/graph-traversal-probe/<run>` recomputes the same readings through the
harness's own classifier; the two agree.
