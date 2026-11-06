import { useState } from "react";
import type { FeedbackTarget, ThreadEvent, ThreadStatus } from "@/api/portal/types";
import { useDeleteThreadEvent, useEditThreadEvent } from "@/api/portal/hooks";
import { useAuthStore } from "@/stores/auth";
import { Button } from "@/components/ui/button";
import { CommentBody } from "../CommentBody";
import { MentionTextarea } from "../MentionTextarea";
import { STATUS_LABEL, formatRelative } from "../meta";

// What each non-comment timeline event says it did. Statuses and ratings carry
// a value, so they are derived below; everything else is a fixed phrase.
const EVENT_SUMMARY: Record<string, string> = {
  approval: "approved",
  rejection: "rejected",
  validation_request: "requested validation",
  validation_result: "recorded a validation result",
  insight_linked: "linked an insight",
  changeset_linked: "linked a changeset",
};

// eventSummary renders a one-line label for non-comment timeline events.
function eventSummary(e: ThreadEvent): string | null {
  if (e.event_type === "status_change" || e.event_type === "resolution") {
    const next = (e.metadata?.["new_status"] as string) ?? "";
    if (!next) return "changed status";
    return `changed status to ${STATUS_LABEL[next as ThreadStatus] ?? next}`;
  }
  if (e.event_type === "rating") {
    return e.rating == null ? "left a rating" : `rated ${e.rating}/5`;
  }
  return EVENT_SUMMARY[e.event_type] ?? null;
}

// recordedMentions returns the addresses the server recorded as delivered
// mentions for an event. Anything else in the body was not delivered and must
// not render as a chip.
function recordedMentions(e: ThreadEvent): string[] {
  const raw = e.metadata?.["mentions"];
  return Array.isArray(raw) ? raw.filter((m): m is string => typeof m === "string") : [];
}

// Only a comment is a message a person can change (#2514): every other event
// records a decision or a link, and the server refuses to change one.
function isChangeable(e: ThreadEvent): boolean {
  return e.event_type === "comment" && !e.deleted_at;
}

// ThreadTimeline is the thread's record: who did what, when, and the comment
// bodies with their delivered mentions resolved to names. A message's author
// can edit and delete it; canRemoveAny (the target's owner or editor, or an
// administrator) can delete any message. The server decides both; these only
// decide what is offered.
export function ThreadTimeline({
  events,
  isLoading,
  threadId,
  target,
  canRemoveAny = false,
}: {
  events: ThreadEvent[] | undefined;
  isLoading: boolean;
  threadId?: string;
  target?: FeedbackTarget;
  canRemoveAny?: boolean;
}) {
  const me = useAuthStore((s) => s.user);
  const myEmail = (me?.email ?? "").toLowerCase();
  return (
    <div className="flex-1 space-y-3 overflow-auto p-3">
      {isLoading && <p className="text-xs text-muted-foreground">Loading timeline…</p>}
      {(events ?? []).map((e) => {
        const mine = myEmail !== "" && e.author_email.toLowerCase() === myEmail;
        return (
          <TimelineEvent
            key={e.id}
            event={e}
            threadId={threadId}
            target={target}
            canEdit={Boolean(threadId) && mine && isChangeable(e)}
            canDelete={Boolean(threadId) && (mine || canRemoveAny) && isChangeable(e)}
          />
        );
      })}
    </div>
  );
}

function TimelineEvent({
  event: e,
  threadId,
  target,
  canEdit,
  canDelete,
}: {
  event: ThreadEvent;
  threadId?: string;
  target?: FeedbackTarget;
  canEdit: boolean;
  canDelete: boolean;
}) {
  const [editing, setEditing] = useState(false);
  return (
    <div className="text-sm">
      <EventHeader event={e} />
      {editing && threadId ? (
        <EditMessage
          threadId={threadId}
          event={e}
          target={target}
          onDone={() => setEditing(false)}
        />
      ) : (
        <EventBody event={e} />
      )}
      {!editing && threadId && (canEdit || canDelete) && (
        <MessageActions
          threadId={threadId}
          eventId={e.id}
          canEdit={canEdit}
          canDelete={canDelete}
          onEdit={() => setEditing(true)}
        />
      )}
    </div>
  );
}

