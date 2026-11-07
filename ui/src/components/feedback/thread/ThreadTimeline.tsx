import { useState } from "react";
import type {
  FeedbackTarget,
  ThreadEvent,
  ThreadStatus,
} from "@/api/portal/types";
import {
  useDeleteThreadEvent,
  useDirectoryNames,
  useEditThreadEvent,
} from "@/api/portal/hooks";
import { useAuthStore } from "@/stores/auth";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { CommentBody } from "../CommentBody";
import { MentionTextarea } from "../MentionTextarea";
import { STATUS_LABEL, formatRelative } from "../meta";
import { STATUS_DOT, isStatusEvent } from "./StatusMenu";

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
  return Array.isArray(raw)
    ? raw.filter((m): m is string => typeof m === "string")
    : [];
}

// Only a comment is a message a person can change (#2514): every other event
// records a decision or a link, and the server refuses to change one.
function isChangeable(e: ThreadEvent): boolean {
  return e.event_type === "comment" && !e.deleted_at;
}

// ThreadTimeline is the thread's record (#2532): each message a card with its
// author's name, and each status, resolution or visibility change a thin line
// between them, visibly not a message. A message's author can edit and delete
// it; canRemoveAny (the target's owner or editor, or an administrator) can
// delete any message. The status change the reader may still undo carries an
// Undo (undoableId). The server decides all of it; these only decide what is
// offered.
export function ThreadTimeline({
  events,
  isLoading,
  threadId,
  target,
  canRemoveAny = false,
  undoableId,
  onUndo,
  undoing = false,
}: {
  events: ThreadEvent[] | undefined;
  isLoading: boolean;
  threadId?: string;
  target?: FeedbackTarget;
  canRemoveAny?: boolean;
  undoableId?: string | null;
  onUndo?: (eventId: string) => void;
  undoing?: boolean;
}) {
  const me = useAuthStore((s) => s.user);
  const myEmail = (me?.email ?? "").toLowerCase();
  const names = useDirectoryNames(
    (events ?? []).map((e) => e.author_email).filter(Boolean),
  );
  const nameOf = (email: string) => names[email.toLowerCase()] || email;
  return (
    <div className="flex-1 space-y-3 overflow-auto p-3">
      {isLoading && (
        <p className="text-xs text-muted-foreground">Loading timeline…</p>
      )}
      {(events ?? []).map((e) => {
        const mine = myEmail !== "" && e.author_email.toLowerCase() === myEmail;
        return isLineEvent(e) ? (
          <TimelineLine
            key={e.id}
            event={e}
            name={mine ? "You" : nameOf(e.author_email)}
            onUndo={undoableId === e.id ? onUndo : undefined}
            undoing={undoing}
          />
        ) : (
          <TimelineEvent
            key={e.id}
            event={e}
            name={nameOf(e.author_email)}
            threadId={threadId}
            target={target}
            canEdit={canEditEvent(threadId, mine, e)}
            canDelete={canEditEvent(threadId, mine || canRemoveAny, e)}
          />
        );
      })}
    </div>
  );
}

/** canEditEvent reports a changeable message the reader is allowed to change. */
function canEditEvent(threadId: string | undefined, allowed: boolean, e: ThreadEvent): boolean {
  return Boolean(threadId) && allowed && isChangeable(e);
}

/** isLineEvent is an event drawn as a line between messages, not a card. */
function isLineEvent(e: ThreadEvent): boolean {
  return isStatusEvent(e) || e.event_type === "visibility_change";
}

