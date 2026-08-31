import { useState } from "react";
import { useInsightStats } from "@/api/admin/hooks";
import { useAuthStore } from "@/stores/auth";
import type { SearchHit } from "@/api/portal/types";
import { hitDestination } from "@/pages/knowledge/hub/hitDestination";
import { KnowledgePagesPage } from "@/pages/knowledge-pages/KnowledgePagesPage";
import { CatalogSection } from "@/pages/knowledge/CatalogSection";
import { useDataHubConnections } from "@/api/portal/datahub";
import {
  MyKnowledgeSection,
  MyMemorySection,
} from "@/pages/knowledge/MyKnowledgePage";
import {
  KnowledgeCaptureTab,
  ChangesetsTab,
} from "@/pages/knowledge/KnowledgePage";
import { InfoHint } from "@/components/patterns/InfoHint";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { SubTabBar } from "@/pages/knowledge/hub/SubTabBar";
import { UnifiedSearch } from "@/pages/knowledge/hub/UnifiedSearch";

import {
  insightSubHash,
  normalizeInsightSub,
  normalizeTab,
  type InsightSubTab,
  type Tab,
} from "./hubHash";

const TABS: { key: Tab; label: string }[] = [
  { key: "knowledge", label: "Knowledge" },
  { key: "insights", label: "Insights" },
  { key: "memory", label: "Memory" },
];

// The Knowledge tab is itself split into sub-tabs so federated search, the page
// browse, the catalog, and changesets each get their own space and explanation
// rather than stacking on one screen. The row stops at four because every
// DataHub-backed surface lives inside Catalog (#1194); anything the portal's own
// database backs stays outside it.
type KnowledgeSubTab = "search" | "pages" | "catalog" | "changesets";

// subRoutePath maps the URL-addressable sub-tabs (#709, #719) to their
// first-class routes. Sub-tabs not listed here live in-page under /knowledge and
// are carried in the URL hash instead.
const subRoutePath: Partial<Record<KnowledgeSubTab, string>> = {
  pages: "/knowledge/pages",
  catalog: "/knowledge/catalog",
};

// SubTabMeta is one Knowledge sub-tab: its key, its label, and the explanation
// shown under the tab bar.
interface SubTabMeta {
  key: KnowledgeSubTab;
  label: string;
  description: string;
}

// knowledgeSubTabsFor lists the Knowledge sub-tabs available to this reader:
// Catalog only where a DataHub connection exists, and Changesets only for a
// reviewer (it is the apply audit). It sits outside the component because it is
// a pure function of those two capabilities.
function knowledgeSubTabsFor({
  hasDataHub,
  canApply,
}: {
  hasDataHub: boolean;
  canApply: boolean;
}): SubTabMeta[] {
  return [
    {
      key: "search",
      label: "Search All",
      description:
        "The same discovery your agent uses to find what the platform already knows, surfaced here for you to audit, review, and reference. One query fans across every source it can access (the DataHub catalog, knowledge pages, memory, captured insights, saved assets, prompts, API endpoints, and connections), grouped by source. It ranks semantically when an embedding provider is configured and falls back to keyword search otherwise.",
    },
    {
      key: "pages",
      label: "Knowledge Pages",
      description:
        "Canonical business and domain knowledge, written as markdown and stored in the portal. Pages are one of the two knowledge sinks; technical and entity knowledge lives in the DataHub catalog instead. Holders of apply_knowledge can create, edit, and remove pages.",
    },
    ...(hasDataHub
      ? [
          {
            key: "catalog" as const,
            label: "Catalog",
            description:
              "Your data catalog, and the second of the two knowledge sinks: the tables it holds, the context documents attached to them, and the tag, domain, and glossary vocabularies that describe them. Pick a connection once and it applies across the whole section.",
          },
        ]
      : []),
    ...(canApply
      ? [
          {
            key: "changesets" as const,
            label: "Changesets",
            description:
              "The record of insights promoted into knowledge: the catalog and knowledge-page changes applied when your agent runs apply_knowledge. Roll back a changeset to undo its writes.",
          },
        ]
      : []),
  ];
}

/**
 * KnowledgeHub is the single home for the Memory to Insight to Knowledge
 * lifecycle (#661). It merges the former /knowledge-pages, /my-knowledge, and
 * /admin/knowledge surfaces into three capability-gated tabs:
 *
 *   - Knowledge (default): four sub-tabs - Search All (federated search),
 *     Knowledge Pages (browse internal pages), Catalog (every DataHub-backed
 *     surface, #1194), and Changesets (apply audit, reviewer-only).
 *   - Insights: your captured insights, and for apply_knowledge holders the
 *     full review queue and changesets.
 *   - Memory: your raw memory substrate classified by sink_class, and for
 *     apply_knowledge holders every user's memory.
 *
 * Review and promote affordances gate on the apply_knowledge capability, never
 * on an admin role.
 */
