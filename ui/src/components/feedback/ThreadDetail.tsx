import { useState } from "react";
import { ArrowLeft, Trash2, Quote } from "lucide-react";
import {
  useThread,
  useThreadEvents,
  useAppendThreadEvent,
  useUpdateThread,
  useDeleteThread,
} from "@/api/portal/hooks";
import type { ThreadEvent, ThreadStatus } from "@/api/portal/types";
import { useAuthStore } from "@/stores/auth";
import {
  KIND_CHIP,
  KIND_LABEL,
  STATUS_CHIP,
  STATUS_LABEL,
  MODERATION_STATUSES,
  formatRelative,
} from "./meta";

interface Props {
  threadId: string;
  canModerate: boolean;
  onBack: () => void;
  onDeleted: () => void;
}

// eventSummary renders a one-line label for non-comment timeline events.
function eventSummary(e: ThreadEvent): string | null {
  switch (e.event_type) {
    case "status_change":
    case "resolution": {
      const next = (e.metadata?.["new_status"] as string) ?? "";
      return next ? `changed status to ${STATUS_LABEL[next as ThreadStatus] ?? next}` : "changed status";
    }
    case "rating":
      return e.rating != null ? `rated ${e.rating}/5` : "left a rating";
    case "approval":
      return "approved";
    case "rejection":
      return "rejected";
    case "validation_request":
      return "requested validation";
    case "validation_result":
      return "recorded a validation result";
    case "insight_linked":
      return "linked an insight";
    case "changeset_linked":
      return "linked a changeset";
    default:
      return null;
  }
}

export function ThreadDetail({ threadId, canModerate, onBack, onDeleted }: Props) {
  const { data: thread } = useThread(threadId);
  const { data: events, isLoading } = useThreadEvents(threadId);
  const append = useAppendThreadEvent();
  const update = useUpdateThread();
  const del = useDeleteThread();
  const me = useAuthStore((s) => s.user);
  const [reply, setReply] = useState("");

  const isAuthor = !!me?.email && thread?.author_email === me.email;
  const mayModerate = canModerate || !!me?.is_admin || isAuthor;

  const postReply = (e: React.FormEvent) => {
    e.preventDefault();
    if (!reply.trim()) return;
    append.mutate(
      { threadId, event_type: "comment", body: reply.trim() },
      { onSuccess: () => setReply("") },
    );
  };

  const changeStatus = (status: ThreadStatus) => update.mutate({ id: threadId, status });

  if (!thread) {
    return <div className="p-4 text-sm text-muted-foreground">Loading…</div>;
  }

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="flex items-center gap-2 border-b p-3">
        <button onClick={onBack} className="rounded p-1 hover:bg-accent" aria-label="Back to list">
          <ArrowLeft className="h-4 w-4" />
        </button>
        <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${KIND_CHIP[thread.kind]}`}>
          {KIND_LABEL[thread.kind]}
        </span>
        <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_CHIP[thread.status]}`}>
          {STATUS_LABEL[thread.status]}
        </span>
        {mayModerate && (
          <button
            onClick={() => del.mutate(threadId, { onSuccess: onDeleted })}
            disabled={del.isPending}
            className="ml-auto rounded p-1 text-destructive hover:bg-destructive/10 disabled:opacity-50"
            aria-label="Delete thread"
            title="Delete"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
      {del.isError && (
        <p className="border-b px-3 py-1.5 text-xs text-destructive">Failed to delete this thread.</p>
      )}

      {/* Title + meta */}
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

      {/* Timeline */}
      <div className="flex-1 space-y-3 overflow-auto p-3">
        {isLoading && <p className="text-xs text-muted-foreground">Loading timeline…</p>}
        {(events ?? []).map((e) => {
          const summary = eventSummary(e);
          return (
            <div key={e.id} className="text-sm">
              <p className="text-xs text-muted-foreground">
                <span className="font-medium text-foreground">{e.author_email}</span>{" "}
                {summary ?? "commented"} · {formatRelative(e.created_at)}
              </p>
              {e.body && <p className="mt-0.5 whitespace-pre-wrap">{e.body}</p>}
            </div>
          );
        })}
      </div>

      {/* Moderation controls */}
      {mayModerate && (
        <div className="flex items-center gap-2 border-t p-3">
          <span className="text-xs font-medium text-muted-foreground">Set status</span>
          <select
            value={thread.status}
            onChange={(e) => changeStatus(e.target.value as ThreadStatus)}
            disabled={update.isPending}
            className="rounded-md border bg-background px-2 py-1 text-xs"
          >
            {MODERATION_STATUSES.map((s) => (
              <option key={s} value={s}>
                {STATUS_LABEL[s]}
              </option>
            ))}
          </select>
        </div>
      )}

      {/* Reply box */}
      <form onSubmit={postReply} className="border-t p-3">
        <textarea
          value={reply}
          onChange={(e) => setReply(e.target.value)}
          rows={2}
          placeholder="Reply…"
          className="w-full resize-y rounded-md border bg-background px-2 py-1.5 text-sm"
        />
        {append.isError && (
          <p className="mt-1 text-xs text-destructive">Failed to post reply.</p>
        )}
        <div className="mt-2 flex justify-end">
          <button
            type="submit"
            disabled={!reply.trim() || append.isPending}
            className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          >
            {append.isPending ? "Posting…" : "Reply"}
          </button>
        </div>
      </form>
    </div>
  );
}
