import { useMemo, useState } from "react";
import { Pencil, Trash2, History } from "lucide-react";
import { useKnowledgePage, useResolveRefs, useDeleteKnowledgePage } from "@/api/portal/hooks";
import type { KnowledgePage } from "@/api/portal/types";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { PageHeader } from "@/components/patterns/PageHeader";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { extractRefUrns } from "@/lib/entityRefs";
import { markdownToPlainText } from "@/lib/markdownText";
import { RelatedPanel } from "@/components/knowledge/RelatedPanel";
import { LineagePanel } from "@/components/knowledge/LineagePanel";
import { RefPicker } from "@/components/knowledge/RefPicker";
import { KnowledgeBacklinks } from "@/components/knowledge/KnowledgeBacklinks";
import { FeedbackButton } from "@/components/feedback/FeedbackButton";
import { KnowledgePageHistory } from "./KnowledgePageHistory";

export function KnowledgePageDetail({
  id,
  canEdit,
  onNavigate,
  onBack,
  onEdit,
  onDeleted,
}: {
  id: string;
  canEdit: boolean;
  onNavigate?: (path: string) => void;
  onBack: () => void;
  onEdit: () => void;
  onDeleted: () => void;
}) {
  const { data: page, isLoading, isError } = useKnowledgePage(id);
  const del = useDeleteKnowledgePage();
  const [showHistory, setShowHistory] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);
  // Resolve the body's inline entity references to display names for the chips (#664).
  const refUrns = useMemo(() => extractRefUrns(page?.body ?? ""), [page?.body]);
  const { data: resolvedRefs } = useResolveRefs(refUrns);

  if (isLoading) return <p className="text-sm text-muted-foreground">Loading...</p>;
  if (isError || !page) {
    return (
      <Alert variant="destructive">
        <AlertDescription>Knowledge page not found.</AlertDescription>
      </Alert>
    );
  }

  return (
    <div className="space-y-4">
      <PageHeader
        backLabel="Back"
        onBack={onBack}
        title={page.title}
        subtitle={`v${page.current_version}${page.updated_by ? ` · last edited by ${page.updated_by}` : ""}`}
        actions={
          <DetailActions
            id={id}
            canEdit={canEdit}
            removing={del.isPending}
            onToggleHistory={() => setShowHistory((v) => !v)}
            onEdit={onEdit}
            onRemove={() => setConfirmRemove(true)}
          />
        }
      />

      <PageIntro page={page} />

      {showHistory && <KnowledgePageHistory id={id} onClose={() => setShowHistory(false)} />}

      {/* The article is the card the body is read in, so the renderer is `bare`:
          its own default box inside this one drew a second border and doubled
          the padding. */}
      <article className="rounded-xl border bg-card p-6 shadow-sm" data-feedback-anchorable>
        <MarkdownRenderer content={page.body} refs={resolvedRefs} onNavigate={onNavigate} bare />
      </article>

      <RelatedPanel pageId={id} onNavigate={onNavigate} />
      <KnowledgeBacklinks urn={`mcp:knowledge_page:${id}`} onNavigate={onNavigate} />
      {canEdit && <LineagePanel pageId={id} />}
      {canEdit && <RefPicker pageId={id} onNavigate={onNavigate} />}

      <ConfirmDialog
        open={confirmRemove}
        onOpenChange={setConfirmRemove}
        title={`Remove "${page.title}"?`}
        description="It will no longer appear in search."
        confirmLabel="Remove"
        destructive
        loading={del.isPending}
        // A failed remove keeps the dialog open, so it has to say why: without
        // this the button simply stops doing anything.
        error={del.isError ? removeError(del.error) : undefined}
        onConfirm={() => del.mutate(id, { onSuccess: onDeleted })}
      />
    </div>
  );
}

/** removeError is what to tell the reader when the remove itself failed. */
function removeError(err: unknown): string {
  return err instanceof Error ? err.message : "Remove failed.";
}

/**
 * DetailActions is the page's own verbs. Feedback is open to any authenticated
 * user; apply_knowledge holders (canEdit) also moderate it and own the page.
 */
function DetailActions({
  id,
  canEdit,
  removing,
  onToggleHistory,
  onEdit,
  onRemove,
}: {
  id: string;
  canEdit: boolean;
  removing: boolean;
  onToggleHistory: () => void;
  onEdit: () => void;
  onRemove: () => void;
}) {
  return (
    <>
      <FeedbackButton target={{ type: "knowledge_page", id }} canModerate={canEdit} />
      {canEdit && (
        <>
          <Button variant="outline" size="sm" onClick={onToggleHistory}>
            <History /> History
          </Button>
          <Button variant="outline" size="sm" onClick={onEdit}>
            <Pencil /> Edit
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="text-destructive hover:bg-destructive/10 hover:text-destructive"
            onClick={onRemove}
            disabled={removing}
          >
            <Trash2 /> Remove
          </Button>
        </>
      )}
    </>
  );
}

/** PageIntro is what the page says it is about, and what it files under. */
function PageIntro({ page }: { page: KnowledgePage }) {
  const tags = page.tags ?? [];
  return (
    <div className="space-y-2">
      {page.summary && (
        <p className="max-w-3xl text-muted-foreground">{markdownToPlainText(page.summary)}</p>
      )}
      {tags.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {tags.map((tag) => (
            <Badge key={tag} variant="muted">
              {tag}
            </Badge>
          ))}
        </div>
      )}
    </div>
  );
}
