import { useState, useEffect, useCallback, useMemo, useRef } from "react";
import {
  useAPICatalogs,
  useEffectiveConnections,
  useSetConnectionInstance,
  useDeleteConnectionInstance,
  useStartAPIGatewayOAuth,
  useSystemInfo,
  useConnectionsOAuthHealth,
} from "@/api/admin/hooks";
import type { ConnectionOAuthHealthSummary } from "@/api/admin/types";
import type { EffectiveConnection } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import {
  Plus,
  Trash2,
  Save,
  Cable,
  AlertCircle,
  Check,
  Database,
  X,
} from "lucide-react";
import { GatewayActionBar, GatewayRulesDrawer } from "./GatewayActions";
import { ConnectionOAuthStatusCard } from "./ConnectionOAuthStatusCard";
import { HelpDialog } from "@/components/HelpDialog";
import { ApiGatewayAuthHelp, ApiGatewayTLSHelp } from "./ApiGatewayHelpContent";

// ConnectionOAuthHealthBadge renders the per-row health indicator
// on the connection list. Visible only when the bulk health hook
// has data AND the connection has OAuth configured. Three states:
//
//   - needs_reauth=true       → red dot, "needs reauth" tooltip
//   - last refresh failed but not yet terminal → amber dot, code in tooltip
//   - token_acquired && no recent failure → no badge (default)
//
// The operator sees the red dot from the connection list without
// clicking in, addressing the "API calls are silently failing"
// blind spot in the UI.
function ConnectionOAuthHealthBadge({
  health,
}: {
  health: ConnectionOAuthHealthSummary | undefined;
}) {
  if (!health || !health.has_oauth) return null;
  if (health.needs_reauth) {
    const code = health.idp_error_code;
    const tooltip = code
      ? `Reauth required (${code}). Click in to view details.`
      : "Reauth required. Click in to view details.";
    return (
      <span
        className="shrink-0 inline-flex items-center gap-1 rounded px-1 py-0 text-xs font-medium bg-destructive/10 text-destructive"
        title={tooltip}
        aria-label={tooltip}
      >
        <span className="h-1.5 w-1.5 rounded-full bg-destructive" />
        reauth
      </span>
    );
  }
  if (health.idp_error_code) {
    // Token still considered valid but the most recent refresh
    // failed transiently. Surface so the operator notices before
    // the access token actually expires.
    const tooltip = `Last refresh failed (${health.idp_error_code}). Retrying.`;
    return (
      <span
        className="shrink-0 inline-flex items-center gap-1 rounded px-1 py-0 text-xs font-medium bg-amber-500/10 text-amber-600 dark:text-amber-400"
        title={tooltip}
        aria-label={tooltip}
      >
        <span className="h-1.5 w-1.5 rounded-full bg-amber-500" />
        refresh failing
      </span>
    );
  }
  return null;
}

