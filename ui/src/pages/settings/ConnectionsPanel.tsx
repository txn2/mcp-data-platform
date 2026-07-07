import { useState, useEffect, useCallback, useMemo } from "react";
import {
  useEffectiveConnections,
  useSystemInfo,
  useConnectionsOAuthHealth,
} from "@/api/admin/hooks";
import type { ConnectionOAuthHealthSummary } from "@/api/admin/types";
import type { EffectiveConnection } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import { Plus, Cable } from "lucide-react";
import {
  ConnectionOAuthHealthBadge,
  GatewayHealthBadge,
} from "./connections/HealthBadges";
import { ConnectionViewer } from "./connections/ConnectionViewer";
import { ConnectionEditor } from "./connections/ConnectionEditor";

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
    <div className="flex h-full overflow-hidden">
      {/* Left: Connection list */}
      <div className="w-56 shrink-0 border-r bg-muted/10 flex flex-col overflow-hidden">
        <div className="flex-1 overflow-auto">
          {Object.entries(grouped)
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([kind, items]) => (
              <div key={kind}>
                <div className="bg-muted/30 px-4 py-1.5 border-b">
                  <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                    {kind}
                  </span>
                </div>
                {items.map((c) => {
                  const key = `${c.kind}/${c.name}`;
                  const health = oauthHealthByKey.get(key);
                  const showHealth = Boolean(
                    health?.has_oauth &&
                      (health.needs_reauth || health.idp_error_code),
                  );
                  return (
                    <button
                      key={key}
                      type="button"
                      onClick={() => handleSelect(c)}
                      className={cn(
                        "flex w-full flex-col px-4 py-3 text-left border-b transition-colors",
                        selectedKey === key && mode !== "create"
                          ? "bg-primary/5 border-l-2 border-l-primary"
                          : "border-l-2 border-l-transparent hover:bg-muted/50",
                      )}
                    >
                      <span className="block truncate font-mono text-sm font-medium">
                        {c.name}
                      </span>
                      {(showHealth || c.health) && (
                        <div className="mt-1 flex flex-wrap items-center gap-1">
                          {showHealth && (
                            <ConnectionOAuthHealthBadge health={health} />
                          )}
                          <GatewayHealthBadge health={c.health} />
                        </div>
                      )}
                      {c.description && (
                        <span className="mt-1 block truncate text-xs text-muted-foreground">
                          {c.description}
                        </span>
                      )}
                      {c.tools && c.tools.length > 0 && (
                        <span className="mt-0.5 block text-xs text-muted-foreground">
                          {c.tools.length} tools
                        </span>
                      )}
                    </button>
                  );
                })}
              </div>
            ))}
          {connections.length === 0 && (
            <div className="px-4 py-8 text-center text-xs text-muted-foreground">
              No connection instances configured
            </div>
          )}
        </div>
        {!isReadOnly && (
          <div className="border-t p-2">
            <button
              type="button"
              onClick={handleCreate}
              className={cn(
                "flex w-full items-center justify-center gap-1.5 rounded-md px-3 py-2 text-xs font-medium transition-colors",
                mode === "create"
                  ? "bg-primary/10 text-primary"
                  : "text-muted-foreground hover:bg-muted hover:text-foreground",
              )}
            >
              <Plus className="h-3.5 w-3.5" />
              Add Connection
            </button>
          </div>
        )}
      </div>

      {/* Right: Detail / Edit panel */}
      <div className="flex-1 overflow-auto">
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
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
            <div className="text-center">
              <Cable className="mx-auto mb-2 h-8 w-8 opacity-30" />
              <p>Select a connection or add a new one</p>
            </div>
          </div>
        ) : (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
            Loading...
          </div>
        )}
      </div>
    </div>
  );
}
