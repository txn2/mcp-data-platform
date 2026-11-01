import { useState, useEffect, useCallback, useMemo } from "react";
import {
  useEffectiveConnections,
  useSystemInfo,
  useConnectionsOAuthHealth,
} from "@/api/admin/hooks";
import type { ConnectionOAuthHealthSummary } from "@/api/admin/types";
import type { EffectiveConnection } from "@/api/admin/types";
import { markdownToPlainText } from "@/lib/markdownText";
import { Cable } from "lucide-react";
import { EmptyState } from "@/components/patterns/EmptyState";
import { DISCARD_PROMPT, useUnsavedChangesGuard } from "@/lib/navigationGuard";
import { FilterSelect } from "@/components/patterns/FilterSelect";
import { SearchInput } from "@/components/patterns/SearchInput";
import { AVAILABLE_KINDS } from "./connections/constants";
import {
  filterConnections,
  kindOptions,
  lacksCatalog,
  readFilter,
  writeFilter,
  type ConnectionFilter,
} from "./connections/connectionFilter";
import {
  ConnectionOAuthHealthBadge,
  GatewayHealthBadge,
  NoCatalogBadge,
} from "./connections/HealthBadges";
import { ConnectionViewer } from "./connections/ConnectionViewer";
import { ConnectionEditor } from "./connections/ConnectionEditor";
import {
  DetailList,
  DetailListAddButton,
  DetailListEmpty,
  DetailListGroupLabel,
  DetailListItem,
  MasterDetail,
} from "./MasterDetail";
import { byName, compareNames } from "@/lib/sortByName";

