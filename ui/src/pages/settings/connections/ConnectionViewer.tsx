import { useState } from "react";
import { Trash2, Database } from "lucide-react";
import { useDeleteConnectionInstance } from "@/api/admin/hooks";
import type { EffectiveConnection } from "@/api/admin/types";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { CollapsibleMarkdown } from "@/components/renderers/CollapsibleMarkdown";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { GatewayActionBar, GatewayRulesDrawer } from "../GatewayActions";
import { ConnectionOAuthStatusCard } from "../ConnectionOAuthStatusCard";
import { CONFIG_LABELS, kindColor } from "./constants";
import { GatewayHealthDetail } from "./HealthBadges";

// ConfigRows renders the raw config key/value pairs of a connection, using the
// per-kind human labels where one exists and falling back to the raw key.
function ConfigRows({
  kind,
  entries,
}: {
  kind: string;
  entries: [string, unknown][];
}) {
  const labelMap = CONFIG_LABELS[kind] ?? {};
  return (
    <div className="divide-y rounded-md border">
      {entries.map(([key, value]) => {
        const displayValue =
          typeof value === "object" && value !== null
            ? JSON.stringify(value)
            : String(value);
        const displayLabel = labelMap[key];
        return (
          <div key={key} className="flex items-center gap-4 px-4 py-2">
            <span
              className="w-48 shrink-0 truncate text-xs text-muted-foreground"
              title={key}
            >
              {displayLabel ?? key}
              {displayLabel && (
                <span className="ml-1 font-mono text-[10px] opacity-50">{key}</span>
              )}
            </span>
            <span className="flex-1 truncate font-mono text-xs">{displayValue}</span>
          </div>
        );
      })}
    </div>
  );
}

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

  const datahubSourceName =
    typeof connection.config?.datahub_source_name === "string"
      ? connection.config.datahub_source_name
      : undefined;
  const rawMapping = connection.config?.catalog_mapping;
  const catalogMapping =
    rawMapping != null &&
    typeof rawMapping === "object" &&
    !Array.isArray(rawMapping)
      ? (rawMapping as Record<string, string>)
      : undefined;
  const hasDataHub =
    Boolean(datahubSourceName) ||
    (catalogMapping != null && Object.keys(catalogMapping).length > 0);
  // datahub_source_name/catalog_mapping render in their own DataHub section;
  // description renders as the markdown subtitle above. Filter them out of the
  // raw Configuration rows so they are not shown twice.
  const hiddenConfigKeys = new Set([
    "datahub_source_name",
    "catalog_mapping",
    "description",
  ]);
  const configEntries = Object.entries(connection.config ?? {}).filter(
    ([key]) => !hiddenConfigKeys.has(key),
  );

  return (
    <div className="space-y-6 p-6">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h2 className="text-lg font-semibold">{connection.name}</h2>
            {/* Kind is a category, not a status, so it carries its own tint
                from the shared kind palette on an outline badge. */}
            <Badge variant="outline" className={kindColor(connection.kind)}>
              {connection.kind}
            </Badge>
          </div>
          {connection.description && (
            <div className="mt-1">
              <CollapsibleMarkdown
                content={connection.description}
                maxHeightPx={200}
                fadeFrom="from-muted"
              />
            </div>
          )}
          {connection.source === "both" && (
            <p className="mt-1 text-xs text-muted-foreground">
              This connection is managed in the database. A fallback version
              also exists in the config file and can be removed once database
              management is confirmed.
            </p>
          )}
        </div>
        {!isReadOnly && (
          <div className="flex gap-2">
            <Button type="button" size="sm" onClick={onEdit}>
              Edit
            </Button>
            <Button
              type="button"
              variant="outline"
              size="icon-sm"
              onClick={() => setConfirmDelete(true)}
              aria-label={`Delete ${connection.kind}/${connection.name}`}
              className="text-muted-foreground hover:border-destructive/30 hover:bg-destructive/10 hover:text-destructive"
            >
              <Trash2 />
            </Button>
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
      {connection.health && <GatewayHealthDetail health={connection.health} />}

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
        <InfoCard
          label="Created By"
          value={connection.created_by || "unknown"}
        />
        <InfoCard
          label="Last Updated"
          value={
            connection.updated_at
              ? new Date(connection.updated_at).toLocaleString()
              : "N/A"
          }
        />
      </div>

      {/* Config */}
      {configEntries.length > 0 && (
        <SectionCard
          title={
            <span className="flex items-center gap-2">
              <Database className="size-4 text-muted-foreground" />
              Configuration
            </span>
          }
        >
          <ConfigRows kind={connection.kind} entries={configEntries} />
        </SectionCard>
      )}

      {/* DataHub Integration */}
      {hasDataHub && (
        <SectionCard
          title={
            <span className="flex items-center gap-2">
              <Database className="size-4 text-muted-foreground" />
              DataHub Integration
            </span>
          }
        >
          <div className="divide-y rounded-md border">
            {datahubSourceName && (
              <div className="flex items-center gap-4 px-4 py-2">
                <span className="w-48 shrink-0 font-mono text-xs text-muted-foreground">
                  DataHub Source Name
                </span>
                <span className="flex-1 font-mono text-xs">{datahubSourceName}</span>
              </div>
            )}
            {catalogMapping && Object.keys(catalogMapping).length > 0 && (
              <div className="px-4 py-2">
                <span className="mb-1 block font-mono text-xs text-muted-foreground">
                  Catalog Mapping
                </span>
                <div className="ml-4 space-y-0.5">
                  {Object.entries(catalogMapping).map(([local, datahub]) => (
                    <div
                      key={local}
                      className="flex items-center gap-2 font-mono text-xs"
                    >
                      <span>{local}</span>
                      <span className="text-muted-foreground">&rarr;</span>
                      <span>{datahub}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        </SectionCard>
      )}

      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        destructive
        title="Delete Connection"
        description={
          <>
            Are you sure you want to delete{" "}
            <code className="font-mono">
              {connection.kind}/{connection.name}
            </code>
            ? This cannot be undone.
          </>
        }
        confirmLabel="Delete"
        loading={deleteMutation.isPending}
        onConfirm={() => {
          deleteMutation.mutate(
            { kind: connection.kind, name: connection.name },
            { onSuccess: onDeleted },
          );
          setConfirmDelete(false);
        }}
      />
    </div>
  );
}

function InfoCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border bg-muted/20 px-3 py-2">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <p className="truncate text-sm font-medium">{value}</p>
    </div>
  );
}
