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
import {
  ConnectionOAuthHealthBadge,
  GatewayHealthBadge,
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
  const [mode, setMode] = useState<"view" | "edit" | "create">("view");
  const [dirty, setDirty] = useState(false);

  // Group by kind. Sidebar order is alphabetical on kind; within a
  // kind, connections are listed in the order the backend returned.
  const grouped = useMemo(() => {
    const groups: Record<string, EffectiveConnection[]> = {};
    for (const c of connections) {
      const k = c.kind;
      if (!groups[k]) groups[k] = [];
      groups[k]!.push(c);
    }
    return groups;
  }, [connections]);

  // firstListed is the connection that appears at the top of the
  // sidebar — first item of the first (alphabetically) kind group.
  // Auto-select uses this so the default view matches what the
  // operator sees in the left nav, instead of whatever sort order
  // the backend happens to return.
  const firstListed = useMemo(() => {
    const kinds = Object.keys(grouped).sort((a, b) => a.localeCompare(b));
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
      if (dirty && !window.confirm("Discard unsaved changes?")) return;
      setSelectedKey(`${c.kind}/${c.name}`);
      setMode("view");
      setDirty(false);
    },
    [dirty],
  );

  const handleCreate = useCallback(() => {
    if (dirty && !window.confirm("Discard unsaved changes?")) return;
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
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([kind, items]) => (
              <div key={kind}>
                <DetailListGroupLabel>{kind}</DetailListGroupLabel>
                {items.map((c) => {
                  const key = `${c.kind}/${c.name}`;
                  const health = oauthHealthByKey.get(key);
                  const showHealth = Boolean(
                    health?.has_oauth &&
                      (health.needs_reauth || health.idp_error_code),
                  );
                  return (
                    <DetailListItem
                      key={key}
                      selected={selectedKey === key && mode !== "create"}
                      onClick={() => handleSelect(c)}
                    >
                      <span className="block truncate font-mono text-sm font-medium">
                        {c.name}
                      </span>
                      {(showHealth || c.health) && (
                        <span className="mt-1 flex flex-wrap items-center gap-1">
                          {showHealth && (
                            <ConnectionOAuthHealthBadge health={health} />
                          )}
                          <GatewayHealthBadge health={c.health} />
                        </span>
                      )}
                      {c.description && (
                        <span className="mt-1 block truncate text-xs text-muted-foreground">
                          {markdownToPlainText(c.description)}
                        </span>
                      )}
                      {c.tools && c.tools.length > 0 && (
                        <span className="mt-0.5 block text-xs text-muted-foreground">
                          {c.tools.length} tools
                        </span>
                      )}
                    </DetailListItem>
                  );
                })}
              </div>
            ))}
          {connections.length === 0 && (
            <DetailListEmpty>No connection instances configured</DetailListEmpty>
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
