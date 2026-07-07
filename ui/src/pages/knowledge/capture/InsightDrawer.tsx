import { useState, useCallback } from "react";
import { useUpdateInsightStatus } from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { Insight } from "@/api/admin/types";
import { formatUser } from "@/lib/formatUser";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { formatCategory, insightStatusVariant } from "./helpers";

export function InsightDrawer({
  insight,
  onClose,
  userLabels,
}: {
  insight: Insight;
  onClose: () => void;
  userLabels: Record<string, string>;
}) {
  const [reviewNotes, setReviewNotes] = useState(insight.review_notes ?? "");
  const updateStatus = useUpdateInsightStatus();

  const handleAction = useCallback(
    (status: string) => {
      updateStatus.mutate(
        { id: insight.id, status, reviewNotes: reviewNotes || undefined },
        { onSuccess: () => onClose() },
      );
    },
    [insight.id, reviewNotes, updateStatus, onClose],
  );

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />
      <div className="relative w-full max-w-lg overflow-auto bg-card p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold">Insight Detail</h2>
          <button
            onClick={onClose}
            className="rounded-md px-2 py-1 text-sm hover:bg-muted"
          >
            Close
          </button>
        </div>

        <div className="space-y-4">
          {/* Metadata grid */}
          <div className="grid grid-cols-2 gap-3 text-sm">
            <div>
              <p className="text-xs text-muted-foreground">ID</p>
              <p className="font-mono text-xs">{insight.id}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Created At</p>
              <p>{new Date(insight.created_at).toLocaleString()}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Captured By</p>
              <p title={insight.captured_by}>
                {formatUser(
                  insight.captured_by,
                  userLabels[insight.captured_by],
                )}
              </p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Persona</p>
              <p>{insight.persona}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Category</p>
              <p>{formatCategory(insight.category)}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Confidence</p>
              <StatusBadge
                variant={
                  insight.confidence === "high"
                    ? "success"
                    : insight.confidence === "medium"
                      ? "warning"
                      : "neutral"
                }
              >
                {insight.confidence}
              </StatusBadge>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Status</p>
              <StatusBadge variant={insightStatusVariant(insight.status)}>
                {formatCategory(insight.status)}
              </StatusBadge>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Session ID</p>
              <p className="font-mono text-xs">{insight.session_id}</p>
            </div>
          </div>

          {/* Full insight text */}
          <div>
            <p className="mb-1 text-xs text-muted-foreground">Insight</p>
            <div className="rounded bg-muted p-3">
              <MarkdownRenderer content={insight.insight_text} bare />
            </div>
          </div>

          {/* Entity URNs */}
          {insight.entity_urns.length > 0 && (
            <div>
              <p className="mb-1 text-xs text-muted-foreground">Entity URNs</p>
              <div className="space-y-1">
                {insight.entity_urns.map((urn, i) => (
                  <p
                    key={i}
                    className="font-mono text-xs text-muted-foreground"
                  >
                    {urn}
                  </p>
                ))}
              </div>
            </div>
          )}

          {/* Suggested Actions */}
          {insight.suggested_actions.length > 0 && (
            <div>
              <p className="mb-1 text-xs text-muted-foreground">
                Suggested Actions
              </p>
              <div className="overflow-auto rounded border">
                <table className="w-full text-xs">
                  <thead>
                    <tr className="border-b bg-muted/50">
                      <th className="px-2 py-1 text-left font-medium">Type</th>
                      <th className="px-2 py-1 text-left font-medium">
                        Target
                      </th>
                      <th className="px-2 py-1 text-left font-medium">
                        Detail
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {insight.suggested_actions.map((a, i) => (
                      <tr key={i} className="border-b">
                        <td className="px-2 py-1 font-mono">
                          {a.action_type}
                        </td>
                        <td className="max-w-[120px] truncate px-2 py-1 font-mono">
                          {a.target}
                        </td>
                        <td className="px-2 py-1">{a.detail}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {/* Related Columns */}
          {insight.related_columns.length > 0 && (
            <div>
              <p className="mb-1 text-xs text-muted-foreground">
                Related Columns
              </p>
              <div className="overflow-auto rounded border">
                <table className="w-full text-xs">
                  <thead>
                    <tr className="border-b bg-muted/50">
                      <th className="px-2 py-1 text-left font-medium">URN</th>
                      <th className="px-2 py-1 text-left font-medium">
                        Column
                      </th>
                      <th className="px-2 py-1 text-left font-medium">
                        Relevance
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {insight.related_columns.map((c, i) => (
                      <tr key={i} className="border-b">
                        <td className="max-w-[120px] truncate px-2 py-1 font-mono">
                          {c.urn}
                        </td>
                        <td className="px-2 py-1 font-mono">{c.column}</td>
                        <td className="px-2 py-1">{c.relevance}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {/* Lifecycle section */}
          {insight.reviewed_by && (
            <div className="grid grid-cols-2 gap-3 border-t pt-3 text-sm">
              <div>
                <p className="text-xs text-muted-foreground">Reviewed By</p>
                <p title={insight.reviewed_by}>
                  {formatUser(
                    insight.reviewed_by!,
                    userLabels[insight.reviewed_by!],
                  )}
                </p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">Reviewed At</p>
                <p>
                  {insight.reviewed_at
                    ? new Date(insight.reviewed_at).toLocaleString()
                    : "-"}
                </p>
              </div>
            </div>
          )}

          {insight.applied_by && (
            <div className="grid grid-cols-2 gap-3 border-t pt-3 text-sm">
              <div>
                <p className="text-xs text-muted-foreground">Applied By</p>
                <p title={insight.applied_by}>
                  {formatUser(
                    insight.applied_by!,
                    userLabels[insight.applied_by!],
                  )}
                </p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">Applied At</p>
                <p>
                  {insight.applied_at
                    ? new Date(insight.applied_at).toLocaleString()
                    : "-"}
                </p>
              </div>
              {insight.changeset_ref && (
                <div>
                  <p className="text-xs text-muted-foreground">
                    Changeset Ref
                  </p>
                  <p className="font-mono text-xs">{insight.changeset_ref}</p>
                </div>
              )}
            </div>
          )}

          {/* Action buttons */}
          <div className="space-y-3 border-t pt-3">
            <div>
              <label className="mb-1 block text-xs text-muted-foreground">
                Review Notes
              </label>
              <textarea
                value={reviewNotes}
                onChange={(e) => setReviewNotes(e.target.value)}
                placeholder="Optional review notes..."
                rows={3}
                className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
              />
            </div>
            <div className="flex gap-2">
              <button
                onClick={() => handleAction("approved")}
                disabled={updateStatus.isPending}
                className="rounded-md bg-green-600 px-4 py-2 text-sm font-medium text-white hover:bg-green-700 disabled:opacity-50"
              >
                Approve
              </button>
              <button
                onClick={() => handleAction("rejected")}
                disabled={updateStatus.isPending}
                className="rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-50"
              >
                Reject
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
