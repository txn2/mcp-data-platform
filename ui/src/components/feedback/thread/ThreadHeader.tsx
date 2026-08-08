import { ArrowLeft, Quote, Trash2 } from "lucide-react";
import type { Thread } from "@/api/portal/types";
import { Button } from "@/components/ui/button";
import { ThreadKindBadge, ThreadStatusBadge } from "../ThreadBadges";
import { formatRelative } from "../meta";

// ThreadHeader is the way back out, what the thread is, and — for a moderator —
// the way to remove it.
export function ThreadHeader({
  thread,
  mayModerate,
  deleting,
  onBack,
  onDelete,
}: {
  thread: Thread;
  mayModerate: boolean;
  deleting: boolean;
  onBack: () => void;
  onDelete: () => void;
}) {
  return (
    <div className="flex items-center gap-2 border-b p-3">
      <Button variant="ghost" size="icon-sm" onClick={onBack} aria-label="Back to list">
        <ArrowLeft />
      </Button>
      <ThreadKindBadge kind={thread.kind} />
      <ThreadStatusBadge status={thread.status} />
      {mayModerate && (
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={onDelete}
          disabled={deleting}
          className="ml-auto text-destructive hover:bg-destructive/10 hover:text-destructive"
          aria-label="Delete thread"
          title="Delete"
        >
          <Trash2 />
        </Button>
      )}
    </div>
  );
}

// ThreadSubject is what the thread is about: its title, who opened it and when,
// and the passage it was anchored to if it was raised against one.
export function ThreadSubject({ thread }: { thread: Thread }) {
  return (
    <div className="border-b p-3">
      {thread.title && <h3 className="text-sm font-semibold">{thread.title}</h3>}
      <p className="text-xs text-muted-foreground">
        {thread.author_email} · {formatRelative(thread.created_at)}
        {thread.target_version ? ` · on v${thread.target_version}` : ""}
        {thread.requires_resolution ? " · needs resolution" : ""}
      </p>
      {thread.anchor?.type === "text_quote" && (
        <p className="mt-1 flex items-start gap-1 rounded border-l-2 border-primary/40 bg-muted/40 px-2 py-1 text-xs italic text-muted-foreground">
          <Quote className="mt-0.5 h-3 w-3 shrink-0" />
          <span className="min-w-0 truncate">{thread.anchor.exact}</span>
        </p>
      )}
    </div>
  );
}
