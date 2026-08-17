// Where a unified-search result opens.
//
// The hub renders results from every federated source, and each source has its
// own home: a portal viewer, a route under Activity, or a tab of the hub
// itself. Deciding that here rather than inside the hub keeps one table of
// destinations that a new source is added to, and keeps the component from
// growing a branch per source.

import type { SearchHit } from "@/api/portal/types";
import { entityHref } from "@/lib/entityRefs";
import { myCallPath, mySessionPath } from "@/pages/activity/routes";
import type { Tab } from "@/pages/knowledge/hubHash";

/**
 * HitDestination is where a result opens: an in-app route, one of the hub's own
 * tabs, or nowhere — the sources with no portal surface (catalog entities, API
 * endpoints, connections), whose drawer shows their metadata instead.
 */
export type HitDestination = { href: string } | { tab: Tab } | null;

/** hitDestination returns where one search result opens. */
export function hitDestination(hit: SearchHit): HitDestination {
  switch (hit.source) {
    case "assets":
      return { href: `/assets/${hit.ref}` };
    case "prompts":
      return { href: `/prompts/${hit.ref}` };
    case "knowledge_pages": {
      // Deep-link to the page's own URL so a search result opens the same
      // shareable detail route as any other reference, through the shared
      // entityHref builder so its safe-id guard applies here too (#709).
      const href = entityHref("knowledge_page", hit.ref);
      return href ? { href } : null;
    }
    case "sessions":
      return { href: mySessionPath(hit.ref) };
    case "calls":
      return { href: myCallPath(hit.ref) };
    case "memory":
      return { tab: "memory" };
    case "insights":
      return { tab: "insights" };
    default:
      return null;
  }
}
