import { ExternalLink } from "lucide-react";
import { useEnrichmentRules } from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { ToolDetail } from "@/api/admin/types";
import { markdownToPlainText } from "@/lib/markdownText";
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
      <Alert variant="destructive">
        <AlertDescription>Failed to load rules: {errorMessage(error)}</AlertDescription>
      </Alert>
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
        <EmptyState>No enrichment rules attached to this tool.</EmptyState>
      ) : (
        <div className="overflow-hidden rounded border">
          <Table>
            <TableHeader>
              <TableRow className="bg-muted/40 hover:bg-muted/40">
                <TableHead className="h-8 px-3 text-xs">Rule</TableHead>
                <TableHead className="h-8 px-3 text-xs">Strategy</TableHead>
                <TableHead className="h-8 px-3 text-xs">Status</TableHead>
                <TableHead className="h-8 px-3 text-xs">Updated</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rulesForTool.map((r) => (
                <TableRow key={r.id}>
                  <TableCell className="px-3 py-1.5 font-medium whitespace-normal">
                    {markdownToPlainText(r.description) || (
                      <span className="text-muted-foreground">Rule {r.id.slice(0, 8)}</span>
                    )}
                  </TableCell>
                  <TableCell className="px-3 py-1.5 text-xs">
                    {r.merge_strategy.kind || "default"}
                    {r.merge_strategy.path ? ` · ${r.merge_strategy.path}` : ""}
                  </TableCell>
                  <TableCell className="px-3 py-1.5">
                    <StatusBadge variant={r.enabled ? "success" : "neutral"}>
                      {r.enabled ? "enabled" : "disabled"}
                    </StatusBadge>
                  </TableCell>
                  <TableCell className="px-3 py-1.5 text-xs text-muted-foreground">
                    {new Date(r.updated_at).toLocaleString()}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {rulesForTool.length > 0 && (
        <p className="text-xs text-muted-foreground">
          {enabledCount} of {rulesForTool.length} rules enabled.
        </p>
      )}

      <Button asChild variant="link" size="xs" className="px-0">
        <a href={drawerHref}>
          Manage rules for this connection <ExternalLink />
        </a>
      </Button>
    </div>
  );
}