export function KnowledgeHub({
  initialTab,
  initialPageId,
  routeSub,
  onNavigate,
}: {
  initialTab?: string;
  // The knowledge page open in detail, from the /knowledge/pages/:id route (#709).
  initialPageId?: string;
  // The sub-tab pinned by a first-class route (#709, #719): "pages" for
  // /knowledge/pages, "catalog" for /knowledge/catalog. Undefined under the bare
  // /knowledge route, where the sub-tab comes from in-page state / the URL hash.
  routeSub?: KnowledgeSubTab;
  onNavigate?: (path: string) => void;
}) {
  // On a sub-tab route the top tab is always Knowledge; otherwise it comes from
  // the URL hash. A routed sub-tab is URL-driven, so it is never stored in
  // knowledgeSub, which only ever holds the in-page sub-tabs (search/changesets).
  const onRoute = routeSub != null;
  const [tab, setTab] = useState<Tab>(() => (onRoute ? "knowledge" : normalizeTab(initialTab)));
  // DataHub connections gate the Catalog sub-tab: on a deployment with no
  // DataHub configured the query returns [], so the tab is hidden rather than
  // rendering an empty, non-functional body.
  const hasDataHub = (useDataHubConnections().data?.length ?? 0) > 0;
  // The pages sub-tab is URL-driven (a /knowledge/pages route); the in-page sub-tabs
  // can be carried in the hash (e.g. /knowledge#changesets) so leaving the pages
  // route opens the chosen one directly rather than defaulting to Search (#709).
  const [knowledgeSub, setKnowledgeSub] = useState<KnowledgeSubTab>(() =>
    initialTab === "changesets" || initialTab === "search" ? initialTab : "search",
  );
  const [insightSub, setInsightSub] = useState<InsightSubTab>(() =>
    onRoute ? "mine" : normalizeInsightSub(initialTab),
  );
  // Review and promote affordances gate on the apply_knowledge capability (not
  // an admin role), or admin. This mirrors the REST handler's userHasToolAccess:
  // the capability grants non-admins, and admins are allowed too since the tool
  // may be unregistered on a deployment.
  const canApply = useAuthStore(
    (s) => (s.user?.tools?.includes("apply_knowledge") ?? false) || s.isAdmin(),
  );
  const isAdmin = useAuthStore((s) => s.isAdmin());

  // Pending-review cue: the team-wide pending count comes from the admin-scoped
  // insight-stats endpoint, so the fetch is gated on isAdmin to avoid a 401 poll
  // for a non-admin reviewer (whose team queue is, today, also admin-gated; see
  // #662). The badge shows the count to admins; other users see no number.
  const insightStats = useInsightStats({ enabled: isAdmin });
  const pendingReviews = isAdmin ? (insightStats.data?.total_pending ?? 0) : 0;

  const knowledgeSubTabs = knowledgeSubTabsFor({ hasDataHub, canApply });
  // A routed sub-tab is selected by the URL; the others by in-page state. Honor
  // the routed sub-tab only when it is actually available (e.g. a deep-link to
  // /knowledge/catalog on a deployment with no DataHub connections falls back to
  // Search rather than selecting a tab that is not rendered).
  const available = (key: KnowledgeSubTab) => knowledgeSubTabs.some((s) => s.key === key);
  const activeSub: KnowledgeSubTab =
    routeSub && available(routeSub)
      ? routeSub
      : available(knowledgeSub)
        ? knowledgeSub
        : "search";
  const activeSubMeta = knowledgeSubTabs.find((s) => s.key === activeSub)!;

  // Insights sub-tabs. The review queue is reviewer-only and carries the
  // pending-review count.
  const insightSubTabs: {
    key: InsightSubTab;
    label: string;
    description: string;
    badge?: number;
  }[] = [
    {
      key: "mine",
      label: "My Insights",
      description:
        "The insights captured from your sessions, with their review status. An insight is a memory worth sharing with your team; it stays a proposal until it is reviewed and promoted into knowledge.",
    },
    ...(canApply
      ? [
          {
            key: "review" as const,
            label: "Review queue",
            badge: pendingReviews,
            description:
              "Your account has permission to let your agent Apply Knowledge, which promotes your team's insights into team-wide, durable knowledge (knowledge pages and the DataHub catalog). Review what your team has captured: approve the insights worth promoting and reject the rest, so when you ask your agent to apply knowledge it works from a curated set.",
          },
        ]
      : []),
  ];
  const activeInsightSub = insightSubTabs.some((s) => s.key === insightSub)
    ? insightSub
    : "mine";
  const insightSubMeta = insightSubTabs.find((s) => s.key === activeInsightSub)!;

  // Reflect the active tab in the URL hash so the view is deep-linkable and
  // survives a refresh, without forcing a full navigation. On a URL-addressable
  // sub-tab route, switching the top tab must leave that path, so navigate rather
  // than only rewriting the hash; staying on Knowledge keeps the route.
  const selectTab = (next: Tab) => {
    if (onRoute) {
      if (next !== "knowledge") onNavigate?.(`/knowledge#${next}`);
      return;
    }
    setTab(next);
    window.history.replaceState(null, "", `#${next}`);
  };

  // The Insights sub-tab is carried in the hash the same way the top tabs are,
  // so a reviewer who opened the queue can share or refresh the exact view --
  // and so the deep link in the review-queue alert email stays honest (#803).
  const selectInsightSub = (next: InsightSubTab) => {
    setInsightSub(next);
    if (onRoute) return;
    window.history.replaceState(null, "", `#${insightSubHash(next)}`);
  };

  // Pages and Catalog are URL-addressable sub-tabs (#709/#719): selecting one
  // routes to its path so deep-links and browser back/forward work. The other
  // sub-tabs (search, changesets) are in-page state under the bare /knowledge
  // route, carried in the hash so leaving a routed sub-tab for one opens it in a
  // single click.
  const selectKnowledgeSub = (next: KnowledgeSubTab) => {
    const path = subRoutePath[next];
    if (path) {
      onNavigate?.(path);
      return;
    }
    if (onRoute) {
      onNavigate?.(`/knowledge#${next}`);
      return;
    }
    setKnowledgeSub(next);
  };

  // Open a search result where that source lives: a portal viewer, a route
  // under Activity, or one of this hub's own tabs. The destination table is in
  // hitDestination, so a new source is added there rather than here.
  const openHit = (hit: SearchHit) => {
    const dest = hitDestination(hit);
    if (dest === null) return;
    if ("href" in dest) {
      onNavigate?.(dest.href);
      return;
    }
    selectTab(dest.tab);
  };

  return (
    <div className="space-y-6">
      {/* The primary tab bar is the page's only underline bar: the levels below
          it are pill bars (SubTabBar), so the three tiers of navigation read as
          a hierarchy rather than three identical rows. */}
      <Tabs value={tab} onValueChange={(v) => selectTab(v as Tab)} className="gap-6">
        <TabsList variant="line" className="w-full justify-start border-b">
          {TABS.map((t) => (
            <TabsTrigger key={t.key} value={t.key} className="flex-none px-4 py-2">
              {t.label}
              {t.key === "insights" && pendingReviews > 0 && (
                <span
                  className="rounded-full bg-primary/15 px-1.5 text-[11px] font-semibold text-primary"
                  aria-label={`${pendingReviews} insights awaiting review`}
                >
                  {pendingReviews}
                </span>
              )}
            </TabsTrigger>
          ))}
        </TabsList>

        <TabsContent value="knowledge" className="space-y-4">
          <div className="flex flex-wrap items-center gap-2">
            <SubTabBar
              tabs={knowledgeSubTabs}
              active={activeSub}
              onSelect={selectKnowledgeSub}
            />
            <InfoHint>{activeSubMeta.description}</InfoHint>
          </div>

          {activeSub === "search" && <UnifiedSearch onOpen={openHit} />}
          {activeSub === "pages" && (
            <KnowledgePagesPage openPageId={initialPageId} onNavigate={onNavigate} />
          )}
          {/* Catalog is a section, not a leaf: its own inner tabs are Tables,
              Context Docs, and Tags, addressed in the hash under this one
              route. The route pins the top tab, so the hash is free to address
              the inner one; anything it does not name opens Tables. */}
          {activeSub === "catalog" && (
            <CatalogSection initialSub={initialTab} onNavigate={onNavigate} />
          )}
          {/* Changesets live under Knowledge (the promoted layer), not Insights:
              a changeset is created only at apply time and records what was
              written, so it belongs with the knowledge it produced. */}
          {activeSub === "changesets" && <ChangesetsTab />}
        </TabsContent>

        <TabsContent value="insights" className="space-y-4">
          <div className="flex flex-wrap items-center gap-2">
            <SubTabBar
              tabs={insightSubTabs}
              active={activeInsightSub}
              onSelect={selectInsightSub}
            />
            <InfoHint>{insightSubMeta.description}</InfoHint>
          </div>

          {activeInsightSub === "mine" && <MyKnowledgeSection />}
          {activeInsightSub === "review" && <KnowledgeCaptureTab />}
        </TabsContent>

        <TabsContent value="memory" className="space-y-6">
          {/* Memory is personal. The only memory that crosses to other users is
              an insight (reviewed in the Insights tab), so this tab is scoped to
              the caller's own records. */}
          <MyMemorySection />
        </TabsContent>
      </Tabs>
    </div>
  );
}