// ConnectionsPanel is the master/detail shell for connection instances: a
// kind-grouped sidebar list on the left and, on the right, the read-only
// viewer or the create/edit form. The per-kind config forms, viewer, editor
// lifecycle, and health badges live under ./connections/ (#766) so this file
// stays focused on selection, URL sync, and routing between view/edit/create.
export function ConnectionsPanel() {
  const { data: systemInfo } = useSystemInfo();
  const isReadOnly = systemInfo?.config_mode === "file";
  const { data: instances, isLoading, isFetching } = useEffectiveConnections();
  const connections = useMemo(() => instances ?? [], [instances]);
  // Bulk per-row OAuth health drives the connection-list health
  // badge. Polls every 10s in the hook so background-refresh
  // failures (refresher runs every 5min) become visible within
  // one tick. Keyed by `kind/name` for O(1) lookup in the row map.
  const { data: oauthHealth } = useConnectionsOAuthHealth();
  const oauthHealthByKey = useMemo(() => {
    const m = new Map<string, ConnectionOAuthHealthSummary>();
    for (const c of oauthHealth?.connections ?? []) {
      m.set(`${c.kind}/${c.name}`, c);
    }
    return m;
  }, [oauthHealth]);

  // Read initial selection from URL (?kind=...&name=...) so the OAuth
  // callback's returnURL can restore the connection the operator was
  // editing. Falls through to the auto-select-first-listed effect when
  // the URL is absent or stale.
  const initialSelection = (() => {
    if (typeof window === "undefined") return null;
    const params = new URLSearchParams(window.location.search);
    const k = params.get("kind");
    const n = params.get("name");
    if (k && n) return `${k}/${n}`;
    return null;
  })();
  const [selectedKey, setSelectedKey] = useState<string | null>(initialSelection);
  // The search and kind filter (#2350) start from the address, so a filtered
  // view can be linked and survives a reload.
  const [filter, setFilter] = useConnectionFilter();
  const filtered = useMemo(() => filterConnections(connections, filter), [connections, filter]);
  const [mode, setMode] = useState<"view" | "edit" | "create">("view");
  const [dirty, setDirty] = useState(false);
  // A sidebar link, a tab close or a reload asks before an unsaved edit is
  // lost (#2268); selecting another connection here asks below.
  useUnsavedChangesGuard(dirty);

  // Group by kind. Sidebar order is alphabetical on kind, and within a
  // kind by connection name (#2389).
  const grouped = useMemo(() => {
    const groups: Record<string, EffectiveConnection[]> = {};
    for (const c of filtered) {
      const k = c.kind;
      if (!groups[k]) groups[k] = [];
      groups[k]!.push(c);
    }
    for (const list of Object.values(groups)) list.sort(byName((c) => c.name));
    return groups;
  }, [filtered]);

  // firstListed is the connection that appears at the top of the
  // sidebar — first item of the first (alphabetically) kind group.
  // Auto-select uses this so the default view matches what the
  // operator sees in the left nav, instead of whatever sort order
  // the backend happens to return.
  const firstListed = useMemo(() => {
    const kinds = Object.keys(grouped).sort(compareNames);
    const first = kinds[0];
    if (!first) return null;
    return grouped[first]?.[0] ?? null;
  }, [grouped]);

  const selected = useMemo(
    () => connections.find((c) => `${c.kind}/${c.name}` === selectedKey) ?? null,
    [connections, selectedKey],
  );

  // Auto-select the first listed connection when none is selected
  // (or the URL-restored one is stale). Gate on !isFetching so the
  // post-save refetch lands before we judge selectedKey stale —
  // otherwise a freshly-created connection's setSelectedKey races
  // the cache invalidation: the effect sees the pre-mutation list,
  // can't find the new key, and snaps selection to firstListed.
  useEffect(() => {
    if (isFetching) return;
    if (selectedKey) {
      // If the URL pointed at a connection that no longer exists,
      // fall through to first-listed.
      const exists = connections.some((c) => `${c.kind}/${c.name}` === selectedKey);
      if (exists) return;
    }
    if (firstListed) {
      setSelectedKey(`${firstListed.kind}/${firstListed.name}`);
    }
  }, [connections, selectedKey, firstListed, isFetching]);

  // Mirror the selection back into the URL (without reloading) so a
  // round-trip through an OAuth callback's returnURL restores the
  // same connection. Use replaceState to avoid polluting browser
  // history with a new entry per selection click.
  useEffect(() => {
    if (typeof window === "undefined" || !selectedKey) return;
    const [k, n] = selectedKey.split("/");
    const params = new URLSearchParams(window.location.search);
    params.set("kind", k ?? "");
    params.set("name", n ?? "");
    const url = `${window.location.pathname}?${params.toString()}`;
    window.history.replaceState(null, "", url);
  }, [selectedKey]);

  const handleSelect = useCallback(
    (c: EffectiveConnection) => {
      if (dirty && !window.confirm(DISCARD_PROMPT)) return;
      setSelectedKey(`${c.kind}/${c.name}`);
      setMode("view");
      setDirty(false);
    },
    [dirty],
  );

  const handleCreate = useCallback(() => {
    if (dirty && !window.confirm(DISCARD_PROMPT)) return;
    setSelectedKey(null);
    setMode("create");
    setDirty(false);
  }, [dirty]);

  const handleEdit = useCallback(() => {
    setMode("edit");
    setDirty(false);
  }, []);

  const handleCancel = useCallback(() => {
    if (mode === "create") {
      if (connections.length > 0 && connections[0]) {
        setSelectedKey(`${connections[0].kind}/${connections[0].name}`);
      }
      setMode("view");
    } else {
      setMode("view");
    }
    setDirty(false);
  }, [mode, connections]);

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        Loading connections...
      </div>
    );
  }

  return (
    <MasterDetail
      list={
        <DetailList
          header={
            connections.length > 0 && (
              <ConnectionFilterBar connections={connections} filter={filter} onChange={setFilter} />
            )
          }
          footer={
            !isReadOnly && (
              <DetailListAddButton
                active={mode === "create"}
                label="Add Connection"
                onClick={handleCreate}
              />
            )
          }
        >
          {Object.entries(grouped)
            .sort(([a], [b]) => compareNames(a, b))
            .map(([kind, items]) => (
              <div key={kind}>
                <DetailListGroupLabel>{kind}</DetailListGroupLabel>
                {items.map((c) => {
                  const key = `${c.kind}/${c.name}`;
                  return (
                    <ConnectionRow
                      key={key}
                      connection={c}
                      health={oauthHealthByKey.get(key)}
                      selected={selectedKey === key && mode !== "create"}
                      onSelect={handleSelect}
                    />
                  );
                })}
              </div>
            ))}
          {connections.length === 0 && (
            <DetailListEmpty>No connection instances configured</DetailListEmpty>
          )}
          {connections.length > 0 && filtered.length === 0 && (
            <DetailListEmpty>No connections match</DetailListEmpty>
          )}
        </DetailList>
      }
    >
      {mode === "create" ? (
        <ConnectionEditor
          connection={null}
          onSave={(savedKind, savedName) => {
            setSelectedKey(`${savedKind}/${savedName}`);
            setMode("view");
            setDirty(false);
          }}
          onCancel={handleCancel}
          onDirtyChange={setDirty}
        />
      ) : selected ? (
        mode === "edit" ? (
          <ConnectionEditor
            connection={selected}
            onSave={(savedKind, savedName) => {
              setSelectedKey(`${savedKind}/${savedName}`);
              setMode("view");
              setDirty(false);
            }}
            onCancel={handleCancel}
            onDirtyChange={setDirty}
          />
        ) : (
          <ConnectionViewer
            connection={selected}
            isReadOnly={isReadOnly}
            onEdit={handleEdit}
            onDeleted={() => {
              setSelectedKey(null);
              setMode("view");
            }}
          />
        )
      ) : !selectedKey ? (
        <div className="flex h-full items-center justify-center p-6">
          <EmptyState icon={Cable} className="w-full max-w-sm">
            Select a connection or add a new one
          </EmptyState>
        </div>
      ) : (
        <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
          Loading...
        </div>
      )}
    </MasterDetail>
  );
}

