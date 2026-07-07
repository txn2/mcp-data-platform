import { MessageSquare } from "lucide-react";
import type { KnowledgePage } from "@/api/portal/types";

export function PageCard({
  page,
  openThreads,
  onOpen,
}: {
  page: KnowledgePage;
  openThreads: number;
  onOpen: (id: string) => void;
}) {
  return (
    <button
      onClick={() => onOpen(page.id)}
      className="flex h-full w-full flex-col rounded-lg border border-border bg-card p-4 text-left transition hover:border-primary/50 hover:shadow-sm"
    >
      <span className="flex items-start justify-between gap-2">
        <span className="font-medium text-foreground">{page.title}</span>
        {openThreads > 0 && (
          <span
            className="inline-flex shrink-0 items-center gap-1 rounded-full bg-muted px-1.5 py-0.5 text-[11px] font-medium text-muted-foreground"
            title={`${openThreads} open feedback ${openThreads === 1 ? "thread" : "threads"}`}
          >
            <MessageSquare className="h-3 w-3" />
            {openThreads}
          </span>
        )}
      </span>
      {page.summary && (
        <span className="mt-1 line-clamp-3 text-sm text-muted-foreground">{page.summary}</span>
      )}
      {page.tags.length > 0 && (
        <span className="mt-3 flex flex-wrap gap-1">
          {page.tags.map((t) => (
            <span key={t} className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">
              {t}
            </span>
          ))}
        </span>
      )}
      <span className="mt-auto pt-3 text-[11px] text-muted-foreground">
        Updated {new Date(page.updated_at).toLocaleDateString()}
        {page.updated_by ? ` by ${page.updated_by}` : ""}
      </span>
    </button>
  );
}
