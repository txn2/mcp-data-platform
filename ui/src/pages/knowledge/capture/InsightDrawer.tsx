import { useState, useCallback, useId } from "react";
import { useUpdateInsightStatus } from "@/api/admin/hooks";
import type { Insight } from "@/api/admin/types";
import { KnowledgeStatusBadge } from "@/components/knowledge/KnowledgeStatusBadge";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { DrawerShell } from "@/components/patterns/DrawerShell";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { formatUser } from "@/lib/formatUser";
import { LabeledBlock, MetaField, MetaGrid } from "./fields";
import { confidenceVariant, formatCategory } from "./helpers";
import { InsightLifecycle, InsightTables } from "./insightSections";

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
    <DrawerShell
      title="Insight Detail"
      onClose={onClose}
      footer={
        <ReviewActions
          notes={reviewNotes}
          onNotes={setReviewNotes}
          pending={updateStatus.isPending}
          onAction={handleAction}
        />
      }
    >
      <MetaGrid>
        <MetaField label="ID" mono>
          {insight.id}
        </MetaField>
        <MetaField label="Created At">
          {new Date(insight.created_at).toLocaleString()}
        </MetaField>
        <MetaField label="Captured By" title={insight.captured_by}>
          {formatUser(insight.captured_by, userLabels[insight.captured_by])}
        </MetaField>
        <MetaField label="Persona">{insight.persona}</MetaField>
        <MetaField label="Category">{formatCategory(insight.category)}</MetaField>
        <MetaField label="Confidence">
          <StatusBadge variant={confidenceVariant(insight.confidence)}>
            {insight.confidence}
          </StatusBadge>
        </MetaField>
        <MetaField label="Status">
          <KnowledgeStatusBadge status={insight.status} />
        </MetaField>
        <MetaField label="Session ID" mono>
          {insight.session_id}
        </MetaField>
      </MetaGrid>

      <LabeledBlock label="Insight">
        <div className="rounded bg-muted p-3">
          <MarkdownRenderer content={insight.insight_text} bare />
        </div>
      </LabeledBlock>

      {insight.entity_urns.length > 0 && (
        <LabeledBlock label="Entity URNs">
          <div className="space-y-1">
            {insight.entity_urns.map((urn, i) => (
              <p key={i} className="break-all font-mono text-xs text-muted-foreground">
                {urn}
              </p>
            ))}
          </div>
        </LabeledBlock>
      )}

      <InsightTables insight={insight} />
      <InsightLifecycle insight={insight} userLabels={userLabels} />
    </DrawerShell>
  );
}

// ReviewActions is the decision the drawer exists for: optional notes, then
// approve or reject. It is pinned below the detail so a reviewer never scrolls
// a long insight to find the two buttons.
function ReviewActions({
  notes,
  onNotes,
  pending,
  onAction,
}: {
  notes: string;
  onNotes: (v: string) => void;
  pending: boolean;
  onAction: (status: string) => void;
}) {
  const notesId = useId();
  return (
    <div className="space-y-3">
      <div className="space-y-1.5">
        <Label htmlFor={notesId} className="text-xs text-muted-foreground">
          Review Notes
        </Label>
        <Textarea
          id={notesId}
          value={notes}
          onChange={(e) => onNotes(e.target.value)}
          placeholder="Optional review notes..."
          rows={3}
          className="field-sizing-fixed"
        />
      </div>
      <div className="flex gap-2">
        <Button onClick={() => onAction("approved")} disabled={pending}>
          Approve
        </Button>
        <Button
          variant="destructive"
          onClick={() => onAction("rejected")}
          disabled={pending}
        >
          Reject
        </Button>
      </div>
    </div>
  );
}
