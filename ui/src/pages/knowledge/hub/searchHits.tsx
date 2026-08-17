import { ChevronRight } from "lucide-react";

import type { SearchHit } from "@/api/portal/types";
import { UrnBadge, uniqueUrns } from "@/components/knowledge/UrnBadge";
import { DrawerShell } from "@/components/patterns/DrawerShell";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

// Human labels for the federated sources the unified search returns, so the
// grouped result set reads in product language rather than provider keys.
const SOURCE_LABELS: Record<string, string> = {
  datahub: "Catalog (DataHub)",
  knowledge_pages: "Knowledge pages",
  memory: "Memory",
  insights: "Insights",
  assets: "Assets",
  prompts: "Prompts",
  scripts: "Managed scripts",
  endpoints: "API endpoints",
  connections: "Connections",
};

export function sourceLabel(source: string): string {
  return SOURCE_LABELS[source] ?? source;
}

// Sources the hub can open to a detail surface, and the action label. Sources
// absent here (datahub, endpoints, connections) have no portal viewer, so their
// drawer shows metadata only.
const OPEN_ACTIONS: Record<string, string> = {
  assets: "Open asset",
  prompts: "Open prompt",
  knowledge_pages: "Open page",
  memory: "View in Memory",
  insights: "View in Insights",
};

export function HitRow({ hit, onClick }: { hit: SearchHit; onClick: () => void }) {
  return (
    <Button
      type="button"
      variant="outline"
      onClick={onClick}
      className="h-auto w-full items-start justify-between gap-2 whitespace-normal px-3 py-3 text-left font-normal hover:border-primary/40"
    >
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex items-start justify-between gap-2">
          <p className="text-sm">{hit.text}</p>
          {hit.status && (
            <Badge variant="muted" className="shrink-0 text-[11px]">
              {hit.status}
            </Badge>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
          <span className="max-w-[18rem] truncate font-mono" title={hit.ref}>
            {hit.ref}
          </span>
          {uniqueUrns(hit.entity_urns)
            .slice(0, 2)
            .map((urn) => (
              <UrnBadge key={urn} urn={urn} />
            ))}
        </div>
      </div>
      <ChevronRight className="mt-0.5 shrink-0 text-muted-foreground" />
    </Button>
  );
}

// HitDetailDrawer shows a result's metadata in a right slide-over and, when the
// source has a portal surface, a button to open the full item.
export function HitDetailDrawer({
  hit,
  onClose,
  onOpen,
}: {
  hit: SearchHit;
  onClose: () => void;
  onOpen: (hit: SearchHit) => void;
}) {
  const openLabel = OPEN_ACTIONS[hit.source];
  return (
    <DrawerShell
      title={sourceLabel(hit.source)}
      onClose={onClose}
      footer={
        openLabel && (
          <Button className="w-full" onClick={() => onOpen(hit)}>
            {openLabel}
          </Button>
        )
      }
    >
      <h3 className="text-base font-semibold">{hit.text}</h3>
      <dl className="space-y-3 text-sm">
        <div>
          <dt className="text-xs text-muted-foreground">Reference</dt>
          <dd className="break-all font-mono text-xs">{hit.ref}</dd>
        </div>
        {hit.status && (
          <div>
            <dt className="text-xs text-muted-foreground">Status</dt>
            <dd>{hit.status}</dd>
          </div>
        )}
        {hit.dimension && (
          <div>
            <dt className="text-xs text-muted-foreground">Category</dt>
            <dd>{hit.dimension}</dd>
          </div>
        )}
        {uniqueUrns(hit.entity_urns).length > 0 && (
          <div>
            <dt className="text-xs text-muted-foreground">Linked entities</dt>
            <dd className="flex flex-wrap gap-1">
              {uniqueUrns(hit.entity_urns).map((urn) => (
                <UrnBadge key={urn} urn={urn} />
              ))}
            </dd>
          </div>
        )}
      </dl>
      {!openLabel && (
        <p className="text-xs text-muted-foreground">
          {hit.source === "datahub"
            ? "This knowledge lives on the entity in the DataHub catalog."
            : "This result does not have a detail page in the portal."}
        </p>
      )}
    </DrawerShell>
  );
}
