import { useState, useEffect, useCallback, useMemo } from "react";
import {
  useEffectiveConnections,
  useSetConnectionInstance,
  useDeleteConnectionInstance,
  useSystemInfo,
} from "@/api/admin/hooks";
import type { EffectiveConnection } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import {
  Plus,
  Trash2,
  Save,
  Cable,
  AlertCircle,
  Check,
  Eye,
  EyeOff,
  Database,
  X,
} from "lucide-react";
import { GatewayActionBar, GatewayRulesDrawer } from "./GatewayActions";

// Fields that should be redacted in view mode.
const SENSITIVE_KEYS = new Set([
  "password",
  "secret_access_key",
  "secret_key",
  "token",
  "access_token",
  "refresh_token",
  "api_key",
  "api_secret",
  "private_key",
  "credential",
]);

function isSensitive(key: string): boolean {
  return SENSITIVE_KEYS.has(key.toLowerCase());
}

// ---------------------------------------------------------------------------
// Kind badge colors
// ---------------------------------------------------------------------------

const KIND_COLORS: Record<string, string> = {
  trino: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
  datahub: "bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-400",
  s3: "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400",
  mcp: "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-400",
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

  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [mode, setMode] = useState<"view" | "edit" | "create">("view");
  const [dirty, setDirty] = useState(false);

  // Group by kind
  const grouped = useMemo(() => {
    const groups: Record<string, EffectiveConnection[]> = {};
    for (const c of connections) {
      const k = c.kind;
      if (!groups[k]) groups[k] = [];
      groups[k]!.push(c);
    }
    return groups;
  }, [connections]);

  const selected = useMemo(
    () => connections.find((c) => `${c.kind}/${c.name}` === selectedKey) ?? null,
    [connections, selectedKey],
  );

  // Auto-select first item
  useEffect(() => {
    if (!selectedKey && connections.length > 0 && connections[0]) {
      setSelectedKey(`${connections[0].kind}/${connections[0].name}`);
    }
  }, [connections, selectedKey]);

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
                  <span className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                    {kind}
                  </span>
                </div>
                {items.map((c) => {
                  const key = `${c.kind}/${c.name}`;
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
                      <div className="flex items-center gap-1.5">
                        <span className="text-sm font-medium truncate">{c.name}</span>
                        <span className={cn(
                          "shrink-0 rounded px-1 py-0 text-[9px] font-medium",
                          c.source === "file" ? "bg-muted text-muted-foreground" :
                          "bg-primary/10 text-primary",
                        )}>
                          {c.source === "file" ? "file" : "database"}
                        </span>
                      </div>
                      {c.description && (
                        <span className="mt-0.5 text-[10px] text-muted-foreground truncate">
                          {c.description}
                        </span>
                      )}
                      {c.tools && c.tools.length > 0 && (
                        <span className="mt-0.5 text-[10px] text-muted-foreground">
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
  const [showSensitive, setShowSensitive] = useState(false);
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
            <span className={cn("rounded-full px-2.5 py-0.5 text-[10px] font-medium", kindColor(connection.kind))}>
              {connection.kind}
            </span>
          </div>
          {connection.description && (
            <p className="mt-1 text-sm text-muted-foreground">{connection.description}</p>
          )}
          {connection.source === "both" && (
            <p className="mt-1 text-[10px] text-muted-foreground">
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
            <button
              type="button"
              onClick={() => setShowSensitive((v) => !v)}
              className="ml-auto text-[10px] text-muted-foreground hover:text-foreground inline-flex items-center gap-1"
            >
              {showSensitive ? <EyeOff className="h-3 w-3" /> : <Eye className="h-3 w-3" />}
              {showSensitive ? "Hide sensitive" : "Show sensitive"}
            </button>
          </div>
          <div className="rounded-md border divide-y">
            {configEntries.map(([key, value]) => {
              const sensitive = isSensitive(key);
              const displayValue = sensitive && !showSensitive
                ? "********"
                : typeof value === "object" && value !== null
                  ? JSON.stringify(value)
                  : String(value);
              return (
                <div key={key} className="flex items-center gap-4 px-4 py-2">
                  <span className="text-xs font-mono text-muted-foreground w-48 shrink-0 truncate">
                    {key}
                  </span>
                  <span
                    className={cn(
                      "text-xs font-mono flex-1 truncate",
                      sensitive && !showSensitive && "text-muted-foreground italic",
                    )}
                  >
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

const AVAILABLE_KINDS = ["trino", "s3", "mcp"];

function ConnectionEditor({ connection, onSave, onCancel, onDirtyChange }: EditorProps) {
  const isCreate = !connection;
  const setMutation = useSetConnectionInstance();
  const [kind, setKind] = useState(connection?.kind ?? "trino");
  const [name, setName] = useState(connection?.name ?? "");
  const [description, setDescription] = useState(
    connection?.description || (connection?.config?.description as string) || "",
  );
  const [configObj, setConfigObj] = useState<Record<string, any>>(
    connection?.config ? { ...connection.config } : {},
  );
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
    if (isCreate) setConfigObj({});
  }, [kind, isCreate]);

  const handleSave = useCallback(() => {
    setSaveError(null);
    const config = configObj;

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
  }, [kind, name, description, configObj, setMutation, onSave]);

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
            disabled={isPending || (isCreate && !name.trim())}
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
            <p className="mt-1 text-[10px] text-muted-foreground">
              Connection type. Cannot be changed after creation.
            </p>
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium">Name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              disabled={!isCreate}
              placeholder="my-connection"
              className="w-full rounded-md border bg-background px-3 py-2 text-sm font-mono outline-none ring-ring focus:ring-2 disabled:opacity-50 disabled:cursor-not-allowed"
            />
            <p className="mt-1 text-[10px] text-muted-foreground">
              Unique name within this kind. Cannot be changed after creation.
            </p>
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
              <TrinoConfigForm config={configObj} onChange={setConfigObj} />
            )}
            {kind === "s3" && (
              <S3ConfigForm config={configObj} onChange={setConfigObj} />
            )}
            {kind === "mcp" && (
              <GatewayConfigForm config={configObj} onChange={setConfigObj} />
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
      <p className="text-[10px] font-medium text-muted-foreground">{label}</p>
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
      {help && <p className="mt-1 text-[10px] text-muted-foreground">{help}</p>}
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
        {help && <p className="text-[10px] text-muted-foreground">{help}</p>}
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
          <p className="mb-2 text-[10px] text-muted-foreground">
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
      <div className="grid grid-cols-2 gap-3">
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
          </select>
          <p className="mt-1 text-[10px] text-muted-foreground">
            Bearer sends Authorization header; API key sends X-API-Key.
          </p>
        </div>
        <ConfigField
          label="Connection name"
          help="Optional override; defaults to the instance name above. Becomes the tool prefix."
          value={String(config.connection_name ?? "")}
          onChange={(v) => onChange(update(config, "connection_name", v))}
          placeholder="vendor"
          mono
        />
      </div>
      {(config.auth_mode === "bearer" || config.auth_mode === "api_key") && (
        <ConfigField
          label="Credential"
          help="Encrypted at rest when ENCRYPTION_KEY is set. Use [REDACTED] when re-saving without changing it."
          value={String(config.credential ?? "")}
          onChange={(v) => onChange(update(config, "credential", v))}
          sensitive
        />
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
        <p className="mt-1 text-[10px] text-muted-foreground">
          Reserved for future content-fencing of upstream responses. Leave at "untrusted" unless you control the upstream.
        </p>
      </div>
    </>
  );
}