// TimelineLine is a status, resolution or visibility change: a dot and "Name
// marked Status", centered between the messages, with its Undo while the
// reader may still take it back.
function TimelineLine({
  event: e,
  name,
  onUndo,
  undoing,
}: {
  event: ThreadEvent;
  name: string;
  onUndo?: (eventId: string) => void;
  undoing: boolean;
}) {
  const next = e.metadata?.["new_status"] as ThreadStatus | undefined;
  return (
    <div className="flex items-center gap-2 text-xs text-muted-foreground" data-testid={`timeline-line-${e.id}`}>
      <span aria-hidden="true" className="h-px flex-1 bg-border" />
      <span aria-hidden="true" className={cn("size-1.5 rounded-full", lineDot(next))} />
      <span>
        <span className="font-medium text-foreground">{name}</span> <LineWhat event={e} next={next} /> ·{" "}
        {formatRelative(e.created_at)}
      </span>
      {onUndo && (
        <button
          type="button"
          className="font-medium text-primary hover:underline"
          disabled={undoing}
          onClick={() => onUndo(e.id)}
        >
          Undo
        </button>
      )}
      <span aria-hidden="true" className="h-px flex-1 bg-border" />
    </div>
  );
}

function lineDot(next: ThreadStatus | undefined): string {
  return (next && STATUS_DOT[next]) || "bg-muted-foreground";
}

/** LineWhat says what a line's change did. */
function LineWhat({ event: e, next }: { event: ThreadEvent; next: ThreadStatus | undefined }) {
  if (e.event_type === "visibility_change") {
    return <>made it {e.metadata?.["to"] === "shared" ? "visible to everyone it is shared with" : "internal"}</>;
  }
  return (
    <>
      marked <strong className="text-foreground">{next ? (STATUS_LABEL[next] ?? next) : "a new status"}</strong>
    </>
  );
}

function initials(name: string): string {
  const words = name
    .replace(/@.*/, "")
    .split(/[\s._-]+/)
    .filter(Boolean);
  return ((words[0]?.[0] ?? "") + (words[1]?.[0] ?? "")).toUpperCase() || "?";
}

function TimelineEvent({
  event: e,
  name,
  threadId,
  target,
  canEdit,
  canDelete,
}: {
  event: ThreadEvent;
  name: string;
  threadId?: string;
  target?: FeedbackTarget;
  canEdit: boolean;
  canDelete: boolean;
}) {
  const [editing, setEditing] = useState(false);
  return (
    <div
      className="flex gap-2.5 rounded-lg border p-3 text-sm"
      data-testid={`timeline-card-${e.id}`}
    >
      <span
        aria-hidden="true"
        className="flex size-7 shrink-0 items-center justify-center rounded-full bg-muted text-[11px] font-semibold text-muted-foreground"
      >
        {initials(name)}
      </span>
      <div className="min-w-0 flex-1">
        <EventHeader event={e} name={name} />
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
    </div>
  );
}

// EventHeader says who did what and when, and that a message was edited. A
// comment needs no verb; any other event says what it did.
function EventHeader({ event: e, name }: { event: ThreadEvent; name: string }) {
  const summary = e.event_type === "comment" ? null : eventSummary(e);
  return (
    <p className="text-xs text-muted-foreground">
      <span className="font-medium text-foreground" title={e.author_email}>
        {name}
      </span>{" "}
      {summary ? `${summary} · ` : "· "}
      {formatRelative(e.created_at)}
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
    return (
      <p className="mt-0.5 text-xs italic text-muted-foreground">
        Message deleted
      </p>
    );
  }
  if (!e.body) return null;
  return (
    <CommentBody
      body={e.body}
      mentions={recordedMentions(e)}
      className="mt-0.5"
    />
  );
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
    edit.mutate(
      { threadId, eventId: event.id, body: text.trim() },
      { onSuccess: onDone },
    );
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
      {edit.isError && (
        <p className="text-xs text-destructive">Failed to save the message.</p>
      )}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={onDone}>
          Cancel
        </Button>
        <Button
          type="submit"
          size="sm"
          disabled={!text.trim() || edit.isPending}
        >
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
      {del.isError && (
        <span className="text-destructive">Failed to delete the message.</span>
      )}
    </div>
  );
}
