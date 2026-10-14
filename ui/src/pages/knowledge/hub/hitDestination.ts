// Where a unified-search result opens.
//
// The hub renders results from every federated source, and each source has its
// own home: a portal viewer, a route under Activity, or a tab of the hub
// itself. Deciding that here rather than inside the hub keeps one table of
// destinations that a new source is added to, and keeps the component from
// growing a branch per source.

import type { SearchHit } from "@/api/portal/types";
import { catalogHref, entityHref } from "@/lib/entityRefs";
import { myCallPath, mySessionPath } from "@/pages/activity/routes";
import type { Tab } from "@/pages/knowledge/hubHash";

/**
 * HitDestination is where a result opens: an in-app route, one of the hub's own
 * tabs, or nowhere — the sources with no portal surface (API endpoints,
 * connections), whose drawer shows their metadata instead.
 */
export type HitDestination = { href: string } | { tab: Tab } | null;

// catalogDestination opens a dataset or a definition on its Catalog tab,
// through the one catalog link builder, which refuses anything that is not a
// catalog reference.
function catalogDestination(ref: string): HitDestination {
  const href = catalogHref(ref);
  return href ? { href } : null;
}

// DESTINATIONS maps each source with a portal surface to where its result
// opens; a source absent here has none.
const DESTINATIONS: Record<string, (ref: string) => HitDestination> = {
  assets: (ref) => ({ href: `/assets/${ref}` }),
  prompts: (ref) => ({ href: `/prompts/${ref}` }),
  // Deep-link to the page's own URL so a search result opens the same
  // shareable detail route as any other reference, through the shared
  // entityHref builder so its safe-id guard applies here too (#709).
  knowledge_pages: (ref) => {
    const href = entityHref("knowledge_page", ref);
    return href ? { href } : null;
  },
  sessions: (ref) => ({ href: mySessionPath(ref) }),
  calls: (ref) => ({ href: myCallPath(ref) }),
  memory: () => ({ tab: "memory" }),
  insights: () => ({ tab: "insights" }),
  catalog: catalogDestination,
  governance: catalogDestination,
};

/** hitDestination returns where one search result opens. */
export function hitDestination(hit: SearchHit): HitDestination {
  return DESTINATIONS[hit.source]?.(hit.ref) ?? null;
}
