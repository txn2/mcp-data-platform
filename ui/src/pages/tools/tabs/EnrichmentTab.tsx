import { ExternalLink } from "lucide-react";
import { useEnrichmentRules } from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { ToolDetail } from "@/api/admin/types";
import { errorMessage } from "@/lib/utils";

export function EnrichmentTab({ detail }: { detail: ToolDetail }) {
  const connection = detail.connection ?? "";
  const { data, isLoading, error } = useEnrichmentRules(connection, !!connection);

  if (!connection) {
    return (
      <p className="text-sm text-muted-foreground">
        Enrichment rules are only configurable on gateway-proxied tools with a
        connection.
      </p>
    );
  }

  if (isLoading) {
    return (
      <p className="text-sm text-muted-foreground">Loading enrichment rules…</p>
    );
  }
  if (error) {
    return (
      <p className="text-sm text-destructive">
        Failed to load rules: {errorMessage(error)}
      </p>
    );
  }

  const rulesForTool = (data ?? []).filter((r) => r.tool_name === detail.name);
  const enabledCount = rulesForTool.filter((r) => r.enabled).length;
  const drawerHref = `/portal/admin/connections#enrichment-${encodeURIComponent(connection)}`;

  return (
    <div className="space-y-4">
      <p className="text-xs text-muted-foreground">
        Cross-enrichment rules attached to this tool on connection{" "}
        <code className="rounded bg-muted px-1 py-0.5 font-mono text-[11px]">
          {connection}
        </code>
        .
      </p>

      {rulesForTool.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No enrichment rules attached to this tool.
        </p>
      ) : (
        <div className="overflow-hidden rounded border">
          <table className="w-full text-sm">
            <thead className="bg-muted/40 text-xs">
              <tr>
                <th className="px-3 py-1.5 text-left font-medium">Rule</th>
                <th className="px-3 py-1.5 text-left font-medium">Strategy</th>
                <th className="px-3 py-1.5 text-left font-medium">Status</th>
                <th className="px-3 py-1.5 text-left font-medium">Updated</th>
              </tr>
            </thead>
            <tbody>
              {rulesForTool.map((r) => (
                <tr key={r.id} className="border-t">
                  <td className="px-3 py-1.5">
                    <div className="font-medium">
                      {r.description || (
                        <span className="text-muted-foreground">
                          Rule {r.id.slice(0, 8)}
                        </span>
                      )}
                    </div>
                  </td>
                  <td className="px-3 py-1.5 text-xs">
                    {r.merge_strategy.kind || "default"}
                    {r.merge_strategy.path ? ` · ${r.merge_strategy.path}` : ""}
                  </td>
                  <td className="px-3 py-1.5">
                    <StatusBadge variant={r.enabled ? "success" : "neutral"}>
                      {r.enabled ? "enabled" : "disabled"}
                    </StatusBadge>
                  </td>
                  <td className="px-3 py-1.5 text-xs text-muted-foreground">
                    {new Date(r.updated_at).toLocaleString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {rulesForTool.length > 0 && (
        <p className="text-xs text-muted-foreground">
          {enabledCount} of {rulesForTool.length} rules enabled.
        </p>
      )}

      <a
        href={drawerHref}
        className="inline-flex items-center gap-1 text-xs text-primary hover:underline"
      >
        Manage rules for this connection <ExternalLink className="h-3 w-3" />
      </a>
    </div>
  );
}
