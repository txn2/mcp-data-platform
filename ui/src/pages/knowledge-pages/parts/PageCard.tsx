import { MessageSquare } from "lucide-react";
import type { KnowledgePage } from "@/api/portal/types";
import { Badge } from "@/components/ui/badge";
import { BuiltinBadge } from "@/components/knowledge/BuiltinBadge";
import { markdownToPlainText } from "@/lib/markdownText";

/**
 * PageCard is one knowledge page in the card list: its title, what it is about,
 * the tags it files under, and when it last changed. The whole tile is the
 * target, so it is a button carrying the shared card face rather than a card
 * holding a link.
 */
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
      className="flex h-full w-full flex-col rounded-xl border bg-card p-4 text-left shadow-sm transition-colors hover:border-primary/50 hover:bg-muted/50"
    >
      <span className="flex items-start justify-between gap-2">
        <span className="font-medium">{page.title}</span>
        {page.builtin && <BuiltinBadge />}
        {openThreads > 0 && (
          <Badge
            variant="muted"
            title={`${openThreads} open feedback ${openThreads === 1 ? "thread" : "threads"}`}
          >
            <MessageSquare />
            {openThreads}
          </Badge>
        )}
      </span>
      {page.summary && (
        <span className="mt-1 line-clamp-3 text-sm text-muted-foreground">
          {markdownToPlainText(page.summary)}
        </span>
      )}
      {page.tags.length > 0 && (
        <span className="mt-3 flex flex-wrap gap-1">
          {page.tags.map((t) => (
            <Badge key={t} variant="muted">
              {t}
            </Badge>
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
