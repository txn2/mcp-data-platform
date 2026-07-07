import { useState } from "react";
import { useDeleteEnrichmentRule } from "@/api/admin/hooks";
import type { EnrichmentRule } from "@/api/admin/types";
import { cn } from "@/lib/utils";
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
            <span
              className={cn(
                "rounded px-1.5 py-0 text-xs font-medium",
                rule.enabled
                  ? "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-400"
                  : "bg-muted text-muted-foreground",
              )}
            >
              {rule.enabled ? "enabled" : "disabled"}
            </span>
          </div>
          {rule.description && (
            <p className="mt-1 text-xs text-muted-foreground">{rule.description}</p>
          )}
          <p className="mt-1 text-xs text-muted-foreground font-mono">
            {rule.enrich_action.source}.{rule.enrich_action.operation} →{" "}
            {rule.merge_strategy.path || "enrichment"}
          </p>
        </div>
        <div className="flex gap-1 shrink-0">
          <button
            type="button"
            onClick={onEdit}
            className="rounded-md border px-2 py-1 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            Edit
          </button>
          {confirmDelete ? (
            <>
              <button
                type="button"
                onClick={async () => {
                  await del.mutateAsync(rule.id);
                  setConfirmDelete(false);
                }}
                className="rounded-md bg-destructive px-2 py-1 text-xs font-medium text-destructive-foreground hover:bg-destructive/90"
              >
                Confirm
              </button>
              <button
                type="button"
                onClick={() => setConfirmDelete(false)}
                className="rounded-md border px-2 py-1 text-xs font-medium text-muted-foreground hover:bg-muted"
              >
                Cancel
              </button>
            </>
          ) : (
            <button
              type="button"
              onClick={() => setConfirmDelete(true)}
              className="rounded-md border px-2 py-1 text-xs font-medium text-muted-foreground hover:bg-destructive/10 hover:text-destructive hover:border-destructive/30"
            >
              <Trash2 className="h-3 w-3" />
            </button>
          )}
        </div>
      </div>
    </li>
  );
}
