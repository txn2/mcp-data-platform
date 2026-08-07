import { useState } from "react";
import { useDeleteEnrichmentRule } from "@/api/admin/hooks";
import type { EnrichmentRule } from "@/api/admin/types";
import { markdownToPlainText } from "@/lib/markdownText";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Trash2 } from "lucide-react";

// ---------------------------------------------------------------------------
// RuleListItem — one rule with summary + edit/delete buttons
// ---------------------------------------------------------------------------

export function RuleListItem({
  connectionName,
  rule,
  onEdit,
}: {
  connectionName: string;
  rule: EnrichmentRule;
  onEdit: () => void;
}) {
  const del = useDeleteEnrichmentRule(connectionName);
  const [confirmDelete, setConfirmDelete] = useState(false);

  return (
    <li className="rounded-md border p-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="font-mono text-xs">{rule.tool_name}</span>
            <Badge variant={rule.enabled ? "success" : "muted"} className="rounded">
              {rule.enabled ? "enabled" : "disabled"}
            </Badge>
          </div>
          {rule.description && (
            <p className="mt-1 text-xs text-muted-foreground">
              {markdownToPlainText(rule.description)}
            </p>
          )}
          <p className="mt-1 font-mono text-xs text-muted-foreground">
            {rule.enrich_action.source}.{rule.enrich_action.operation} →{" "}
            {rule.merge_strategy.path || "enrichment"}
          </p>
        </div>
        <div className="flex shrink-0 gap-1">
          <Button type="button" variant="outline" size="xs" onClick={onEdit}>
            Edit
          </Button>
          {confirmDelete ? (
            <>
              <Button
                type="button"
                variant="destructive"
                size="xs"
                onClick={async () => {
                  await del.mutateAsync(rule.id);
                  setConfirmDelete(false);
                }}
              >
                Confirm
              </Button>
              <Button
                type="button"
                variant="outline"
                size="xs"
                onClick={() => setConfirmDelete(false)}
              >
                Cancel
              </Button>
            </>
          ) : (
            <Button
              type="button"
              variant="outline"
              size="icon-xs"
              aria-label={`Delete rule for ${rule.tool_name}`}
              onClick={() => setConfirmDelete(true)}
              className="text-muted-foreground hover:border-destructive/30 hover:bg-destructive/10 hover:text-destructive"
            >
              <Trash2 />
            </Button>
          )}
        </div>
      </div>
    </li>
  );
}
