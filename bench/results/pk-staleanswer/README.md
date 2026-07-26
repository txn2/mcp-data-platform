# Stale answer-bearing note (#1054): the tier-exposure cell

Exploratory. The last cell of the capability-by-derivability matrix: a
stored note states an answer ("the account has three listening monitors")
that the world has since falsified (the account was emptied after the note
was planted; the truthful answer is zero, one call away). A no-knowledge
control at the same world separates trusting the stale note from any
difficulty counting an empty account.

| Run | Condition | n | Verified | Trusted | Correct |
| --- | --- | --- | --- | --- | --- |
| `pk-staleanswer-sonnet-20260725-162050` | stale note | 8 | 8 | 0 | 8 |
| | no note | 8 | 8 | 0 | 8 |
| `pk-staleanswer-haiku-20260725-163111` | stale note | 8 | 2 | 6 | **0** |
| | no note | 8 | 8 | 0 | 8 |

Sonnet is staleness-immune: it re-derived the state as it does everywhere
else and answered zero, 8 of 8. Haiku answered the note's stale value in 8
of 8 attempts: six trusted it outright with zero catalog calls, and two
made the observation, received the empty listing (`{"items":[]}` is in the
transcript), and still reported three — the stored note overrode fresh
contradictory evidence. Against its own 8-of-8-correct no-knowledge
control, the delivered stale note took haiku from perfect to zero on this
question: on the weak tier, a stale note is strictly worse than no note
at all.
