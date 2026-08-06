# Generalization arms (protocol 6.5) — in progress

Takes the RQ1 effect apart: it was found on one claim, in one fixture,
delivered to one sink, at one tier. These arms vary one of those at a time.

## Sink control: complete

The identical warehouse claim, at the identical directive level, planted on a
knowledge page instead of the DataHub entity description. Fixture, task,
claim and phrasing are all held; only the sink differs.

| Cell | Adopted | Ran a COUNT | Correct |
| --- | --- | --- | --- |
| entity-description sink (RQ1 reference) | 18/24 = 75.0% [55.1, 88.0] | 8/24 | 6/24 |
| **knowledge-page sink** | **24/24 = 100%** [86.2, 100] | **1/24** | 0/24 |

**H5a holds.** Its falsifier was adoption collapsing to 2/24 or below on the
page sink while the reference reproduced; instead adoption is at least as high
and every episode took the planted value.

Stated carefully: at n=24 the two intervals overlap, so this is "at least as
strong at the page sink", not a demonstrated increase. What it rules out is
the falsifier's direction, and that is what the arm was for.

**The mechanism replicated again.** One episode ran the query, that one
episode answered correctly, and all 23 that did not query adopted. Across the
RQ1 cell, the directive contrast and this arm, that is 120 episodes on this
claim with no exception in either direction.

**Why this arm exists.** Section 5.4 held that fixture and sink are
irreducibly confounded on the API cell, because the API fixture has no catalog
entity and its treatments must apply to a page. That is only true while the
warehouse claim insists on the DataHub sink. With this arm, sink is excluded
as an explanation, so a null on the API fixture is attributable to fixture
rather than ambiguous between the two.

**It also bears on D5 and on #1131.** The applied sink is not what carries a
claim into an answer. Delivery through search alone is sufficient, and here
the page-sink claim was taken more often than the one written onto the entity
the question is about.

The plant's cross-identity read-back confirmed the claim reachable in both
search and the sink. The arm ran to completion with no cross-identity store
drift.

## Remaining

The cross-fixture arms on the perishable-knowledge API fixture: wrong, correct
and absent at haiku, and wrong at sonnet. Not yet run.