// APICatalogPicker renders the dropdown that points an api-kind
// connection at one of the globally-owned API catalogs. The model
// resolves connection → catalog → specs at runtime, so changing
// the dropdown immediately changes the set of operations that
// api_list_endpoints exposes for this connection on the next
// reload.
function APICatalogPicker({
  config,
  onChange,
}: {
  config: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const { data: catalogs, isLoading } = useAPICatalogs();
  const value = String(config.catalog_id ?? "");
  return (
    <div>
      <label className="mb-1 block text-xs font-medium">OpenAPI Catalog</label>
      <select
        value={value}
        onChange={(e) =>
          onChange({
            ...config,
            catalog_id: e.target.value === "" ? undefined : e.target.value,
          })
        }
        className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
      >
        <option value="">— No spec (model can still invoke explicit method+path) —</option>
        {(catalogs ?? []).map((c) => (
          <option key={c.id} value={c.id}>
            {c.display_name}
            {c.version ? ` (v${c.version})` : ""} — {c.spec_count} spec
            {c.spec_count === 1 ? "" : "s"}
          </option>
        ))}
      </select>
      <p className="mt-1 text-xs text-muted-foreground">
        Catalogs are managed under{" "}
        <a className="underline" href={`${(import.meta.env.BASE_URL || "/").replace(/\/$/, "")}/admin/api-catalogs`}>
          API Catalogs
        </a>
        . One catalog can back many connections. {isLoading && "Loading…"}
      </p>
    </div>
  );
}

// LegacyOpenAPISpecBanner surfaces a one-time migration hint when an
// older connection still has the deprecated `openapi_spec` JSONB key
// set. The toolkit no longer reads it; the operator should move the
// content into a catalog (or accept that the connection has no
// model-visible spec until they do).
function LegacyOpenAPISpecBanner({
  config,
  onChange,
}: {
  config: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const legacy = typeof config.openapi_spec === "string" ? (config.openapi_spec as string).trim() : "";
  if (!legacy) return null;
  if (config.catalog_id) {
    // Operator already wired a catalog — offer to clear the stale field.
    return (
      <div className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200">
        This connection still carries a deprecated inline <code>openapi_spec</code> field.
        It is no longer read by the toolkit; the catalog above is what the model sees.
        <button
          type="button"
          onClick={() => {
            const next = { ...config };
            delete next.openapi_spec;
            onChange(next);
          }}
          className="ml-2 underline"
        >
          Clear it
        </button>
      </div>
    );
  }
  return (
    <div className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200">
      This connection uses an inline OpenAPI spec which is no longer supported.
      Create a catalog under{" "}
      <a className="underline" href={`${(import.meta.env.BASE_URL || "/").replace(/\/$/, "")}/admin/api-catalogs`}>
        API Catalogs
      </a>{" "}
      and select it above. Until you do, <code>api_list_endpoints</code> returns no operations.
    </div>
  );
}

// ---------------------------------------------------------------------------
// Kind badge colors
// ---------------------------------------------------------------------------

const KIND_COLORS: Record<string, string> = {
  trino: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
  datahub: "bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-400",
  s3: "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400",
  mcp: "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-400",
  api: "bg-indigo-100 text-indigo-800 dark:bg-indigo-900/30 dark:text-indigo-400",
};

function kindColor(kind: string): string {
  return KIND_COLORS[kind] ?? "bg-muted text-muted-foreground";
}

// ---------------------------------------------------------------------------
// Main panel
// ---------------------------------------------------------------------------

export function ConnectionsPanel() {
  const { data: systemInfo } = useSystemInfo();
  const isReadOnly = systemInfo?.config_mode === "file";
  const { data: instances, isLoading } = useEffectiveConnections();
  const connections = instances ?? [];
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
  // (or the URL-restored one is stale).
  useEffect(() => {
    if (selectedKey) {
      // If the URL pointed at a connection that no longer exists,
      // fall through to first-listed.
      const exists = connections.some((c) => `${c.kind}/${c.name}` === selectedKey);
      if (exists) return;
    }
    if (firstListed) {
      setSelectedKey(`${firstListed.kind}/${firstListed.name}`);
    }
  }, [connections, selectedKey, firstListed]);

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
                      {showHealth && (
                        <div className="mt-1">
                          <ConnectionOAuthHealthBadge health={health} />
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
            onSave={() => {
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
              onSave={() => {
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

// ---------------------------------------------------------------------------
// Connection Viewer (read-only)
// ---------------------------------------------------------------------------

function ConnectionViewer({
  connection,
  isReadOnly,
  onEdit,
  onDeleted,
}: {
  connection: EffectiveConnection;
  isReadOnly: boolean;
  onEdit: () => void;
  onDeleted: () => void;
}) {
  const deleteMutation = useDeleteConnectionInstance();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [rulesOpen, setRulesOpen] = useState(false);

  const datahubSourceName = typeof connection.config?.datahub_source_name === "string"
    ? connection.config.datahub_source_name : undefined;
  const rawMapping = connection.config?.catalog_mapping;
  const catalogMapping = (rawMapping != null && typeof rawMapping === "object" && !Array.isArray(rawMapping))
    ? rawMapping as Record<string, string> : undefined;
  const hasDataHub = Boolean(datahubSourceName) || (catalogMapping != null && Object.keys(catalogMapping).length > 0);
  const datahubFilterKeys = new Set(["datahub_source_name", "catalog_mapping"]);
  const configEntries = Object.entries(connection.config ?? {}).filter(([key]) => !datahubFilterKeys.has(key));

  return (
    <div className="p-6 space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h2 className="text-lg font-semibold">{connection.name}</h2>
            <span className={cn("rounded-full px-2.5 py-0.5 text-xs font-medium", kindColor(connection.kind))}>
              {connection.kind}
            </span>
          </div>
          {connection.description && (
            <p className="mt-1 text-sm text-muted-foreground">{connection.description}</p>
          )}
          {connection.source === "both" && (
            <p className="mt-1 text-xs text-muted-foreground">
              This connection is managed in the database. A fallback version also exists in the config file and can be removed once database management is confirmed.
            </p>
          )}
        </div>
        {!isReadOnly && (
          <div className="flex gap-2">
            <button
              type="button"
              onClick={onEdit}
              className="rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90"
            >
              Edit
            </button>
            <button
              type="button"
              onClick={() => setConfirmDelete(true)}
              className="rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-destructive/10 hover:text-destructive hover:border-destructive/30"
            >
              <Trash2 className="h-3 w-3" />
            </button>
          </div>
        )}
      </div>

      {/* Gateway-specific actions: test, refresh, rules */}
      {connection.kind === "mcp" && !isReadOnly && (
        <GatewayActionBar
          connectionName={connection.name}
          connectionConfig={connection.config ?? {}}
          onOpenRules={() => setRulesOpen(true)}
        />
      )}
      {rulesOpen && connection.kind === "mcp" && (
        <GatewayRulesDrawer
          connectionName={connection.name}
          onClose={() => setRulesOpen(false)}
        />
      )}

      {/* OAuth status — shown for every connection kind that supports
          authorization_code. The card hides itself when the
          connection's auth_mode is not OAuth, so it's safe to render
          unconditionally. Consistent surface across mcp / api / future
          kinds. */}
      {!isReadOnly && (
        <ConnectionOAuthStatusCard
          kind={connection.kind}
          name={connection.name}
          authMode={String(connection.config?.auth_mode ?? "")}
        />
      )}

      {/* Metadata */}
      <div className="grid grid-cols-3 gap-4">
        <InfoCard label="Kind" value={connection.kind} />
        <InfoCard label="Created By" value={connection.created_by || "unknown"} />
        <InfoCard
          label="Last Updated"
          value={connection.updated_at ? new Date(connection.updated_at).toLocaleString() : "N/A"}
        />
      </div>

      {/* Config */}
      {configEntries.length > 0 && (
        <div>
          <div className="mb-3 flex items-center gap-2">
            <Database className="h-4 w-4 text-muted-foreground" />
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Configuration
            </h3>
          </div>
          <div className="rounded-md border divide-y">
            {configEntries.map(([key, value]) => {
              const displayValue = typeof value === "object" && value !== null
                ? JSON.stringify(value)
                : String(value);
              return (
                <div key={key} className="flex items-center gap-4 px-4 py-2">
                  <span className="text-xs font-mono text-muted-foreground w-48 shrink-0 truncate">
                    {key}
                  </span>
                  <span className="text-xs font-mono flex-1 truncate">
                    {displayValue}
                  </span>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* DataHub Integration */}
      {hasDataHub && (
        <div>
          <div className="mb-3 flex items-center gap-2">
            <Database className="h-4 w-4 text-muted-foreground" />
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              DataHub Integration
            </h3>
          </div>
          <div className="rounded-md border divide-y">
            {datahubSourceName && (
              <div className="flex items-center gap-4 px-4 py-2">
                <span className="text-xs font-mono text-muted-foreground w-48 shrink-0">
                  DataHub Source Name
                </span>
                <span className="text-xs font-mono flex-1">
                  {datahubSourceName}
                </span>
              </div>
            )}
            {catalogMapping && Object.keys(catalogMapping).length > 0 && (
              <div className="px-4 py-2">
                <span className="text-xs font-mono text-muted-foreground block mb-1">
                  Catalog Mapping
                </span>
                <div className="ml-4 space-y-0.5">
                  {Object.entries(catalogMapping).map(([local, datahub]) => (
                    <div key={local} className="flex items-center gap-2 text-xs font-mono">
                      <span>{local}</span>
                      <span className="text-muted-foreground">&rarr;</span>
                      <span>{datahub}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        </div>
      )}

      {/* Delete confirmation */}
      {confirmDelete && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={() => setConfirmDelete(false)}>
          <div className="rounded-lg border bg-card p-6 shadow-lg max-w-sm mx-4" onClick={(e) => e.stopPropagation()}>
            <h3 className="text-sm font-semibold mb-2">Delete Connection</h3>
            <p className="text-sm text-muted-foreground mb-4">
              Are you sure you want to delete &quot;{connection.kind}/{connection.name}&quot;? This cannot be undone.
            </p>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setConfirmDelete(false)}
                className="rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => {
                  deleteMutation.mutate(
                    { kind: connection.kind, name: connection.name },
                    { onSuccess: onDeleted },
                  );
                  setConfirmDelete(false);
                }}
                className="rounded-md bg-destructive px-3 py-1.5 text-xs font-medium text-destructive-foreground hover:bg-destructive/90"
              >
                Delete
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Connection Editor
// ---------------------------------------------------------------------------

interface EditorProps {
  connection: EffectiveConnection | null; // null = create mode
  onSave: () => void;
  onCancel: () => void;
  onDirtyChange: (dirty: boolean) => void;
}

const AVAILABLE_KINDS = ["trino", "s3", "mcp", "api"];

function ConnectionEditor({ connection, onSave, onCancel, onDirtyChange }: EditorProps) {
  const isCreate = !connection;
  const setMutation = useSetConnectionInstance();
  const [kind, setKind] = useState(connection?.kind ?? "trino");
  const [name, setName] = useState(connection?.name ?? "");
  const nameValid = !isCreate || /^[a-z][a-z0-9_-]*$/.test(name);
  const [description, setDescription] = useState(
    connection?.description || (connection?.config?.description as string) || "",
  );
  const [configObj, setConfigObj] = useState<Record<string, any>>(
    connection?.config ? { ...connection.config } : {},
  );
  // configObjRef mirrors configObj synchronously so handleSave can
  // read the latest value even when the Save click follows a child
  // editor's onChange in the same task. React schedules setConfigObj
  // asynchronously; the closure-captured configObj is one render
  // behind. The ref bridges the gap so a keystroke that landed in
  // the keystroke-eager SensitiveKeyValueEditor immediately before
  // the Save click is included in the PUT body.
  const configObjRef = useRef(configObj);
  const updateConfig = useCallback((next: Record<string, any>) => {
    configObjRef.current = next;
    setConfigObj(next);
  }, []);
  const configJson = JSON.stringify(configObj); // for dirty tracking
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  // Track dirty state
  useEffect(() => {
    if (isCreate) {
      onDirtyChange(!!name.trim());
    } else {
      const origDesc = connection?.description || (connection?.config?.description as string) || "";
      const origJson = JSON.stringify(connection?.config ?? {});
      onDirtyChange(
        description !== origDesc || configJson !== origJson,
      );
    }
  }, [kind, name, description, configJson, connection, isCreate, onDirtyChange]);

  // Reset config when kind changes in create mode
  useEffect(() => {
    if (isCreate) {
      configObjRef.current = {};
      setConfigObj({});
    }
  }, [kind, isCreate]);

  const handleSave = useCallback(() => {
    setSaveError(null);
    // Read from ref, not state, so a keystroke that just propagated
    // through a child editor's onChange is included even when the
    // Save button is the next click in the same task. The closure-
    // captured configObj is one render behind; configObjRef is
    // updated synchronously by updateConfig before React rerenders.
    const config = configObjRef.current;

    setMutation.mutate(
      {
        kind,
        name,
        config,
        description: description || undefined,
      },
      {
        onSuccess: () => {
          setSaveSuccess(true);
          setTimeout(() => setSaveSuccess(false), 2000);
          onSave();
        },
        onError: (err) => {
          setSaveError(err instanceof Error ? err.message : "Failed to save");
        },
      },
    );
  }, [kind, name, description, setMutation, onSave]);

  const isPending = setMutation.isPending;

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="flex items-center justify-between border-b px-6 py-3 bg-muted/10">
        <h2 className="text-sm font-semibold">
          {isCreate ? "New Connection" : `Edit: ${connection.kind}/${connection.name}`}
        </h2>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={handleSave}
            disabled={isPending || (isCreate && (!name.trim() || !nameValid))}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-all disabled:opacity-50",
              saveSuccess
                ? "bg-green-600 text-white"
                : "bg-primary text-primary-foreground hover:bg-primary/90",
            )}
          >
            {saveSuccess ? (
              <>
                <Check className="h-3 w-3" />
                Saved
              </>
            ) : isPending ? (
              "Saving..."
            ) : (
              <>
                <Save className="h-3 w-3" />
                {isCreate ? "Create" : "Save"}
              </>
            )}
          </button>
        </div>
      </div>

      {saveError && (
        <div className="flex items-center gap-2 border-b bg-red-50 px-6 py-2 text-xs text-red-700 dark:bg-red-950/30 dark:text-red-400">
          <AlertCircle className="h-3.5 w-3.5" />
          {saveError}
        </div>
      )}

      {/* Form */}
      <div className="flex-1 overflow-auto p-6 space-y-6">
        {/* Kind & Name */}
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="mb-1 block text-xs font-medium">Kind</label>
            <select
              value={kind}
              onChange={(e) => setKind(e.target.value)}
              disabled={!isCreate}
              className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {AVAILABLE_KINDS.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
            <p className="mt-1 text-xs text-muted-foreground">
              Connection type. Cannot be changed after creation.
            </p>
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium">
              Identifier
            </label>
            <input
              type="text"
              value={name}
              onChange={(e) => {
                if (!isCreate) return;
                const raw = e.target.value.toLowerCase();
                const cleaned = raw.replace(/[^a-z0-9_-]/g, "");
                setName(cleaned);
              }}
              disabled={!isCreate}
              placeholder="prod-trino"
              pattern="^[a-z][a-z0-9_-]*$"
              maxLength={64}
              autoComplete="off"
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              aria-describedby="connection-name-help"
              className="w-full rounded-md border bg-background px-3 py-2 text-sm font-mono outline-none ring-ring focus:ring-2 disabled:opacity-50 disabled:cursor-not-allowed"
            />
            <p
              id="connection-name-help"
              className="mt-1 text-xs text-muted-foreground"
            >
              Machine identifier used in API routes and persona patterns.
              Lowercase letters, digits, hyphens, underscores. Must start with
              a letter. Cannot be changed after creation.
            </p>
            {isCreate && name.length > 0 && !nameValid && (
              <p className="mt-1 text-xs text-destructive">
                Identifier must start with a lowercase letter.
              </p>
            )}
          </div>
        </div>

        {/* Description */}
        <div>
          <label className="mb-1 block text-xs font-medium">Description</label>
          <input
            type="text"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="What this connection is for..."
            className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
          />
        </div>

        {/* Kind-specific configuration form */}
        <div className="rounded-lg border">
          <div className="px-4 py-3 border-b bg-muted/10">
            <span className="text-sm font-medium">Configuration</span>
          </div>
          <div className="px-4 py-4 space-y-4">
            {kind === "trino" && (
              <TrinoConfigForm config={configObj} onChange={updateConfig} />
            )}
            {kind === "s3" && (
              <S3ConfigForm config={configObj} onChange={updateConfig} />
            )}
            {kind === "mcp" && (
              <GatewayConfigForm config={configObj} onChange={updateConfig} />
            )}
            {kind === "api" && (
              <ApiGatewayConfigForm
                config={configObj}
                onChange={updateConfig}
                connectionName={name}
                isCreate={isCreate}
              />
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Shared primitives
// ---------------------------------------------------------------------------

function InfoCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border bg-muted/20 px-3 py-2">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <p className="text-sm font-medium truncate">{value}</p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Kind-specific configuration forms
// ---------------------------------------------------------------------------

interface ConfigFormProps {
  config: Record<string, any>;
  onChange: (config: Record<string, any>) => void;
}

function ConfigField({
  label,
  help,
  value,
  onChange,
  type = "text",
  placeholder,
  mono,
  sensitive,
}: {
  label: string;
  help?: string;
  value: string;
  onChange: (v: string) => void;
  type?: "text" | "number";
  placeholder?: string;
  mono?: boolean;
  sensitive?: boolean;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs font-medium">{label}</label>
      <input
        type={sensitive ? "password" : type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        autoComplete={sensitive ? "off" : undefined}
        className={cn(
          "w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2",
          mono && "font-mono",
        )}
      />
      {help && <p className="mt-1 text-xs text-muted-foreground">{help}</p>}
    </div>
  );
}

function ConfigToggle({
  label,
  help,
  checked,
  onChange,
}: {
  label: string;
  help?: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-start gap-3">
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={cn(
          "mt-0.5 relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors",
          checked ? "bg-primary" : "bg-muted",
        )}
      >
        <span
          className={cn(
            "pointer-events-none block h-4 w-4 rounded-full bg-background shadow-sm transition-transform",
            checked ? "translate-x-4" : "translate-x-0",
          )}
        />
      </button>
      <div>
        <label className="text-xs font-medium">{label}</label>
        {help && <p className="text-xs text-muted-foreground">{help}</p>}
      </div>
    </div>
  );
}

function update(config: Record<string, any>, key: string, value: any): Record<string, any> {
  if (value === "" || value === undefined) {
    const next = { ...config };
    delete next[key];
    return next;
  }
  return { ...config, [key]: value };
}

function TrinoConfigForm({ config, onChange }: ConfigFormProps) {
  return (
    <>
      <div className="grid grid-cols-2 gap-4">
        <ConfigField
          label="Host"
          value={String(config.host ?? "")}
          onChange={(v) => onChange(update(config, "host", v))}
          placeholder="trino.example.com"
          mono
          help="Trino coordinator hostname or IP address"
        />
        <ConfigField
          label="Port"
          type="number"
          value={String(config.port ?? "")}
          onChange={(v) => onChange(update(config, "port", v ? parseInt(v, 10) : ""))}
          placeholder="443"
          help="Trino coordinator port (default: 443 for SSL, 8080 for plain)"
        />
      </div>
      <div className="grid grid-cols-2 gap-4">
        <ConfigField
          label="Username"
          value={String(config.user ?? "")}
          onChange={(v) => onChange(update(config, "user", v))}
          placeholder="platform_svc"
          help="Service account username for Trino authentication"
        />
        <ConfigField
          label="Password"
          value={String(config.password ?? "")}
          onChange={(v) => onChange(update(config, "password", v))}
          placeholder="••••••••"
          sensitive
          help="Leave blank to keep existing password"
        />
      </div>
      <div className="grid grid-cols-2 gap-4">
        <ConfigField
          label="Default Catalog"
          value={String(config.catalog ?? "")}
          onChange={(v) => onChange(update(config, "catalog", v))}
          placeholder="iceberg"
          mono
          help="Default Trino catalog for queries (e.g. iceberg, hive, memory)"
        />
        <ConfigField
          label="Default Schema"
          value={String(config.schema ?? "")}
          onChange={(v) => onChange(update(config, "schema", v))}
          placeholder="public"
          mono
          help="Default Trino schema within the catalog"
        />
      </div>
      <ConfigToggle
        label="SSL / TLS"
        checked={!!config.ssl}
        onChange={(v) => onChange(update(config, "ssl", v))}
        help="Connect using HTTPS. Required for production deployments."
      />
      <div className="border-t pt-4 mt-2">
        <p className="text-xs font-medium mb-3">DataHub Integration</p>
        <ConfigField
          label="DataHub Source Name"
          value={String(config.datahub_source_name ?? "")}
          onChange={(v) => onChange(update(config, "datahub_source_name", v))}
          placeholder="trino"
          mono
          help="The platform identifier in DataHub URNs for datasets accessible through this connection (e.g. trino, postgres, hive). Defaults to trino if not set."
        />
        <div className="mt-4">
          <label className="mb-1 block text-xs font-medium">Catalog Mapping</label>
          <p className="mb-2 text-xs text-muted-foreground">
            Maps this connection's catalog names to DataHub catalog names. For example, if this connection uses catalog "rdbms" but DataHub knows it as "postgres", add rdbms → postgres.
          </p>
          <KeyValueEditor
            entries={config.catalog_mapping as Record<string, string> ?? {}}
            onChange={(v) => onChange(update(config, "catalog_mapping", Object.keys(v).length > 0 ? v : ""))}
            keyPlaceholder="connection catalog"
            valuePlaceholder="datahub catalog"
          />
        </div>
      </div>
    </>
  );
}


function S3ConfigForm({ config, onChange }: ConfigFormProps) {
  return (
    <>
      <ConfigField
        label="Endpoint"
        value={String(config.endpoint ?? "")}
        onChange={(v) => onChange(update(config, "endpoint", v))}
        placeholder="https://s3.amazonaws.com"
        mono
        help="S3-compatible endpoint URL. Leave blank for AWS S3. Set for MinIO, SeaweedFS, etc."
      />
      <div className="grid grid-cols-2 gap-4">
        <ConfigField
          label="Region"
          value={String(config.region ?? "")}
          onChange={(v) => onChange(update(config, "region", v))}
          placeholder="us-east-1"
          mono
          help="AWS region for the S3 service"
        />
        <ConfigField
          label="Bucket Prefix"
          value={String(config.bucket_prefix ?? "")}
          onChange={(v) => onChange(update(config, "bucket_prefix", v))}
          placeholder="data-lake-"
          mono
          help="Only show buckets matching this prefix"
        />
      </div>
      <div className="grid grid-cols-2 gap-4">
        <ConfigField
          label="Access Key ID"
          value={String(config.access_key_id ?? "")}
          onChange={(v) => onChange(update(config, "access_key_id", v))}
          placeholder="AKIA..."
          mono
          help="AWS access key ID or S3-compatible equivalent"
        />
        <ConfigField
          label="Secret Access Key"
          value={String(config.secret_access_key ?? "")}
          onChange={(v) => onChange(update(config, "secret_access_key", v))}
          placeholder="••••••••"
          sensitive
          help="Leave blank to keep existing secret"
        />
      </div>
      <ConfigToggle
        label="Force Path Style"
        checked={!!config.use_path_style}
        onChange={(v) => onChange(update(config, "use_path_style", v))}
        help="Use path-style URLs (bucket in path, not subdomain). Required for MinIO and most S3-compatible stores."
      />
      <div className="border-t pt-4 mt-2">
        <p className="text-xs font-medium mb-3">DataHub Integration</p>
        <ConfigField
          label="DataHub Source Name"
          value={String(config.datahub_source_name ?? "")}
          onChange={(v) => onChange(update(config, "datahub_source_name", v))}
          placeholder="s3"
          mono
          help="The platform identifier in DataHub URNs for datasets accessible through this connection. Defaults to s3 if not set."
        />
      </div>
    </>
  );
}

function KeyValueEditor({
  entries,
  onChange,
  keyPlaceholder,
  valuePlaceholder,
}: {
  entries: Record<string, string>;
  onChange: (entries: Record<string, string>) => void;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
}) {
  const [newKey, setNewKey] = useState("");
  const [newValue, setNewValue] = useState("");
  const items = Object.entries(entries);

  const add = () => {
    const k = newKey.trim();
    const v = newValue.trim();
    if (k && v) {
      onChange({ ...entries, [k]: v });
      setNewKey("");
      setNewValue("");
    }
  };

  return (
    <div>
      {items.length > 0 && (
        <div className="rounded-md border overflow-hidden mb-2">
          <table className="w-full text-xs">
            <tbody>
              {items.map(([k, v]) => (
                <tr key={k} className="border-b last:border-0">
                  <td className="px-3 py-1.5 font-mono">{k}</td>
                  <td className="px-2 text-muted-foreground">→</td>
                  <td className="px-3 py-1.5 font-mono">{v}</td>
                  <td className="px-2">
                    <button
                      type="button"
                      onClick={() => {
                        const next = { ...entries };
                        delete next[k];
                        onChange(next);
                      }}
                      className="text-muted-foreground hover:text-destructive"
                    >
                      <X className="h-3 w-3" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="flex gap-2">
        <input
          type="text"
          value={newKey}
          onChange={(e) => setNewKey(e.target.value)}
          placeholder={keyPlaceholder ?? "key"}
          className="w-36 rounded-md border bg-background px-3 py-1.5 text-xs font-mono outline-none ring-ring focus:ring-2"
        />
        <input
          type="text"
          value={newValue}
          onChange={(e) => setNewValue(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); add(); } }}
          placeholder={valuePlaceholder ?? "value"}
          className="flex-1 rounded-md border bg-background px-3 py-1.5 text-xs font-mono outline-none ring-ring focus:ring-2"
        />
        <button
          type="button"
          onClick={add}
          disabled={!newKey.trim() || !newValue.trim()}
          className="rounded-md border px-2.5 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-30"
        >
          <Plus className="h-3 w-3" />
        </button>
      </div>
    </div>
  );
}

// asStringMap normalizes a possibly-undefined/array/scalar value into
// Record<string, string>. The platform's redaction layer returns
// `static_headers` with values of "[REDACTED]" (a string), so the
// editor just sees strings here either way.
function asStringMap(raw: unknown): Record<string, string> {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return {};
  }
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
    if (typeof v === "string") {
      out[k] = v;
    }
  }
  return out;
}

// SensitiveKeyValueEditor renders one inline-editable row per entry
// plus an "Add header" button that appends a fresh empty row. Every
// keystroke commits to the parent's entries map (a row contributes
// only when BOTH name and value are non-empty), so there is no
// "pending uncommitted" state to lose at save time. Stable row IDs
// keep React's reconciliation correct as the user renames keys or
// removes rows out of order. Without IDs, deleting row 1 of 3 would
// look like row 1 had its values changed and row 3 disappeared.
//
// Existing rows come back from the server with value === "[REDACTED]"
// (the redaction mask). The password input renders that as dots; the
// operator selects-all and types to replace the value, and the
// backend's redaction-merge layer reinstates the stored value if the
// row is saved without a change.
function SensitiveKeyValueEditor({
  entries,
  onChange,
  keyPlaceholder,
  valuePlaceholder,
}: {
  entries: Record<string, string>;
  onChange: (entries: Record<string, string>) => void;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
}) {
  type Row = { id: number; name: string; value: string };
  const idSeq = useRef(0);
  const nextID = useCallback(() => ++idSeq.current, []);

  // Local rows are the source of truth for editing. Initial value is
  // derived from the entries prop on mount; later sync happens only
  // when the prop's KEY SET changes (a real refresh from the server),
  // not on every value change (which would clobber the user's
  // mid-edit local state after every save round-trip turning real
  // values into "[REDACTED]" masks).
  const [rows, setRows] = useState<Row[]>(() =>
    Object.entries(entries).map(([k, v]) => ({ id: nextID(), name: k, value: v })),
  );
  const lastKeySet = useRef(
    Object.keys(entries).slice().sort().join(""),
  );
  useEffect(() => {
    const k = Object.keys(entries).slice().sort().join("");
    if (k !== lastKeySet.current) {
      lastKeySet.current = k;
      setRows(
        Object.entries(entries).map(([key, val]) => ({
          id: nextID(),
          name: key,
          value: val,
        })),
      );
    }
  }, [entries, nextID]);

  const commit = useCallback(
    (updated: Row[]) => {
      setRows(updated);
      const out: Record<string, string> = {};
      for (const r of updated) {
        const n = r.name.trim();
        if (n && r.value.length > 0) {
          out[n] = r.value;
        }
      }
      // Update lastKeySet to match what we're about to emit so the
      // useEffect above doesn't fire a redundant re-sync after our
      // own commit propagates back through the entries prop.
      lastKeySet.current = Object.keys(out).slice().sort().join("");
      onChange(out);
    },
    [onChange],
  );

  const updateRow = useCallback(
    (id: number, patch: Partial<Row>) => {
      commit(rows.map((r) => (r.id === id ? { ...r, ...patch } : r)));
    },
    [rows, commit],
  );

  const deleteRow = useCallback(
    (id: number) => {
      commit(rows.filter((r) => r.id !== id));
    },
    [rows, commit],
  );

  const addRow = useCallback(() => {
    setRows((prev) => [...prev, { id: nextID(), name: "", value: "" }]);
  }, [nextID]);

  return (
    <div className="space-y-2">
      {rows.map((row) => (
        <div key={row.id} className="flex gap-2">
          <input
            type="text"
            value={row.name}
            onChange={(e) => updateRow(row.id, { name: e.target.value })}
            placeholder={keyPlaceholder ?? "header"}
            className="w-56 rounded-md border bg-background px-3 py-1.5 text-xs font-mono outline-none ring-ring focus:ring-2"
          />
          <input
            type="password"
            value={row.value}
            onChange={(e) => updateRow(row.id, { value: e.target.value })}
            placeholder={valuePlaceholder ?? "value"}
            className="flex-1 rounded-md border bg-background px-3 py-1.5 text-xs font-mono outline-none ring-ring focus:ring-2"
          />
          <button
            type="button"
            onClick={() => deleteRow(row.id)}
            className="rounded-md border px-2.5 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-destructive"
            aria-label={`Remove ${row.name || "header"}`}
          >
            <X className="h-3 w-3" />
          </button>
        </div>
      ))}
      <button
        type="button"
        onClick={addRow}
        className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
      >
        <Plus className="h-3 w-3" />
        Add header
      </button>
    </div>
  );
}

function GatewayConfigForm({ config, onChange }: ConfigFormProps) {
  return (
    <>
      <ConfigField
        label="Endpoint"
        help="HTTPS URL of the upstream MCP server (Streamable HTTP transport)."
        value={String(config.endpoint ?? "")}
        onChange={(v) => onChange(update(config, "endpoint", v))}
        placeholder="https://vendor.example.com/mcp"
        mono
      />
      <div>
        <label className="mb-1 block text-xs font-medium">Auth mode</label>
        <select
          value={String(config.auth_mode ?? "none")}
          onChange={(e) => onChange(update(config, "auth_mode", e.target.value))}
          className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="none">None</option>
          <option value="bearer">Bearer token</option>
          <option value="api_key">API key</option>
          <option value="oauth">OAuth 2.1</option>
        </select>
        <p className="mt-1 text-xs text-muted-foreground">
          Bearer sends Authorization header; API key sends X-API-Key; OAuth obtains a managed bearer token via client_credentials or authorization_code+PKCE.
        </p>
      </div>
      {(config.auth_mode === "bearer" || config.auth_mode === "api_key") && (
        <ConfigField
          label="Credential"
          help="Encrypted at rest. Use [REDACTED] when re-saving without changing it."
          value={String(config.credential ?? "")}
          onChange={(v) => onChange(update(config, "credential", v))}
          sensitive
        />
      )}
      {config.auth_mode === "oauth" && (
        <div className="rounded-md border bg-muted/20 px-3 py-3 space-y-3">
          <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            OAuth 2.1
          </div>
          <div>
            <label className="block text-xs font-medium text-foreground/80">Grant type</label>
            <select
              value={String(config.oauth_grant ?? "client_credentials")}
              onChange={(e) => onChange(update(config, "oauth_grant", e.target.value))}
              className="mt-1 w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
            >
              <option value="client_credentials">client_credentials (machine-to-machine)</option>
              <option value="authorization_code">authorization_code + PKCE (browser sign-in)</option>
            </select>
            <p className="mt-1 text-xs text-muted-foreground">
              Use authorization_code for upstreams that require a human sign-in (Salesforce Hosted MCP, etc.). After saving the connection, click Connect to authorize once — the platform refreshes the token automatically thereafter.
            </p>
          </div>
          {config.oauth_grant === "authorization_code" && (
            <ConfigField
              label="Authorization URL"
              help="Where the browser is sent to sign in. e.g. https://login.salesforce.com/services/oauth2/authorize"
              value={String(config.oauth_authorization_url ?? "")}
              onChange={(v) => onChange(update(config, "oauth_authorization_url", v))}
              placeholder="https://login.salesforce.com/services/oauth2/authorize"
              mono
            />
          )}
          <ConfigField
            label="Token URL"
            help="OAuth token endpoint. The platform POSTs the grant here."
            value={String(config.oauth_token_url ?? "")}
            onChange={(v) => onChange(update(config, "oauth_token_url", v))}
            placeholder="https://vendor.example.com/oauth/token"
            mono
          />
          <div className="grid grid-cols-2 gap-3">
            <ConfigField
              label="Client ID"
              value={String(config.oauth_client_id ?? "")}
              onChange={(v) => onChange(update(config, "oauth_client_id", v))}
              placeholder="platform-client"
              mono
            />
            <ConfigField
              label="Client Secret"
              help="Encrypted at rest. Use [REDACTED] to keep the existing value when re-saving."
              value={String(config.oauth_client_secret ?? "")}
              onChange={(v) => onChange(update(config, "oauth_client_secret", v))}
              sensitive
            />
          </div>
          <ConfigField
            label="Scope"
            help={config.oauth_grant === "authorization_code"
              ? "Space-delimited scopes. Include 'refresh_token' so cron jobs work without re-authenticating."
              : "Optional space-delimited scope string."}
            value={String(config.oauth_scope ?? "")}
            onChange={(v) => onChange(update(config, "oauth_scope", v))}
            placeholder={config.oauth_grant === "authorization_code" ? "api refresh_token" : "read"}
            mono
          />
          {config.oauth_grant === "authorization_code" && (
            <div>
              <label className="mb-1 block text-xs font-medium">OIDC prompt</label>
              <select
                value={String(config.oauth_prompt ?? "")}
                onChange={(e) => onChange(update(config, "oauth_prompt", e.target.value))}
                className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
              >
                <option value="">(default — no prompt parameter)</option>
                <option value="login">login (force fresh credentials each Reconnect)</option>
                <option value="consent">consent (force consent screen)</option>
                <option value="select_account">select_account (force account picker)</option>
                <option value="none">none (silent auth, fails if interaction needed)</option>
              </select>
              <p className="mt-1 text-xs text-muted-foreground">
                OIDC <code>prompt</code> parameter (§3.1.2.1). Leave default for non-OIDC OAuth providers
                that reject unknown parameters. Use <code>login</code> for Keycloak / Auth0 / Okta
                connections an admin holds — defeats stale-form bugs by forcing a fresh credential
                prompt on every Reconnect.
              </p>
            </div>
          )}
        </div>
      )}
      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Connect timeout"
          help="Initial dial + tool discovery (e.g. 10s, 1m)."
          value={String(config.connect_timeout ?? "")}
          onChange={(v) => onChange(update(config, "connect_timeout", v))}
          placeholder="10s"
          mono
        />
        <ConfigField
          label="Call timeout"
          help="Per-tool-call upstream timeout (e.g. 60s)."
          value={String(config.call_timeout ?? "")}
          onChange={(v) => onChange(update(config, "call_timeout", v))}
          placeholder="60s"
          mono
        />
      </div>
      <div>
        <label className="mb-1 block text-xs font-medium">Trust level</label>
        <select
          value={String(config.trust_level ?? "untrusted")}
          onChange={(e) => onChange(update(config, "trust_level", e.target.value))}
          className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="untrusted">Untrusted (default)</option>
          <option value="trusted">Trusted</option>
        </select>
        <p className="mt-1 text-xs text-muted-foreground">
          Reserved for future content-fencing of upstream responses. Leave at "untrusted" unless you control the upstream.
        </p>
      </div>
    </>
  );
}

// TLSMaterialEditor renders the per-connection mTLS material section:
// client cert + private key (both required together) and an optional
// CA bundle for upstreams behind a private root. The fields are
// optional for every auth mode and required when auth_mode is "mtls"
// (the cert itself is the credential). PEM blocks are pasted into
// textareas rather than file-picked because operators commonly
// receive the material as text from their PKI tooling and the
// uniform paste path keeps the read-after-save flow trivial: the
// server returns the cert verbatim and the private key as
// [REDACTED], and we surface the leaf certificate's expiry from a
// server-computed field so the badge does not duplicate the parse
// logic in JavaScript.
function TLSMaterialEditor({
  config,
  onChange,
  onOpenHelp,
}: ConfigFormProps & { onOpenHelp: () => void }) {
  const expiry = String(config.mtls_cert_not_after ?? "");
  const isMTLSMode = config.auth_mode === "mtls";
  return (
    <div className="rounded-md border bg-muted/20 px-3 py-3 space-y-3">
      <div className="flex items-center justify-between">
        <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          TLS / mTLS
          {isMTLSMode && (
            <span className="ml-2 rounded bg-blue-100 px-1.5 py-0.5 text-[10px] font-medium text-blue-700 dark:bg-blue-900/40 dark:text-blue-200">
              required for auth_mode: mtls
            </span>
          )}
        </div>
        <button
          type="button"
          onClick={onOpenHelp}
          className="text-xs text-blue-600 hover:underline dark:text-blue-400"
        >
          Learn about TLS / mTLS
        </button>
      </div>
      <PEMTextarea
        label="Client certificate (PEM)"
        value={String(config.mtls_client_cert_pem ?? "")}
        onChange={(v) => onChange(update(config, "mtls_client_cert_pem", v))}
        placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----"
      />
      <PEMTextarea
        label="Client private key (PEM)"
        help="Encrypted at rest. Use [REDACTED] to keep the existing value when re-saving."
        value={String(config.mtls_client_key_pem ?? "")}
        onChange={(v) => onChange(update(config, "mtls_client_key_pem", v))}
        placeholder="-----BEGIN PRIVATE KEY-----&#10;...&#10;-----END PRIVATE KEY-----"
        sensitive
      />
      <PEMTextarea
        label="CA bundle (PEM)"
        value={String(config.tls_ca_bundle_pem ?? "")}
        onChange={(v) => onChange(update(config, "tls_ca_bundle_pem", v))}
        placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----"
      />
      {expiry && <CertExpiryBadge notAfter={expiry} />}
    </div>
  );
}

// PEMTextarea is a multi-line variant of ConfigField for PEM-encoded
// material. Kept local to this file because no other connection kind
// pastes multi-line secrets today; if a second consumer appears, lift
// to a shared component alongside ConfigField.
function PEMTextarea({
  label,
  help,
  value,
  onChange,
  placeholder,
  sensitive,
}: {
  label: string;
  help?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  sensitive?: boolean;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs font-medium">{label}</label>
      <textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        autoComplete={sensitive ? "off" : undefined}
        spellCheck={false}
        rows={5}
        className="w-full rounded-md border bg-background px-3 py-2 text-xs font-mono outline-none ring-ring focus:ring-2"
      />
      {help && <p className="mt-1 text-xs text-muted-foreground">{help}</p>}
    </div>
  );
}

// CertExpiryBadge renders a one-line summary of the client cert's
// NotAfter, color-coded by remaining time. Treats every input as a
// server-formatted RFC3339 string (the admin handler computes this
// server-side via crypto/x509). A parse failure renders nothing
// rather than guessing.
function CertExpiryBadge({ notAfter }: { notAfter: string }) {
  const ms = Date.parse(notAfter);
  if (Number.isNaN(ms)) return null;
  const now = Date.now();
  const days = Math.floor((ms - now) / (24 * 60 * 60 * 1000));
  let label: string;
  let tone: string;
  if (days < 0) {
    label = `Certificate expired ${-days} day${-days === 1 ? "" : "s"} ago`;
    tone = "bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-200";
  } else if (days < 30) {
    label = `Certificate expires in ${days} day${days === 1 ? "" : "s"}`;
    tone = "bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200";
  } else {
    label = `Certificate valid for ${days} more days`;
    tone = "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200";
  }
  return (
    <div className={cn("inline-flex rounded px-2 py-1 text-xs font-medium", tone)}>
      {label}
    </div>
  );
}

// ApiGatewayConfigForm renders the editor for kind=api connections —
// the HTTP API gateway. Field shape matches the apigateway toolkit
// config (see pkg/toolkits/apigateway/config.go): base_url, optional
// openapi_spec, the same auth_mode set the toolkit accepts (none,
// bearer, api_key, basic, oauth2_client_credentials,
// oauth2_authorization_code), timeouts, max_response_bytes, and the
// OAuth Connect button when authorization_code is selected.
//
// The Connect button is wired to the admin /api-gateway/connections/
// {name}/oauth-start endpoint shipped in #381; clicking it opens the
// IdP authorization URL in a new tab so the operator completes the
// browser flow without losing the editor's unsaved state.
function ApiGatewayConfigForm({
  config,
  onChange,
  connectionName,
  isCreate,
}: ConfigFormProps & { connectionName: string; isCreate: boolean }) {
  const startOAuth = useStartAPIGatewayOAuth();
  const [oauthError, setOAuthError] = useState<string | null>(null);
  const [authHelpOpen, setAuthHelpOpen] = useState(false);
  const [tlsHelpOpen, setTlsHelpOpen] = useState(false);
  const handleConnect = useCallback(() => {
    setOAuthError(null);
    if (!connectionName) {
      setOAuthError("Save the connection first, then click Connect.");
      return;
    }
    startOAuth.mutate(
      { name: connectionName, returnURL: window.location.pathname },
      {
        onSuccess: (resp) => {
          // Open the IdP authorization URL in a new tab so the
          // editor's unsaved fields survive the round-trip; the
          // callback handler redirects the new tab back to the
          // portal after persisting tokens.
          window.open(resp.authorization_url, "_blank", "noopener,noreferrer");
        },
        onError: (err) => {
          setOAuthError(err instanceof Error ? err.message : "Connect failed");
        },
      },
    );
  }, [connectionName, startOAuth]);

  return (
    <>
      <ConfigField
        label="Base URL"
        help="HTTPS URL of the upstream API (no trailing slash)."
        value={String(config.base_url ?? "")}
        onChange={(v) => onChange(update(config, "base_url", v))}
        placeholder="https://api.vendor.example.com"
        mono
      />
      <div>
        <div className="mb-1 flex items-center justify-between">
          <label className="block text-xs font-medium">Auth mode</label>
          <button
            type="button"
            onClick={() => setAuthHelpOpen(true)}
            className="text-xs text-blue-600 hover:underline dark:text-blue-400"
          >
            Learn about auth modes
          </button>
        </div>
        <select
          value={String(config.auth_mode ?? "none")}
          onChange={(e) => onChange(update(config, "auth_mode", e.target.value))}
          className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="none">None</option>
          <option value="bearer">Bearer token</option>
          <option value="api_key">API key</option>
          <option value="basic">Basic (RFC 7617)</option>
          <option value="oauth2_client_credentials">OAuth 2.1 client_credentials</option>
          <option value="oauth2_authorization_code">OAuth 2.1 authorization_code (browser sign-in)</option>
          <option value="mtls">mTLS (client certificate is the credential)</option>
        </select>
      </div>

      {config.auth_mode === "bearer" && (
        <ConfigField
          label="Credential"
          help="Bearer token. Encrypted at rest. Use [REDACTED] when re-saving without changing it."
          value={String(config.credential ?? "")}
          onChange={(v) => onChange(update(config, "credential", v))}
          sensitive
        />
      )}

      {config.auth_mode === "api_key" && (
        <div className="rounded-md border bg-muted/20 px-3 py-3 space-y-3">
          <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            API key
          </div>
          <ConfigField
            label="Credential"
            help="The API key value. Encrypted at rest. Use [REDACTED] to keep an existing value when re-saving."
            value={String(config.credential ?? "")}
            onChange={(v) => onChange(update(config, "credential", v))}
            sensitive
          />
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="mb-1 block text-xs font-medium">Placement</label>
              <select
                value={String(config.api_key_placement ?? "header")}
                onChange={(e) => onChange(update(config, "api_key_placement", e.target.value))}
                className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
              >
                <option value="header">Header</option>
                <option value="query">Query string</option>
              </select>
            </div>
            {config.api_key_placement === "query" ? (
              <ConfigField
                label="Query parameter name"
                help="e.g. api_key, apikey, key."
                value={String(config.api_key_param ?? "")}
                onChange={(v) => onChange(update(config, "api_key_param", v))}
                placeholder="api_key"
                mono
              />
            ) : (
              <ConfigField
                label="Header name"
                help="Defaults to X-API-Key."
                value={String(config.api_key_header ?? "")}
                onChange={(v) => onChange(update(config, "api_key_header", v))}
                placeholder="X-API-Key"
                mono
              />
            )}
          </div>
        </div>
      )}

      {config.auth_mode === "basic" && (
        <div className="rounded-md border bg-muted/20 px-3 py-3 space-y-3">
          <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            HTTP Basic (RFC 7617)
          </div>
          <ConfigField
            label="Username"
            help="The userid. May contain any character except ':' (RFC 7617 §2)."
            value={String(config.username ?? "")}
            onChange={(v) => onChange(update(config, "username", v))}
            mono
          />
          <ConfigField
            label="Password"
            help="Encrypted at rest. Use [REDACTED] when re-saving without changing it. May be empty for legacy 'token in userid' patterns."
            value={String(config.password ?? "")}
            onChange={(v) => onChange(update(config, "password", v))}
            sensitive
          />
        </div>
      )}

      {(config.auth_mode === "oauth2_client_credentials" ||
        config.auth_mode === "oauth2_authorization_code") && (
        <div className="rounded-md border bg-muted/20 px-3 py-3 space-y-3">
          <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            OAuth 2.1 — {config.auth_mode === "oauth2_authorization_code" ? "authorization_code" : "client_credentials"}
          </div>
          <ConfigField
            label="Token URL"
            help="OAuth token endpoint."
            value={String(config.oauth2_token_url ?? "")}
            onChange={(v) => onChange(update(config, "oauth2_token_url", v))}
            placeholder="https://idp.example.com/oauth/token"
            mono
          />
          {config.auth_mode === "oauth2_authorization_code" && (
            <ConfigField
              label="Authorization URL"
              help="Where the browser is sent to sign in."
              value={String(config.oauth2_authorization_url ?? "")}
              onChange={(v) => onChange(update(config, "oauth2_authorization_url", v))}
              placeholder="https://idp.example.com/oauth/authorize"
              mono
            />
          )}
          <div className="grid grid-cols-2 gap-3">
            <ConfigField
              label="Client ID"
              value={String(config.oauth2_client_id ?? "")}
              onChange={(v) => onChange(update(config, "oauth2_client_id", v))}
              placeholder="platform-client"
              mono
            />
            <ConfigField
              label="Client Secret"
              help="Encrypted at rest. Use [REDACTED] to keep the existing value when re-saving."
              value={String(config.oauth2_client_secret ?? "")}
              onChange={(v) => onChange(update(config, "oauth2_client_secret", v))}
              sensitive
            />
          </div>
          <ConfigField
            label="Scopes"
            help="Space-delimited scope string. Leave empty if the IdP does not require it."
            value={String(
              Array.isArray(config.oauth2_scopes)
                ? (config.oauth2_scopes as string[]).join(" ")
                : (config.oauth2_scopes ?? ""),
            )}
            onChange={(v) =>
              onChange(update(config, "oauth2_scopes", v.trim() ? v.split(/\s+/) : []))
            }
            placeholder="read:users write:orders"
            mono
          />
          <div>
            <label className="mb-1 block text-xs font-medium">Endpoint auth style</label>
            <select
              value={String(config.oauth2_endpoint_auth_style ?? "header")}
              onChange={(e) => onChange(update(config, "oauth2_endpoint_auth_style", e.target.value))}
              className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
            >
              <option value="header">Header (HTTP Basic) — OAuth 2.1 default</option>
              <option value="params">Form params — some IdPs require this</option>
            </select>
          </div>
          {config.auth_mode === "oauth2_authorization_code" && (
            <>
              <div>
                <label className="mb-1 block text-xs font-medium">OIDC prompt</label>
                <select
                  value={String(config.oauth2_prompt ?? "")}
                  onChange={(e) => onChange(update(config, "oauth2_prompt", e.target.value))}
                  className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
                >
                  <option value="">(default — no prompt parameter)</option>
                  <option value="login">login (force fresh credentials each Connect)</option>
                  <option value="consent">consent (force consent screen)</option>
                  <option value="select_account">select_account (force account picker)</option>
                  <option value="none">none (silent auth)</option>
                </select>
                <p className="mt-1 text-xs text-muted-foreground">
                  Leave default for non-OIDC OAuth providers that reject unknown parameters. Use <code>login</code> for Keycloak / Auth0 / Okta to defeat stale-form bugs by forcing a fresh credential prompt on every Connect.
                </p>
              </div>
              <div className="rounded-md border border-dashed bg-background px-3 py-3 space-y-2">
                <p className="text-xs">
                  <strong>Connect</strong> opens the IdP sign-in page in a new tab. After the
                  browser flow completes, the platform persists the refresh token (encrypted)
                  so subsequent tool calls refresh access tokens silently.
                </p>
                <p className="text-xs text-muted-foreground">
                  Save the connection first; Connect needs the connection registered before
                  the IdP redirect can find it.
                </p>
                <button
                  type="button"
                  onClick={handleConnect}
                  disabled={isCreate || startOAuth.isPending || !connectionName}
                  className="inline-flex items-center gap-1.5 rounded-md border bg-background px-3 py-1.5 text-xs font-medium hover:bg-muted disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  {startOAuth.isPending ? "Opening IdP…" : "Connect"}
                </button>
                {oauthError && (
                  <p className="text-xs text-red-600 dark:text-red-400">{oauthError}</p>
                )}
              </div>
            </>
          )}
        </div>
      )}

      <TLSMaterialEditor
        config={config}
        onChange={onChange}
        onOpenHelp={() => setTlsHelpOpen(true)}
      />

      <div className="rounded-md border bg-muted/20 px-3 py-3 space-y-2">
        <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Static headers
        </div>
        <p className="text-xs text-muted-foreground">
          Headers added to every outbound request, in addition to whatever
          Auth mode contributes. Required by APIs that demand both an
          OAuth bearer AND a separate key, e.g. Google's
          <code className="mx-1">x-goog-user-project</code> for quota
          billing or a vendor subscription header. Values are encrypted
          at rest; existing values are masked.
        </p>
        <SensitiveKeyValueEditor
          entries={asStringMap(config.static_headers)}
          onChange={(next) =>
            onChange(
              update(
                config,
                "static_headers",
                Object.keys(next).length === 0 ? undefined : next,
              ),
            )
          }
          keyPlaceholder="X-Goog-User-Project"
          valuePlaceholder="header value"
        />
      </div>

      <APICatalogPicker config={config} onChange={onChange} />
      <LegacyOpenAPISpecBanner config={config} onChange={onChange} />


      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Connect timeout"
          help="Initial dial timeout (e.g. 10s, 1m)."
          value={String(config.connect_timeout ?? "")}
          onChange={(v) => onChange(update(config, "connect_timeout", v))}
          placeholder="10s"
          mono
        />
        <ConfigField
          label="Call timeout"
          help="Per-call upstream timeout (e.g. 60s)."
          value={String(config.call_timeout ?? "")}
          onChange={(v) => onChange(update(config, "call_timeout", v))}
          placeholder="60s"
          mono
        />
      </div>

      <ConfigField
        label="Max response bytes"
        help="Cap on response body size returned through api_invoke_endpoint. Above this, the call sets body_truncated=true and hints the model toward api_export. Default 10485760 (10 MiB)."
        type="number"
        value={String(config.max_response_bytes ?? "")}
        onChange={(v) => onChange(update(config, "max_response_bytes", v ? Number(v) : undefined))}
        placeholder="10485760"
      />

      <div>
        <label className="mb-1 block text-xs font-medium">Trust level</label>
        <select
          value={String(config.trust_level ?? "untrusted")}
          onChange={(e) => onChange(update(config, "trust_level", e.target.value))}
          className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="untrusted">Untrusted (default)</option>
          <option value="trusted">Trusted</option>
        </select>
        <p className="mt-1 text-xs text-muted-foreground">
          Reserved for future content-fencing of upstream responses.
        </p>
      </div>

      <HelpDialog
        open={authHelpOpen}
        onOpenChange={setAuthHelpOpen}
        title="Authentication modes"
      >
        <ApiGatewayAuthHelp />
      </HelpDialog>

      <HelpDialog
        open={tlsHelpOpen}
        onOpenChange={setTlsHelpOpen}
        title="TLS and mTLS"
      >
        <ApiGatewayTLSHelp />
      </HelpDialog>
    </>
  );
}
