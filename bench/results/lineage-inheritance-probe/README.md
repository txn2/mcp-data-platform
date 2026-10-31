# Lineage-inheritance probe (enrichment-tuning premise probe, #2379)

Exploratory. This is the premise probe for a proposed study of the platform's response-side enrichment settings, starting with `enrichment.lineage_inheritance` (#2130). The question: when an agent queries a table whose columns carry no curation of their own but whose upstream table does, does inheriting that curation change whether the answer is right?

The decision rule was pre-registered on #2379 before any episode ran. The probe is killed at ceiling. Recomputed by `analyze.py` from the transcripts in this directory:

| Arm | Config | Correct | Unit flagged as unverified | `inherited_from` on a response |
| --- | --- | ---: | ---: | ---: |
| inherit-on | `config/platform.bench.a2.yaml` | 12/12 | 0/12 | 12/12 |
| inherit-off | `config/platform.bench.a2-noinherit.yaml` | 12/12 | 12/12 | 0/12 |

The rule's first kill condition reads "inherit-off is 11/12 or better", and the off arm scored 12/12.

## Fixture and run

- `memory.bench.orders_mirror` is a copy of `memory.bench.orders` (`seed/trino/lineage_mirror.sql`). It is registered in the catalog with no curation of its own, and one lineage edge runs from `orders` to it (`seed/postgres/lineage_mirror.sql`).
- There are three tasks in `tasks-lineage/`. Each names the mirror table and asks for a USD figure, and missing the cents convention (`orders.amount` and `orders.discount` are documented as US cents) makes the answer wrong by a factor of 100.
- Sonnet ran through the `claude-cli` adapter, k=4 per task per arm, on a `v1.152.5-dirty` build at commit `cc2c005d`. Each run's manifest records the details.

## What it establishes

- Inheritance delivers. In every inherit-on episode, `trino_describe_table` on the mirror carried the upstream column descriptions, marked with `inherited_from` naming `memory.bench.orders`.
- On this fixture, inheritance does not change accuracy. Without it, Sonnet read the unit from the data itself: integer amounts such as 276677 next to a question asking for USD rounded to the cent. It divided by 100 every time.
- Inheritance changes how certain the answer is. Every inherit-off answer said in its own words that the cents reading was an assumption and gave the whole-dollar alternative. No inherit-on answer did. The probe has no decision riding on this reading, and the rule records it as descriptive only.

## What it does not establish

- **The value of inheritance for a convention the data cannot reveal.** Integer cents are derivable from the values, and the knowledge-use report already found that the strong tier re-derives derivable knowledge whatever it is given (its sections 3 and 4). A convention the values do not show might separate the arms: what a status code means, which rows a policy excludes, a fiscal calendar. That would be a new candidate needing its own probe, and this probe does not license one.
- **Anything about weaker models or other clients.** All episodes ran on one model through one client.
- **The other enrichment settings.** `column_context_filtering`, `semantic_fallback` and `semantic_fallback_top_k` were not varied.

## Instrument leak (#2380)

The arms ran back to back on one database, and the identity pool gives each run the same identities. As a result, `search` in every inherit-off episode returned hits from the inherit-on run: the caller's own prior sessions or recorded calls. In 3 episodes, all on the average-amount task, that text said the amount is stored in cents. The kill does not depend on those 3 episodes. Of the other 9 inherit-off episodes, 8 saw no cents text in any tool result. The ninth saw it only in a column alias it had written itself (`avg_if_cents`). All 9 were correct. The leak signature (`"source":"sessions"` or `"source":"calls"` in a search result) appears in no file under any other directory of `bench/results/`, so no published run is affected. #2380 tracks resetting that state between runs.
