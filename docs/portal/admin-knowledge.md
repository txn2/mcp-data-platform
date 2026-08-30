---
description: Reviewing and promoting captured knowledge, with the warehouse observation beside each claim.
---

# Knowledge Review

The separate admin Knowledge & Memory page was merged into the unified **Knowledge** page in the user portal (see [Portal User Guide](knowledge.md)). Review and promotion gate on the `apply_knowledge` capability, not an admin role: whoever holds the tool sees the review surfaces inside the Knowledge page, whether or not they are an admin.

Inside the Knowledge page, `apply_knowledge` holders get:

- **Review queue** (Insights tab) - All captured insights across users, with status/category/confidence filters and an insight detail drawer (full metadata, entity URNs, suggested actions, related columns, review notes, approve/reject actions). A pending-review count is badged on the sidebar Knowledge item and the Insights tab.
- **Changesets** (Knowledge tab) - The record of insights promoted into knowledge: the target DataHub URN or knowledge page, change type, who applied it, and status, with rollback to revert applied changes. They sit with the promoted knowledge rather than with the unpromoted insights in the review pipeline.

Opening an insight from the review queue shows the full review drawer: the captured statement, entity URNs, suggested catalog actions, related columns, the capture/review/apply audit trail, and approve/reject controls.

![Insight review](../images/screenshots/light/admin-knowledge-insight-detail-light.webp#only-light)![Insight review](../images/screenshots/dark/admin-knowledge-insight-detail-dark.webp#only-dark)

## Observed warehouse state

A claim about an entity the platform can query is one the platform can check for itself. When a pending insight's entity URN resolves through the configured query provider to an available table, the drawer puts that observation under the claim: what the entity is queryable as, which connection it lives on, and how many rows it currently holds.

![Insight review with observed warehouse state](../images/screenshots/light/admin-knowledge-insight-observed-light.webp#only-light)![Insight review with observed warehouse state](../images/screenshots/dark/admin-knowledge-insight-observed-dark.webp#only-dark)

When the claim states a number and the table estimates a different one, the drawer carries an advisory marker naming both. It is advisory only: an estimate is an estimate, the number in the claim may be about something else entirely, and approve and reject stay exactly as available as before. Nothing is ever refused mechanically.

![Insight review with a claim conflict](../images/screenshots/light/admin-knowledge-insight-conflict-light.webp#only-light)![Insight review with a claim conflict](../images/screenshots/dark/admin-knowledge-insight-conflict-dark.webp#only-dark)

Row estimation is off by default, because `COUNT(*)` can scan a whole table (`enrichment.estimate_row_counts`, see [Configuration](../server/configuration.md)). With it off, the block still states that the entity exists and is queryable, and claims no count on the platform's behalf.

![Insight review without a row estimate](../images/screenshots/light/admin-knowledge-insight-no-estimate-light.webp#only-light)![Insight review without a row estimate](../images/screenshots/dark/admin-knowledge-insight-no-estimate-dark.webp#only-dark)

The block is absent, rather than empty, whenever there is nothing to show: a decided insight, an entity URN the provider cannot resolve, a table it reports unavailable, a slow warehouse, and a deployment with no query provider all render the drawer exactly as it was.

The Memory tab is personal to each user; there is no all-user memory view, because the only memory that crosses between users is an insight (handled in the review queue above).

