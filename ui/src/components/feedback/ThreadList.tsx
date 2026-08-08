import { Quote, MessageCircle, AlertCircle } from "lucide-react";
import type { ThreadWithMeta } from "@/api/portal/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { ThreadKindBadge, ThreadStatusBadge } from "./ThreadBadges";
import { formatRelative } from "./meta";

interface Props {
  threads: ThreadWithMeta[];
  isLoading: boolean;
  onSelect: (threadId: string) => void;
}

export function ThreadList({ threads, isLoading, onSelect }: Props) {
  if (isLoading) {
    return <p className="p-4 text-sm text-muted-foreground">Loading feedback…</p>;
  }
  if (threads.length === 0) {
    return (
      <EmptyState icon={MessageCircle} className="m-3">
        No feedback yet.
      </EmptyState>
    );
  }
  return (
    <ul className="divide-y">
      {threads.map((t) => {
        const replies = Math.max(0, t.event_count - 1);
        return (
          <li key={t.id}>
            <button
              type="button"
              onClick={() => onSelect(t.id)}
              className="flex w-full flex-col items-start gap-1 px-3 py-2.5 text-left hover:bg-accent/50"
            >
              <div className="flex w-full items-center gap-1.5">
                <ThreadKindBadge kind={t.kind} />
                <ThreadStatusBadge status={t.status} />
                {t.requires_resolution && t.status !== "resolved" && t.status !== "wont_fix" && (
                  <AlertCircle className="h-3.5 w-3.5 text-amber-500" aria-label="Needs resolution" />
                )}
                <span className="ml-auto text-[11px] text-muted-foreground">
                  {formatRelative(t.last_event_at)}
                </span>
              </div>
              {t.title && <span className="text-sm font-medium">{t.title}</span>}
              {t.anchor?.type === "text_quote" && (
                <span className="flex items-center gap-1 text-xs italic text-muted-foreground">
                  <Quote className="h-3 w-3 shrink-0" />
                  <span className="min-w-0 truncate">{t.anchor.exact}</span>
                </span>
              )}
              <span className="text-xs text-muted-foreground">
                {t.author_email}
                {replies > 0 ? ` · ${replies} ${replies === 1 ? "reply" : "replies"}` : ""}
              </span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}
