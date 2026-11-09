import { useId } from "react";
import { ArrowLeft, Quote, Trash2 } from "lucide-react";
import type { Thread } from "@/api/portal/types";
import { Button } from "@/components/ui/button";
import { ThreadKindBadge, ThreadStatusBadge, ThreadVisibilityBadge, VisibilityChoice } from "../ThreadBadges";
import { useUpdateThread } from "@/api/portal/hooks";
import { usePortalAccess } from "@/hooks/usePortalAccess";
import { formatRelative } from "../meta";

// ThreadHeader is the way back out, what the thread is, and, for a moderator,
// the way to remove it. The Internal / Shared choice has a row of its own so the
// header fits the 448px drawer and a phone (#2580).
export function ThreadHeader({
  thread,
  mayModerate,
  deleting,
  onBack,
  onDelete,
  statusControl,
}: {
  thread: Thread;
  mayModerate: boolean;
  deleting: boolean;
  onBack: () => void;
  onDelete: () => void;
  /** statusControl replaces the status badge for a moderator (#2532). */
  statusControl?: React.ReactNode;
}) {
  // Whoever moderates the thread changes who sees it; an external person
  // cannot (#2227). Anyone offered the choice reads the value from it.
  const update = useUpdateThread();
  const { external } = usePortalAccess();
  const labelId = useId();
  const offersVisibility = mayModerate && !external;
  return (
    <div className="border-b p-3">
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="ghost" size="icon-sm" onClick={onBack} aria-label="Back to list">
          <ArrowLeft />
        </Button>
        <ThreadKindBadge kind={thread.kind} />
        {statusControl ?? <ThreadStatusBadge status={thread.status} />}
        {!offersVisibility && <ThreadVisibilityBadge visibility={thread.visibility} />}
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
      {offersVisibility && (
        <div className="mt-2 flex items-center gap-3 pl-10">
          <span id={labelId} className="text-xs text-muted-foreground">
            Visibility
          </span>
          <VisibilityChoice
            labelledBy={labelId}
            value={thread.visibility ?? "internal"}
            disabled={update.isPending}
            onChange={(visibility) => update.mutate({ id: thread.id, visibility })}
          />
        </div>
      )}
    </div>
  );
}

// ThreadSubject is what the thread is about: who opened it and when, and the
// passage it was anchored to if it was raised against one. A thread opened
// before #2513 keeps its stored title as a heading; a newer one has none and
// is read from its first message, the first entry of the timeline below.
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