// EventHeader says who did what and when, and that a message was edited.
function EventHeader({ event: e }: { event: ThreadEvent }) {
  return (
    <p className="text-xs text-muted-foreground">
      <span className="font-medium text-foreground">{e.author_email}</span>{" "}
      {eventSummary(e) ?? "commented"} · {formatRelative(e.created_at)}
      {e.edited_at && !e.deleted_at && (
        <span title={new Date(e.edited_at).toLocaleString()}>
          {" "}
          · edited {formatRelative(e.edited_at)}
        </span>
      )}
    </p>
  );
}

// EventBody is the message's text, or its place when it was deleted.
function EventBody({ event: e }: { event: ThreadEvent }) {
  if (e.deleted_at) {
    return <p className="mt-0.5 text-xs italic text-muted-foreground">Message deleted</p>;
  }
  if (!e.body) return null;
  return <CommentBody body={e.body} mentions={recordedMentions(e)} className="mt-0.5" />;
}

function EditMessage({
  threadId,
  event,
  target,
  onDone,
}: {
  threadId: string;
  event: ThreadEvent;
  target?: FeedbackTarget;
  onDone: () => void;
}) {
  const edit = useEditThreadEvent();
  const [text, setText] = useState(event.body ?? "");
  const save = (ev: React.FormEvent) => {
    ev.preventDefault();
    if (!text.trim()) return;
    edit.mutate({ threadId, eventId: event.id, body: text.trim() }, { onSuccess: onDone });
  };
  return (
    <form onSubmit={save} className="mt-1 space-y-1">
      {target ? (
        <MentionTextarea
          target={target}
          value={text}
          onChange={setText}
          rows={3}
          aria-label="Edit message"
          className="resize-y"
        />
      ) : (
        <textarea
          value={text}
          onChange={(ev) => setText(ev.target.value)}
          rows={3}
          aria-label="Edit message"
          className="w-full resize-y rounded-md border bg-background px-2 py-1 text-sm"
        />
      )}
      {edit.isError && <p className="text-xs text-destructive">Failed to save the message.</p>}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" size="sm" disabled={!text.trim() || edit.isPending}>
          {edit.isPending ? "Saving…" : "Save"}
        </Button>
      </div>
    </form>
  );
}

function MessageActions({
  threadId,
  eventId,
  canEdit,
  canDelete,
  onEdit,
}: {
  threadId: string;
  eventId: string;
  canEdit: boolean;
  canDelete: boolean;
  onEdit: () => void;
}) {
  const del = useDeleteThreadEvent();
  const [confirming, setConfirming] = useState(false);
  return (
    <div className="mt-0.5 flex items-center gap-2 text-xs">
      {canEdit && (
        <button
          type="button"
          className="text-muted-foreground hover:text-foreground"
          onClick={onEdit}
        >
          Edit
        </button>
      )}
      {canDelete && !confirming && (
        <button
          type="button"
          className="text-muted-foreground hover:text-destructive"
          onClick={() => setConfirming(true)}
        >
          Delete
        </button>
      )}
      {confirming && (
        <>
          <span className="text-muted-foreground">Delete this message?</span>
          <button
            type="button"
            className="text-destructive"
            disabled={del.isPending}
            onClick={() => del.mutate({ threadId, eventId })}
          >
            Delete
          </button>
          <button
            type="button"
            className="text-muted-foreground"
            onClick={() => setConfirming(false)}
          >
            Cancel
          </button>
        </>
      )}
      {del.isError && <span className="text-destructive">Failed to delete the message.</span>}
    </div>
  );
}
