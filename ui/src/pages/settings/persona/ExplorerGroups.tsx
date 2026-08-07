import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/patterns/EmptyState";
import { matchPattern, type Resolution } from "./resolve";
import { ItemRow } from "./ItemRow";
import { BUCKET_TINT, type Bucket } from "./tints";
import type { Item, Scope, StatusFilter } from "./types";

// visibleItems narrows one kind group by the toolbar's status filter and
// search text. Kept out of the component so the group renderer stays a
// straight map over rows.
function visibleItems(
  items: Item[],
  resolved: Map<string, Resolution>,
  statusFilter: StatusFilter,
  search: string,
): Item[] {
  const q = search.toLowerCase();
  return items.filter((it) => {
    const res = resolved.get(it.key);
    if (!res) return false;
    if (statusFilter === "allowed" && res.decision !== "allow") return false;
    if (statusFilter === "denied" && res.decision === "allow") return false;
    if (!q) return true;
    return (
      it.primary.toLowerCase().includes(q) || it.secondary.toLowerCase().includes(q)
    );
  });
}

export interface ExplorerHandlers {
  addAllow: (pattern: string) => void;
  addDeny: (pattern: string) => void;
  addMany: (bucket: Bucket, patterns: string[]) => void;
}

// ExplorerGroups is the permissions list itself: every tool (or connection)
// the platform exposes, grouped by kind, with the per-group and per-item
// allow/deny actions.
export function ExplorerGroups({
  grouped,
  resolved,
  statusFilter,
  search,
  scope,
  selected,
  setSelected,
  setHovered,
  highlightRule,
  handlers,
}: {
  grouped: [string, Item[]][];
  resolved: Map<string, Resolution>;
  statusFilter: StatusFilter;
  search: string;
  scope: Scope;
  selected: string | null;
  setSelected: React.Dispatch<React.SetStateAction<string | null>>;
  setHovered: React.Dispatch<React.SetStateAction<string | null>>;
  highlightRule: { bucket: Bucket; pattern: string } | null;
  handlers: ExplorerHandlers;
}) {
  return (
    <div className="px-5 py-3 xl:flex-1 xl:overflow-y-auto">
      {grouped.length === 0 && <EmptyState>No {scope} match</EmptyState>}
      {grouped.map(([kind, kindItems]) => {
        const filtered = visibleItems(kindItems, resolved, statusFilter, search);
        if (filtered.length === 0) return null;
        return (
          <div key={kind} className="mb-5 last:mb-0">
            <KindGroupHeader
              kind={kind}
              kindItems={kindItems}
              resolved={resolved}
              scope={scope}
              handlers={handlers}
            />
            <div className="grid grid-cols-1 gap-1">
              {filtered.map((it) => {
                const res = resolved.get(it.key)!;
                return (
                  <ItemRow
                    key={it.key}
                    name={it.primary}
                    secondary={it.secondary}
                    tertiary={it.tertiary}
                    allowed={res.decision === "allow"}
                    highlighted={
                      !!highlightRule && matchPattern(highlightRule.pattern, it.primary)
                    }
                    highlightBucket={highlightRule?.bucket}
                    selected={selected === it.key}
                    matchedPattern={res.matchedPattern}
                    decision={res.decision}
                    onHover={(h) => {
                      if (h) setHovered(it.key);
                    }}
                    onClick={() =>
                      setSelected((cur) => (cur === it.key ? null : it.key))
                    }
                    onAddPattern={(bucket) => {
                      if (bucket === "allow") handlers.addAllow(it.primary);
                      else handlers.addDeny(it.primary);
                    }}
                  />
                );
              })}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function KindGroupHeader({
  kind,
  kindItems,
  resolved,
  scope,
  handlers,
}: {
  kind: string;
  kindItems: Item[];
  resolved: Map<string, Resolution>;
  scope: Scope;
  handlers: ExplorerHandlers;
}) {
  const kindAllow = kindItems.filter(
    (it) => resolved.get(it.key)?.decision === "allow",
  ).length;
  // Prefer the concise `${kind}_*` glob ONLY when it actually matches every
  // item in the group (native toolkits, whose tools are kind-prefixed) — it
  // stays correct as future tools are added. Gateway tools ("mcptest__echo")
  // and connection names ("Test API") are not kind-prefixed, so fall back to
  // granting each item by exact name. Either way the action covers the whole
  // kind group, independent of the search/status filter.
  const kindGlob = `${kind}_*`;
  const globCoversAll = kindItems.every((it) => matchPattern(kindGlob, it.primary));
  const groupNames = kindItems.map((it) => it.primary);
  const noun = scope === "tools" ? "tool" : "connection";

  const apply = (bucket: Bucket) => {
    if (!globCoversAll) {
      handlers.addMany(bucket, groupNames);
      return;
    }
    if (bucket === "allow") handlers.addAllow(kindGlob);
    else handlers.addDeny(kindGlob);
  };

  return (
    <div className="mb-1.5 flex items-baseline justify-between border-b pb-1">
      <div className="flex items-baseline gap-2">
        <h4 className="text-xs font-semibold uppercase tracking-wider">{kind}</h4>
        <span className="font-mono text-[10px] text-muted-foreground">
          {kindAllow}/{kindItems.length} allowed
        </span>
      </div>
      <div className="flex gap-1">
        {(["allow", "deny"] as const).map((bucket) => (
          <Button
            key={bucket}
            type="button"
            variant="ghost"
            size="xs"
            onClick={() => apply(bucket)}
            title={
              globCoversAll
                ? `Add ${bucket} rule: ${kindGlob}`
                : `${bucket === "allow" ? "Allow" : "Deny"} every ${kind} ${noun} by name`
            }
            className={`h-5 px-1.5 font-mono text-[10px] ${BUCKET_TINT[bucket].text}`}
          >
            + {bucket} {globCoversAll ? kindGlob : "all"}
          </Button>
        ))}
      </div>
    </div>
  );
}
