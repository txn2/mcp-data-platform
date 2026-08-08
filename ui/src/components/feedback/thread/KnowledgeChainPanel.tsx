import { GitBranch } from "lucide-react";
import { useThreadChain } from "@/api/portal/hooks";
import { Badge } from "@/components/ui/badge";

// KnowledgeChainPanel surfaces the thread -> insight -> changeset chain (#602):
// once a thread is resolved by a captured insight, show the insight and any
// knowledge changesets that insight produced (the applied data-catalog edits).
export function KnowledgeChainPanel({
  threadId,
  insightId,
}: {
  threadId: string;
  insightId: string;
}) {
  const { data: chain, isLoading } = useThreadChain(threadId, true);
  return (
    <div className="border-b bg-muted/30 p-3">
      <div className="mb-1 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        <GitBranch className="h-3.5 w-3.5" /> Knowledge chain
      </div>
      <p className="text-xs">
        Resolved by insight{" "}
        <code className="rounded bg-muted px-1 font-mono" title={insightId}>
          {insightId.length > 14 ? `${insightId.slice(0, 14)}…` : insightId}
        </code>
      </p>
      {isLoading && <p className="mt-1 text-xs text-muted-foreground">Loading applied changes…</p>}
      {chain && chain.changesets.length === 0 && (
        <p className="mt-1 text-xs text-muted-foreground">
          No catalog changes applied from this insight yet.
        </p>
      )}
      {chain && chain.changesets.length > 0 && (
        <ul className="mt-1.5 space-y-1">
          {chain.changesets.map((cs) => (
            <li key={cs.id} className="flex items-start gap-1.5 text-xs">
              <Badge variant="info" className="rounded px-1">
                {cs.change_type}
              </Badge>
              <span
                className="min-w-0 flex-1 truncate font-mono text-muted-foreground"
                title={cs.target_urn}
              >
                {cs.target_urn}
              </span>
              {cs.rolled_back && (
                <Badge variant="warning" className="rounded px-1">
                  rolled back
                </Badge>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
