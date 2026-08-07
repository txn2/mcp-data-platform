import type { Resolution } from "./resolve";
import { ExplorerToolbar, type ExplorerCounts } from "./ExplorerToolbar";
import { ExplorerGroups } from "./ExplorerGroups";
import { ExplorerAside } from "./ExplorerAside";
import type { Bucket } from "./tints";
import type { PersonaDraft, Scope, StatusFilter, Item } from "./types";

// PermissionsExplorer is the persona editor's live-preview surface: the center
// tool/connection list (grouped by kind, filterable, with per-item and
// per-group allow/deny actions) and the right rail's summary, resolution
// trace, and quick templates. Extracted from PersonaEditor.tsx (#766) and split
// into toolbar/groups/aside (#1206); it reads resolved state and routes edits
// through the passed-in handlers.
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
  counts: ExplorerCounts;
  grouped: [string, Item[]][];
  highlightRule: { bucket: Bucket; pattern: string } | null;
  allowList: string[];
  denyList: string[];
  addAllow: (pattern: string) => void;
  addDeny: (pattern: string) => void;
  addMany: (bucket: Bucket, patterns: string[]) => void;
}) {
  const focusItem = selected ?? hovered;
  const focusResolution = focusItem ? resolved.get(focusItem) : null;
  const focusItemMeta = focusItem ? items.find((i) => i.key === focusItem) : null;

  return (
    <div className="flex flex-1 flex-col xl:grid xl:min-h-0 xl:grid-cols-[minmax(0,1fr)_340px]">
      <section className="flex flex-col xl:min-h-0 xl:overflow-hidden">
        <ExplorerToolbar
          personaName={draft.displayName}
          counts={counts}
          scope={scope}
          onScopeChange={(s) => {
            setScope(s);
            setSelected(null);
            setHovered(null);
          }}
          toolCount={toolCount}
          connectionCount={connectionCount}
          search={search}
          onSearchChange={setSearch}
          statusFilter={statusFilter}
          onStatusFilterChange={setStatusFilter}
        />
        <fieldset disabled={isReadOnly} className="contents">
          <ExplorerGroups
            grouped={grouped}
            resolved={resolved}
            statusFilter={statusFilter}
            search={search}
            scope={scope}
            selected={selected}
            setSelected={setSelected}
            setHovered={setHovered}
            highlightRule={highlightRule}
            handlers={{ addAllow, addDeny, addMany }}
          />
        </fieldset>
      </section>

      <ExplorerAside
        counts={counts}
        focusItem={focusItem}
        focusResolution={focusResolution}
        focusItemMeta={focusItemMeta}
        allowList={allowList}
        denyList={denyList}
        scope={scope}
        isReadOnly={isReadOnly}
        onUpdate={onUpdate}
      />
    </div>
  );
}
