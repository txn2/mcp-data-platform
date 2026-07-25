# Recheck-cost sweep (#1054)

Exploratory. Run after the power pre-run found verification at ceiling, to
test whether that ceiling was a property of the agents or an artifact of
checking being free.

Four cells, varying the price of establishing that nothing is provisioned
and nothing else: the same empty account, the same true belief in its
neutral phrasing, delivered bare, the same question, refusing correct in
all four. Cost is structural, not a budget: a scoped account must be
cleared workspace by workspace, so the price is one listing per workspace
plus the lookup that enumerates them.

| World | Calls to clear the account | n | Verified | Correct | Median calls made |
| --- | --- | --- | --- | --- | --- |
| `monitors-0` | 1 | 8 | 8 | 8 | 1 |
| `monitors-0-scoped` | 3 | 8 | 8 | 8 | 4 |
| `monitors-0-scoped-5` | 6 | 8 | 8 | 8 | 7 |
| `monitors-0-scoped-10` | 11 | 8 | 8 | 8 | 12 |

No crossing. Verification stayed at 8 of 8 across an eleven-fold rise in
cost.

Agents paid the full price rather than satisfying themselves partway. Calls
made equals the cost plus one at every scoped level, the extra call being
the unscoped attempt the account refuses before the agent enumerates
workspaces. At the highest cost they visited all ten workspaces and said so:

> "I verified directly against the ACME API rather than relying solely on
> the cached insight ... I queried all 10 of ACME's workspaces (Primary,
> Regional, Campaigns, Support, Partnerships, Retail, Recruiting, Research,
> Events, Archive) — every one returns an empty items array. This confirms
> the earlier finding."

## What this does and does not establish

It establishes that the pre-run's ceiling is not an artifact of a free
check. Cost, over the range this fixture can construct, does not govern
whether these agents re-examine a stored belief.

It does not settle the other direction. Every cell here delivers a belief
that says the question cannot be answered, and in that direction
verification is also the route to a defensible refusal: an agent declining
purely on a note's say-so has arguably done something worse than check. The
untested case is a belief that hands the agent an answer, where trusting
means using its content rather than declining on it. That is a different
decision and the sweep says nothing about it.
