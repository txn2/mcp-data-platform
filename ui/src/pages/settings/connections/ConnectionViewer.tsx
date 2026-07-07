import { useState } from "react";
import { Trash2, Database } from "lucide-react";
import { useDeleteConnectionInstance } from "@/api/admin/hooks";
import type { EffectiveConnection } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import { CollapsibleMarkdown } from "@/components/renderers/CollapsibleMarkdown";
import { GatewayActionBar, GatewayRulesDrawer } from "../GatewayActions";
import { ConnectionOAuthStatusCard } from "../ConnectionOAuthStatusCard";
import { CONFIG_LABELS, kindColor } from "./constants";
import { GatewayHealthDetail } from "./HealthBadges";

export function ConnectionViewer({
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
  // datahub_source_name/catalog_mapping render in their own DataHub section;
  // description renders as the markdown subtitle above. Filter them out of the
  // raw Configuration rows so they are not shown twice.
  const hiddenConfigKeys = new Set(["datahub_source_name", "catalog_mapping", "description"]);
  const configEntries = Object.entries(connection.config ?? {}).filter(([key]) => !hiddenConfigKeys.has(key));

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
            <div className="mt-1">
              <CollapsibleMarkdown content={connection.description} maxHeightPx={200} fadeFrom="from-muted" />
            </div>
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

      {/* Runtime reachability for gateway upstreams. Same state the
          list_connections MCP tool reports, so the admin UI and the tool
          never disagree about whether an upstream is up. */}
      {connection.health && (
        <GatewayHealthDetail health={connection.health} />
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
              const labelMap = CONFIG_LABELS[connection.kind] ?? {};
              const displayLabel = labelMap[key];
              return (
                <div key={key} className="flex items-center gap-4 px-4 py-2">
                  <span className="text-xs text-muted-foreground w-48 shrink-0 truncate" title={key}>
                    {displayLabel ?? key}
                    {displayLabel && (
                      <span className="ml-1 font-mono text-[10px] opacity-50">{key}</span>
                    )}
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

function InfoCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border bg-muted/20 px-3 py-2">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <p className="text-sm font-medium truncate">{value}</p>
    </div>
  );
}
