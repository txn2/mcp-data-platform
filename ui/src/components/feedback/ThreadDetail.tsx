import { useCallback, useEffect, useRef, useState } from "react";
import {
  useThread,
  useThreadEvents,
  useUndoStatusChange,
  useUpdateThread,
  useDeleteThread,
} from "@/api/portal/hooks";
import type { Thread, ThreadEvent, ThreadStatus } from "@/api/portal/types";
import { useAuthStore } from "@/stores/auth";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { ShareToast, useShareToast } from "@/components/share/ShareToast";
import { targetOfThread } from "./targetFilter";
import { STATUS_LABEL } from "./meta";
import { CaptureInsightPanel } from "./thread/CaptureInsightPanel";
import { KnowledgeChainPanel } from "./thread/KnowledgeChainPanel";
import { ReplyComposer } from "./thread/ReplyComposer";
import { StatusMenu, undoableStatusEvent } from "./thread/StatusMenu";
import { ThreadHeader, ThreadSubject } from "./thread/ThreadHeader";
import { ThreadTimeline } from "./thread/ThreadTimeline";
import { ValidationPanel } from "./thread/ValidationPanel";

interface Props {
  threadId: string;
  canModerate: boolean;
  onBack: () => void;
  onDeleted: () => void;
}

type Viewer = ReturnType<typeof useAuthStore.getState>["user"];

function isThreadAuthor(me: Viewer, thread: Thread | undefined): boolean {
  const email = me?.email ?? "";
  return email !== "" && thread?.author_email === email;
}

// Capturing feedback as an insight requires apply_knowledge access (or admin),
// the same capability that reviews and applies it; mirrors the backend gate.
function canApplyKnowledge(me: Viewer): boolean {
  return me?.is_admin === true || (me?.tools ?? []).includes("apply_knowledge");
}

// useThreadPermissions resolves what the reader may do with this thread. The
// backend enforces all of it; these only decide what is offered.
function useThreadPermissions(thread: Thread | undefined, canModerate: boolean) {
  const me = useAuthStore((s) => s.user);
  const isAuthor = isThreadAuthor(me, thread);
  return {
    isAuthor,
    mayModerate: canModerate || me?.is_admin === true || isAuthor,
    canApply: canApplyKnowledge(me),
  };
}

// Capturing is offered only where it can land: an unlinked correction or
// suggestion, to someone who can apply knowledge.
function offersCapture(thread: Thread, canApply: boolean): boolean {
  if (thread.insight_id || !canApply) return false;
  return thread.kind === "correction" || thread.kind === "suggestion";
}

/** useNow ticks every 15 seconds so an Undo goes away when its window ends. */
function useNow(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 15_000);
    return () => clearInterval(t);
  }, []);
  return now;
}

/**
 * useStatusChange changes a thread's status and offers its Undo (#2532): a
 * notice saying what changed with an Undo, and the same Undo on the line the
 * change wrote, while the server would still allow it.
 */
function useStatusChange(threadId: string, events: ThreadEvent[] | undefined) {
  const update = useUpdateThread();
  const undo = useUndoStatusChange();
  const latestEvents = useRef(events);
  latestEvents.current = events;
  const myEmail = useAuthStore((s) => s.user?.email ?? "");
  const toast = useShareToast();
  const now = useNow();
  const undoableId = undoableStatusEvent(events, myEmail, now)?.id ?? null;
  const undoLatest = useCallback(() => {
    // The notice's Undo names no event: it takes back the newest change of
    // the reader's, read from the timeline as it now stands.
    const latest = undoableStatusEvent(latestEvents.current, myEmail, Date.now());
    if (latest) undo.mutate({ threadId, eventId: latest.id });
  }, [threadId, myEmail, undo]);
  const change = (status: ThreadStatus) =>
    update.mutate(
      { id: threadId, status },
      { onSuccess: () => toast.show(`Status set to ${STATUS_LABEL[status]}`, undoLatest) },
    );
  return { update, undo, toast, undoableId, change };
}

export function ThreadDetail({ threadId, canModerate, onBack, onDeleted }: Props) {
  const { data: thread } = useThread(threadId);
  const { data: events, isLoading } = useThreadEvents(threadId);
  const del = useDeleteThread();
  const { isAuthor, mayModerate, canApply } = useThreadPermissions(thread, canModerate);
  const isAdmin = useAuthStore((s) => s.user?.is_admin === true);
  const status = useStatusChange(threadId, events);

  if (!thread) {
    return <div className="p-4 text-sm text-muted-foreground">Loading…</div>;
  }

  return (
    <div className="relative flex h-full flex-col">
      <ThreadHeader
        thread={thread}
        mayModerate={mayModerate}
        deleting={del.isPending}
        onBack={onBack}
        onDelete={() => del.mutate(threadId, { onSuccess: onDeleted })}
        statusControl={
          mayModerate ? (
            <StatusMenu status={thread.status} disabled={status.update.isPending} onChange={status.change} />
          ) : undefined
        }
      />
      {del.isError && (
        <Alert variant="destructive" className="rounded-none border-x-0 border-t-0">
          <AlertDescription>Failed to delete this thread.</AlertDescription>
        </Alert>
      )}
      {status.undo.isError && (
        <Alert variant="destructive" className="rounded-none border-x-0 border-t-0">
          <AlertDescription>{status.undo.error.message}</AlertDescription>
        </Alert>
      )}

      <ThreadSubject thread={thread} />

      {offersCapture(thread, canApply) && <CaptureInsightPanel threadId={threadId} />}

      {thread.insight_id && (
        <KnowledgeChainPanel threadId={threadId} insightId={thread.insight_id} />
      )}

      {thread.validation_state === "pending" && isAuthor && (
        <ValidationPanel threadId={threadId} />
      )}

      <ThreadTimeline
        events={events}
        isLoading={isLoading}
        threadId={threadId}
        target={targetOfThread(thread)}
        canRemoveAny={canModerate || isAdmin}
        undoableId={status.undoableId}
        onUndo={(eventId) => status.undo.mutate({ threadId, eventId })}
        undoing={status.undo.isPending}
      />

      <ReplyComposer threadId={threadId} thread={thread} mayResolve={mayModerate} />
      <ShareToast toast={status.toast.toast} onDismiss={status.toast.dismiss} />
    </div>
  );
}
