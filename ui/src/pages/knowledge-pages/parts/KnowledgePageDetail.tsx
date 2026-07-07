import { useMemo, useState } from "react";
import { Pencil, Trash2, ArrowLeft, History } from "lucide-react";
import { useKnowledgePage, useResolveRefs, useDeleteKnowledgePage } from "@/api/portal/hooks";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { extractRefUrns } from "@/lib/entityRefs";
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
  // Resolve the body's inline entity references to display names for the chips (#664).
  const refUrns = useMemo(() => extractRefUrns(page?.body ?? ""), [page?.body]);
  const { data: resolvedRefs } = useResolveRefs(refUrns);

  if (isLoading) return <p className="text-sm text-muted-foreground">Loading...</p>;
  if (isError || !page) return <p className="text-sm text-destructive">Knowledge page not found.</p>;

  const handleDelete = () => {
    if (!window.confirm(`Remove "${page.title}"? It will no longer appear in search.`)) return;
    del.mutate(id, { onSuccess: onDeleted });
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-4">
        <button onClick={onBack} className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground">
          <ArrowLeft className="h-4 w-4" /> Back
        </button>
        <div className="flex items-center gap-2">
          {/* Feedback is open to any authenticated user; apply_knowledge holders
              (canEdit) also moderate. */}
          <FeedbackButton target={{ type: "knowledge_page", id }} canModerate={canEdit} />
          {canEdit && (
            <>
              <button
                onClick={() => setShowHistory((v) => !v)}
                className="inline-flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-sm hover:bg-muted"
              >
                <History className="h-4 w-4" /> History
              </button>
              <button
                onClick={onEdit}
                className="inline-flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-sm hover:bg-muted"
              >
                <Pencil className="h-4 w-4" /> Edit
              </button>
              <button
                onClick={handleDelete}
                disabled={del.isPending}
                className="inline-flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-sm text-destructive hover:bg-destructive/10 disabled:opacity-50"
              >
                <Trash2 className="h-4 w-4" /> Remove
              </button>
            </>
          )}
        </div>
      </div>

      <div>
        <h1 className="text-2xl font-semibold text-foreground">{page.title}</h1>
        {page.summary && <p className="mt-1 text-muted-foreground">{page.summary}</p>}
        <p className="mt-2 text-xs text-muted-foreground">
          v{page.current_version}
          {page.updated_by ? ` · last edited by ${page.updated_by}` : ""}
        </p>
        {page.tags && page.tags.length > 0 && (
          <div className="mt-2 flex flex-wrap gap-1.5">
            {page.tags.map((tag) => (
              <span
                key={tag}
                className="inline-flex items-center rounded-full border border-border bg-muted px-2 py-0.5 text-xs text-muted-foreground"
              >
                {tag}
              </span>
            ))}
          </div>
        )}
      </div>

      {showHistory && <KnowledgePageHistory id={id} onClose={() => setShowHistory(false)} />}

      <article
        className="prose prose-sm max-w-none rounded-lg border border-border bg-card p-6 dark:prose-invert"
        data-feedback-anchorable
      >
        <MarkdownRenderer content={page.body} refs={resolvedRefs} onNavigate={onNavigate} />
      </article>

      <RelatedPanel pageId={id} onNavigate={onNavigate} />
      <KnowledgeBacklinks urn={`mcp:knowledge_page:${id}`} onNavigate={onNavigate} />
      {canEdit && <LineagePanel pageId={id} />}
      {canEdit && <RefPicker pageId={id} onNavigate={onNavigate} />}
    </div>
  );
}