// ConnectionFilterBar is the rail's search and kind filter (#2350).
function ConnectionFilterBar({
  connections,
  filter,
  onChange,
}: {
  connections: EffectiveConnection[];
  filter: ConnectionFilter;
  onChange: (update: (f: ConnectionFilter) => ConnectionFilter) => void;
}) {
  return (
    <>
      <SearchInput
        aria-label="Search connections"
        placeholder="Search connections"
        value={filter.q}
        onChange={(e) => {
          const q = e.target.value;
          onChange((f) => ({ ...f, q }));
        }}
        inputClassName="h-8 text-xs"
      />
      <FilterSelect
        label="Filter by kind"
        value={filter.type}
        onChange={(type) => onChange((f) => ({ ...f, type }))}
        options={kindOptions(connections, AVAILABLE_KINDS, filter.type)}
        className="w-full"
      />
    </>
  );
}

// ConnectionRow is one connection in the rail: its name, the health badges a
// problem raises, its description and how many tools it registers.
function ConnectionRow({
  connection: c,
  health,
  selected,
  onSelect,
}: {
  connection: EffectiveConnection;
  health: ConnectionOAuthHealthSummary | undefined;
  selected: boolean;
  onSelect: (c: EffectiveConnection) => void;
}) {
  return (
    <DetailListItem selected={selected} onClick={() => onSelect(c)}>
      <span className="block truncate font-mono text-sm font-medium">{c.name}</span>
      <RowBadges connection={c} health={health} />
      {c.description && (
        <span className="mt-1 block truncate text-xs text-muted-foreground">
          {markdownToPlainText(c.description)}
        </span>
      )}
      {c.tools && c.tools.length > 0 && (
        <span className="mt-0.5 block text-xs text-muted-foreground">{c.tools.length} tools</span>
      )}
    </DetailListItem>
  );
}

// RowBadges are the marks a connection's row carries: an OAuth problem, its
// upstream's reachability, and an api connection with no catalog (#2271).
function RowBadges({
  connection: c,
  health,
}: {
  connection: EffectiveConnection;
  health: ConnectionOAuthHealthSummary | undefined;
}) {
  const showHealth = Boolean(health?.has_oauth && (health.needs_reauth || health.idp_error_code));
  const noCatalog = lacksCatalog(c);
  if (!showHealth && !c.health && !noCatalog) return null;
  return (
    <span className="mt-1 flex flex-wrap items-center gap-1">
      {showHealth && <ConnectionOAuthHealthBadge health={health} />}
      <GatewayHealthBadge health={c.health} />
      {noCatalog && <NoCatalogBadge />}
    </span>
  );
}

// useConnectionFilter is the rail's search and kind filter (#2350), read from
// the address when the page opens and written back to it as it changes,
// beside the selection's own parameters.
function useConnectionFilter(): [ConnectionFilter, React.Dispatch<React.SetStateAction<ConnectionFilter>>] {
  const [filter, setFilter] = useState<ConnectionFilter>(() =>
    typeof window === "undefined" ? { q: "", type: "" } : readFilter(window.location.search),
  );
  useEffect(() => {
    if (typeof window === "undefined") return;
    const params = writeFilter(new URLSearchParams(window.location.search), filter);
    const query = params.toString();
    window.history.replaceState(null, "", `${window.location.pathname}${query ? `?${query}` : ""}`);
  }, [filter]);
  return [filter, setFilter];
}
