# Derivability bridge (#1054): the two-regime probe, across models and clients

Exploratory. One question whose answer requires combining fetchable trend data with an unfetchable reporting convention ("a monitor day counts as positive coverage at sentiment_score 70 or higher" — a threshold no endpoint states). The convention cell delivers it as a stored note; the control cell delivers nothing. Thresholds 50 through 80 all yield distinct day counts, so a stated answer betrays which threshold produced it; the note-only answer is 11. The control doubles as the leakage check: a control producing 11 would invalidate the probe.

| Run | Driver | Convention used | Control fabricated | Control declined | Failures |
| --- | --- | --- | --- | --- | --- |
| `pk-bridge-20260725-135349` | claude-cli, sonnet | 8/8 | 6/8 | 2/8 | 0 |
| `pk-bridge-haiku-20260725-141119` | claude-cli, haiku | 5/6 | 6/6 | 0/6 | 4 episodes lost to API 529 Overloaded |
| `pk-bridge-api-20260725-152744` | raw Messages API, claude-sonnet-5 | 8/8 | 4/8 | 4/8 | 0 |

Zero leakage in any run: no control ever produced 11. Fabricating controls in every run picked 50 as a "neutral midpoint" and answered 15.

## What the three runs establish together

Non-derivable conventions are used by every model and driver tested (21 of 22 clean convention episodes). Held against the answer sweep in this same fixture — where sonnet showed zero reliance on notes whose content it could re-derive — this is the two-regime result under a single controlled environment: reliance on stored knowledge is governed by derivability, and (from the cost sweep) not by the price of checking.

Without the convention, fabrication is the norm, not the exception: 16 of 22 clean control episodes invented a plausible threshold and answered confidently, and on haiku no control ever declined. A delivered convention therefore does double duty: it is used, and it suppresses confident invention of institutional definitions.

The raw-API run kills the client confound for the convention result the same way the answer-sweep API run kills it for the null-delivery result: the pattern survives with no client in the loop.
