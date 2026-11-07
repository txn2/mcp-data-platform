import { useEffect, useRef, useState } from "react";
import { AtSign, Bold, Code, Italic, Link, List, Maximize2, Minimize2 } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useAppendThreadEvent, useUpdateThread } from "@/api/portal/hooks";
import type { Thread } from "@/api/portal/types";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { MentionTextarea } from "../MentionTextarea";
import { targetOfThread } from "../targetFilter";

// ReplyComposer is the reply box at the foot of a thread (#2532): at least
// 150px tall, resizable and expandable, with a formatting toolbar over the
// Markdown the reply is written in, the Write/Preview switch, a draft kept
// while the thread is open, and "Reply and resolve" for whoever may moderate
// the thread.

const draftKey = (threadId: string) => `thread-reply-draft:${threadId}`;

function readDraft(threadId: string): string {
  try {
    return sessionStorage.getItem(draftKey(threadId)) ?? "";
  } catch {
    return "";
  }
}

function writeDraft(threadId: string, text: string) {
  try {
    if (text) sessionStorage.setItem(draftKey(threadId), text);
    else sessionStorage.removeItem(draftKey(threadId));
  } catch {
    // A browser with storage blocked keeps the draft only in the box.
  }
}

/** A formatting action: what it puts around the selection, or at the caret. */
interface Format {
  label: string;
  icon: LucideIcon;
  before: string;
  after: string;
  /** placeholder is inserted between before and after when nothing is selected. */
  placeholder: string;
  /** line puts before at the start of each selected line. */
  line?: boolean;
}

const FORMATS: Format[] = [
  { label: "Bold", icon: Bold, before: "**", after: "**", placeholder: "bold" },
  { label: "Italic", icon: Italic, before: "_", after: "_", placeholder: "italic" },
  { label: "Code", icon: Code, before: "`", after: "`", placeholder: "code" },
  { label: "Link", icon: Link, before: "[", after: "](https://)", placeholder: "text" },
  { label: "List", icon: List, before: "- ", after: "", placeholder: "item", line: true },
  { label: "Mention", icon: AtSign, before: "@", after: "", placeholder: "" },
];

/** applyFormat wraps text[start, end) in a format, returning the new text and selection. */
export function applyFormat(text: string, start: number, end: number, f: Format): { text: string; start: number; end: number } {
  const selected = text.slice(start, end) || f.placeholder;
  const inner = f.line ? selected.split("\n").map((l) => f.before + l).join("\n") : f.before + selected + f.after;
  const next = text.slice(0, start) + inner + text.slice(end);
  const from = f.line ? start : start + f.before.length;
  return { text: next, start: from, end: f.line ? start + inner.length : from + selected.length };
}

export function ReplyComposer({ threadId, thread, mayResolve }: { threadId: string; thread: Thread; mayResolve: boolean }) {
  const append = useAppendThreadEvent();
  const update = useUpdateThread();
  const [reply, setReply] = useState(() => readDraft(threadId));
  const [expanded, setExpanded] = useState(false);
  const boxRef = useRef<HTMLDivElement>(null);

  useEffect(() => writeDraft(threadId, reply), [threadId, reply]);

  const format = (f: Format) => {
    const area = boxRef.current?.querySelector("textarea");
    const start = area?.selectionStart ?? reply.length;
    const end = area?.selectionEnd ?? reply.length;
    const next = applyFormat(reply, start, end, f);
    setReply(next.text);
    requestAnimationFrame(() => {
      area?.focus();
      area?.setSelectionRange(next.start, next.end);
    });
  };

  const post = (resolve: boolean) => {
    const body = reply.trim();
    if (!body || append.isPending) return;
    append.mutate(
      { threadId, event_type: "comment", body },
      {
        onSuccess: () => {
          setReply("");
          if (resolve) update.mutate({ id: threadId, status: "resolved" });
        },
      },
    );
  };

  const busy = append.isPending || update.isPending;
  const closed = thread.status === "resolved" || thread.status === "wont_fix";
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        post(false);
      }}
      className="border-t p-3"
    >
      <FormatToolbar onFormat={format} expanded={expanded} onExpand={() => setExpanded(!expanded)} />
      <div ref={boxRef} data-testid="reply-box">
        <MentionTextarea
          target={targetOfThread(thread)}
          value={reply}
          onChange={setReply}
          rows={6}
          placeholder="Reply… type @ to mention someone"
          aria-label="Reply"
          className={cn("resize-y", expanded ? "min-h-[360px]" : "min-h-[150px]")}
        />
      </div>
      {append.isError && <p className="mt-1 text-xs text-destructive">Failed to post reply.</p>}
      {update.isError && <p className="mt-1 text-xs text-destructive">The reply was posted; the thread could not be resolved.</p>}
      <ComposerActions
        canResolve={mayResolve && !closed}
        disabled={!reply.trim() || busy}
        posting={append.isPending}
        onResolve={() => post(true)}
      />
    </form>
  );
}

/** FormatToolbar is the formatting buttons over the reply box and its expand control. */
function FormatToolbar({
  onFormat,
  expanded,
  onExpand,
}: {
  onFormat: (f: Format) => void;
  expanded: boolean;
  onExpand: () => void;
}) {
  return (
    <div className="mb-1.5 flex items-center gap-0.5" role="toolbar" aria-label="Formatting">
      {FORMATS.map((f) => {
        const Icon = f.icon;
        return (
          <Button key={f.label} type="button" size="icon-xs" variant="ghost" aria-label={f.label} title={f.label} onClick={() => onFormat(f)}>
            <Icon />
          </Button>
        );
      })}
      <Button
        type="button"
        size="icon-xs"
        variant="ghost"
        className="ml-auto"
        aria-label={expanded ? "Shrink the reply box" : "Expand the reply box"}
        aria-pressed={expanded}
        onClick={onExpand}
      >
        {expanded ? <Minimize2 /> : <Maximize2 />}
      </Button>
    </div>
  );
}

/** ComposerActions is Reply, and Reply and resolve for whoever may resolve. */
function ComposerActions({
  canResolve,
  disabled,
  posting,
  onResolve,
}: {
  canResolve: boolean;
  disabled: boolean;
  posting: boolean;
  onResolve: () => void;
}) {
  return (
    <div className="mt-2 flex justify-end gap-2">
      {canResolve && (
        <Button type="button" size="sm" variant="outline" disabled={disabled} onClick={onResolve}>
          Reply and resolve
        </Button>
      )}
      <Button type="submit" size="sm" disabled={disabled}>
        {posting ? "Posting…" : "Reply"}
      </Button>
    </div>
  );
}
