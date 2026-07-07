import { Search } from "lucide-react";
import { cn } from "@/lib/utils";
import { matchPattern, type Resolution } from "./resolve";
import { ScopeTab, TemplateRow } from "./primitives";
import { ItemRow } from "./ItemRow";
import { Trace } from "./Trace";
import type { PersonaDraft, Scope, StatusFilter, Item } from "./types";

// PermissionsExplorer is the persona editor's live-preview surface: the center
// tool/connection list (grouped by kind, filterable, with per-item and
// per-group allow/deny actions) and the right rail's summary, resolution
// trace, and quick templates. Extracted from PersonaEditor.tsx (#766); it
// reads resolved state and routes edits through the passed-in handlers.
export function PermissionsExplorer({
  draft,
  onUpdate,
  isReadOnly,
  scope,
  setScope,
  statusFilter,
  setStatusFilter,
  search,
  setSearch,
  selected,
  setSelected,
  hovered,
  setHovered,
  toolCount,
  connectionCount,
  items,
  resolved,
  counts,
  grouped,
  highlightRule,
  allowList,
  denyList,
  addAllow,
  addDeny,
  addMany,
}: {
  draft: PersonaDraft;
  onUpdate: (partial: Partial<PersonaDraft>) => void;
  isReadOnly: boolean;
  scope: Scope;
  setScope: (s: Scope) => void;
  statusFilter: StatusFilter;
  setStatusFilter: (f: StatusFilter) => void;
  search: string;
  setSearch: (s: string) => void;
  selected: string | null;
  setSelected: React.Dispatch<React.SetStateAction<string | null>>;
  hovered: string | null;
  setHovered: React.Dispatch<React.SetStateAction<string | null>>;
  toolCount: number;
  connectionCount: number;
  items: Item[];
  resolved: Map<string, Resolution>;
  counts: { allowed: number; denied: number; total: number };
  grouped: [string, Item[]][];
  highlightRule: { bucket: "allow" | "deny"; pattern: string } | null;
  allowList: string[];
  denyList: string[];
  addAllow: (pattern: string) => void;
  addDeny: (pattern: string) => void;
  addMany: (bucket: "allow" | "deny", patterns: string[]) => void;
}) {
  const focusItem = selected ?? hovered;
  const focusResolution = focusItem ? resolved.get(focusItem) : null;
  const focusItemMeta = focusItem
    ? items.find((i) => i.key === focusItem)
    : null;

  return (
    <div className="flex flex-1 flex-col xl:grid xl:min-h-0 xl:grid-cols-[minmax(0,1fr)_340px]">
      {/* ── CENTER: Tool / Connection explorer ── */}
      <section className="flex flex-col xl:min-h-0 xl:overflow-hidden">
        <div className="border-b bg-muted/10 px-5 pt-4 pb-3">
          <div className="mb-3">
            <h3 className="text-base font-semibold leading-tight">
              What can {draft.displayName || "this persona"} do?
            </h3>
            <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px]">
              <p className="text-muted-foreground">
                Live preview. Updates as you edit allow / deny patterns.
              </p>
              <div className="flex items-center gap-3">
                <span className="flex items-center gap-1.5">
                  <span className="h-2 w-2 rounded-full bg-emerald-500" />
                  <strong className="font-mono text-foreground">
                    {counts.allowed}
                  </strong>
                  <span className="text-muted-foreground">allowed</span>
                </span>
                <span className="flex items-center gap-1.5">
                  <span className="h-2 w-2 rounded-full bg-rose-500" />
                  <strong className="font-mono text-foreground">
                    {counts.denied}
                  </strong>
                  <span className="text-muted-foreground">denied</span>
                </span>
              </div>
            </div>
          </div>
          <div className="flex border-b -mb-3">
            <ScopeTab
              active={scope === "tools"}
              count={toolCount}
              label="Tools"
              onClick={() => {
                setScope("tools");
                setSelected(null);
                setHovered(null);
              }}
            />
            <ScopeTab
              active={scope === "connections"}
              count={connectionCount}
              label="Connections"
              onClick={() => {
                setScope("connections");
                setSelected(null);
                setHovered(null);
              }}
            />
          </div>
        </div>

        <div className="flex items-center gap-2 border-b px-5 py-2.5">
          <div className="relative flex-1">
            <Search className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={`Search ${scope}…`}
              className="w-full rounded-md border bg-background py-1.5 pr-2.5 pl-8 font-mono text-[11px] outline-none ring-ring focus:ring-2"
            />
          </div>
          <div className="flex rounded-md border bg-background p-0.5">
            {(["all", "allowed", "denied"] as const).map((f) => (
              <button
                key={f}
                onClick={() => setStatusFilter(f)}
                className={cn(
                  "rounded px-2 py-0.5 text-[11px] transition-colors",
                  statusFilter === f
                    ? "bg-muted font-medium text-foreground"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                {f === "all"
                  ? "All"
                  : f === "allowed"
                    ? counts.allowed
                    : counts.denied}
                {f !== "all" && " "}
                {f !== "all" && f}
              </button>
            ))}
          </div>
        </div>

        <fieldset disabled={isReadOnly} className="contents">
          <div className="px-5 py-3 xl:flex-1 xl:overflow-y-auto">
            {grouped.length === 0 && (
              <div className="py-12 text-center text-xs text-muted-foreground">
                No {scope} match
              </div>
            )}
            {grouped.map(([kind, kindItems]) => {
              const filtered = kindItems.filter((it) => {
                const res = resolved.get(it.key);
                if (!res) return false;
                if (statusFilter === "allowed" && res.decision !== "allow")
                  return false;
                if (statusFilter === "denied" && res.decision === "allow")
                  return false;
                if (search) {
                  const q = search.toLowerCase();
                  if (
                    !it.primary.toLowerCase().includes(q) &&
                    !it.secondary.toLowerCase().includes(q)
                  )
                    return false;
                }
                return true;
              });
              if (filtered.length === 0) return null;
              const kindAllow = kindItems.filter(
                (it) => resolved.get(it.key)?.decision === "allow",
              ).length;
              // Prefer the concise `${kind}_*` glob ONLY when it actually
              // matches every item in the group (native toolkits, whose tools
              // are kind-prefixed) — it stays correct as future tools are
              // added. Gateway tools ("mcptest__echo") and connection names
              // ("Test API") are not kind-prefixed, so fall back to granting
              // each item by exact name. Either way the action covers the whole
              // kind group, independent of the search/status filter.
              const kindGlob = `${kind}_*`;
              const globCoversAll = kindItems.every((it) =>
                matchPattern(kindGlob, it.primary),
              );
              const groupNames = kindItems.map((it) => it.primary);
              const noun = scope === "tools" ? "tool" : "connection";
              return (
                <div key={kind} className="mb-5 last:mb-0">
                  <div className="mb-1.5 flex items-baseline justify-between border-b pb-1">
                    <div className="flex items-baseline gap-2">
                      <h4 className="text-xs font-semibold uppercase tracking-wider">
                        {kind}
                      </h4>
                      <span className="font-mono text-[10px] text-muted-foreground">
                        {kindAllow}/{kindItems.length} allowed
                      </span>
                    </div>
                    <div className="flex gap-1">
                      <button
                        onClick={() =>
                          globCoversAll
                            ? addAllow(kindGlob)
                            : addMany("allow", groupNames)
                        }
                        className="rounded px-1.5 py-0.5 font-mono text-[10px] text-emerald-700 hover:bg-emerald-50 dark:text-emerald-400 dark:hover:bg-emerald-950/40"
                        title={
                          globCoversAll
                            ? `Add allow rule: ${kindGlob}`
                            : `Allow every ${kind} ${noun} by name`
                        }
                      >
                        + allow {globCoversAll ? kindGlob : "all"}
                      </button>
                      <button
                        onClick={() =>
                          globCoversAll
                            ? addDeny(kindGlob)
                            : addMany("deny", groupNames)
                        }
                        className="rounded px-1.5 py-0.5 font-mono text-[10px] text-rose-700 hover:bg-rose-50 dark:text-rose-400 dark:hover:bg-rose-950/40"
                        title={
                          globCoversAll
                            ? `Add deny rule: ${kindGlob}`
                            : `Deny every ${kind} ${noun} by name`
                        }
                      >
                        + deny {globCoversAll ? kindGlob : "all"}
                      </button>
                    </div>
                  </div>
                  <div className="grid grid-cols-1 gap-1">
                    {filtered.map((it) => {
                      const res = resolved.get(it.key)!;
                      const isAllowed = res.decision === "allow";
                      const isHighlighted =
                        highlightRule &&
                        matchPattern(highlightRule.pattern, it.primary);
                      const isSelected = selected === it.key;
                      return (
                        <ItemRow
                          key={it.key}
                          name={it.primary}
                          secondary={it.secondary}
                          tertiary={it.tertiary}
                          allowed={isAllowed}
                          highlighted={!!isHighlighted}
                          highlightBucket={highlightRule?.bucket}
                          selected={isSelected}
                          matchedPattern={res.matchedPattern}
                          decision={res.decision}
                          onHover={(h) => {
                            if (h) setHovered(it.key);
                          }}
                          onClick={() =>
                            setSelected((cur) =>
                              cur === it.key ? null : it.key,
                            )
                          }
                          onAddPattern={(bucket) => {
                            if (bucket === "allow") addAllow(it.primary);
                            else addDeny(it.primary);
                          }}
                        />
                      );
                    })}
                  </div>
                </div>
              );
            })}
          </div>
        </fieldset>
      </section>

      {/* ── RIGHT: Summary + Trace + Templates ── */}
      <aside className="flex flex-col border-t xl:overflow-y-auto xl:border-t-0 xl:border-l">
        <div className="grid grid-cols-2 border-b">
          <div className="border-r px-4 py-3">
            <div className="text-2xl font-semibold text-emerald-600 dark:text-emerald-400">
              {counts.allowed}
            </div>
            <div className="text-[10px] uppercase tracking-wider text-muted-foreground">
              allowed
            </div>
          </div>
          <div className="px-4 py-3">
            <div className="text-2xl font-semibold text-rose-600 dark:text-rose-400">
              {counts.denied}
            </div>
            <div className="text-[10px] uppercase tracking-wider text-muted-foreground">
              denied
            </div>
          </div>
          <div className="col-span-2 px-4 pb-3">
            <div className="h-1.5 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full bg-emerald-500 transition-all"
                style={{
                  width:
                    counts.total === 0
                      ? "0%"
                      : `${(counts.allowed / counts.total) * 100}%`,
                }}
              />
            </div>
          </div>
        </div>

        <div className="border-b p-4">
          <div className="mb-2 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
            Resolution Trace
          </div>
          {!focusItem || !focusResolution ? (
            <p className="py-4 text-center text-[11px] text-muted-foreground">
              Hover or click an item to trace its decision.
            </p>
          ) : (
            <Trace
              name={focusItem}
              meta={focusItemMeta}
              resolution={focusResolution}
              hasAllow={allowList.length > 0}
              hasDeny={denyList.length > 0}
            />
          )}
        </div>

        {scope === "tools" && (
          <fieldset disabled={isReadOnly} className="block p-4">
            <div className="mb-2 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
              Quick Templates
            </div>
            <div className="space-y-1">
              <TemplateRow
                name="Administrator"
                hint="Allow everything"
                onApply={() =>
                  onUpdate({ allowTools: ["*"], denyTools: [] })
                }
              />
              <TemplateRow
                name="Read Only"
                hint="search, browse, get_*, list_*"
                onApply={() =>
                  onUpdate({
                    allowTools: [
                      "*_search",
                      "*_browse",
                      "*_get_*",
                      "*_list_*",
                      "*_describe_*",
                    ],
                    denyTools: [],
                  })
                }
              />
              <TemplateRow
                name="Analyst"
                hint="Query + catalog, no mutations"
                onApply={() =>
                  onUpdate({
                    allowTools: [
                      "trino_*",
                      "datahub_*",
                      "s3_get_*",
                      "s3_list_*",
                    ],
                    denyTools: ["*_delete_*", "*_execute"],
                  })
                }
              />
              <TemplateRow
                name="Engineer"
                hint="Everything except destructive"
                onApply={() =>
                  onUpdate({ allowTools: ["*"], denyTools: ["*_delete_*"] })
                }
              />
            </div>
          </fieldset>
        )}
      </aside>
    </div>
  );
}
