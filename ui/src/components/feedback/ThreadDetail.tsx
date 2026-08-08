import { useState } from "react";
import {
  useThread,
  useThreadEvents,
  useAppendThreadEvent,
  useUpdateThread,
  useDeleteThread,
} from "@/api/portal/hooks";
import type { Thread, ThreadStatus } from "@/api/portal/types";
import { useAuthStore } from "@/stores/auth";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { MentionTextarea } from "./MentionTextarea";
import { targetOfThread } from "./targetFilter";
import { STATUS_LABEL, MODERATION_STATUSES } from "./meta";
import { CaptureInsightPanel } from "./thread/CaptureInsightPanel";
import { KnowledgeChainPanel } from "./thread/KnowledgeChainPanel";
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

export function ThreadDetail({ threadId, canModerate, onBack, onDeleted }: Props) {
  const { data: thread } = useThread(threadId);
  const { data: events, isLoading } = useThreadEvents(threadId);
  const update = useUpdateThread();
  const del = useDeleteThread();
  const { isAuthor, mayModerate, canApply } = useThreadPermissions(thread, canModerate);

  if (!thread) {
    return <div className="p-4 text-sm text-muted-foreground">Loading…</div>;
  }

  return (
    <div className="flex h-full flex-col">
      <ThreadHeader
        thread={thread}
        mayModerate={mayModerate}
        deleting={del.isPending}
        onBack={onBack}
        onDelete={() => del.mutate(threadId, { onSuccess: onDeleted })}
      />
      {del.isError && (
        <Alert variant="destructive" className="rounded-none border-x-0 border-t-0">
          <AlertDescription>Failed to delete this thread.</AlertDescription>
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

      <ThreadTimeline events={events} isLoading={isLoading} />

      {mayModerate && (
        <ModerationBar
          status={thread.status}
          disabled={update.isPending}
          onChange={(status) => update.mutate({ id: threadId, status })}
        />
      )}

      <ReplyForm threadId={threadId} thread={thread} />
    </div>
  );
}

function ModerationBar({
  status,
  disabled,
  onChange,
}: {
  status: ThreadStatus;
  disabled: boolean;
  onChange: (status: ThreadStatus) => void;
}) {
  return (
    <div className="flex items-center gap-2 border-t p-3">
      <span className="text-xs font-medium text-muted-foreground">Set status</span>
      <Select
        value={status}
        onValueChange={(v) => onChange(v as ThreadStatus)}
        disabled={disabled}
      >
        <SelectTrigger size="sm" aria-label="Set status" className="text-xs">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {MODERATION_STATUSES.map((s) => (
            <SelectItem key={s} value={s}>
              {STATUS_LABEL[s]}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

function ReplyForm({ threadId, thread }: { threadId: string; thread: Thread }) {
  const append = useAppendThreadEvent();
  const [reply, setReply] = useState("");

  const postReply = (e: React.FormEvent) => {
    e.preventDefault();
    if (!reply.trim()) return;
    append.mutate(
      { threadId, event_type: "comment", body: reply.trim() },
      { onSuccess: () => setReply("") },
    );
  };

  return (
    <form onSubmit={postReply} className="border-t p-3">
      <MentionTextarea
        target={targetOfThread(thread)}
        value={reply}
        onChange={setReply}
        rows={2}
        placeholder="Reply… type @ to mention someone"
        aria-label="Reply"
        className="resize-y"
      />
      {append.isError && (
        <p className="mt-1 text-xs text-destructive">Failed to post reply.</p>
      )}
      <div className="mt-2 flex justify-end">
        <Button type="submit" size="sm" disabled={!reply.trim() || append.isPending}>
          {append.isPending ? "Posting…" : "Reply"}
        </Button>
      </div>
    </form>
  );
}
