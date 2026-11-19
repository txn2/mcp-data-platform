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
//
// On a phone's feedback sheet (collapsible, #2705) the box waits as one
// "Reply…" row so the comments get the sheet; a tap opens the toolbar, the
// Write/Preview tabs and the buttons, and there is no expand control. It folds
// back once the reply is posted, or left empty.

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

export function ReplyComposer({
  threadId,
  thread,
  mayResolve,
  collapsible = false,
  onOpen,
}: {
  threadId: string;
  thread: Thread;
  mayResolve: boolean;
  collapsible?: boolean;
  /** onOpen runs when a collapsible box opens or is focused. */
  onOpen?: () => void;
}) {
  const [reply, setReply] = useState(() => readDraft(threadId));
  const [expanded, setExpanded] = useState(false);
  const boxRef = useRef<HTMLDivElement>(null);
  const fold = useFold(threadId, collapsible, boxRef, onOpen);
  const { append, update, post } = usePost(threadId, reply, () => {
    setReply("");
    if (collapsible) fold.close();
  });

  useEffect(() => writeDraft(threadId, reply), [threadId, reply]);
  const format = (f: Format) => formatIn(boxRef.current, reply, setReply, f);

  const busy = append.isPending || update.isPending;
  if (!fold.open) return <CollapsedReply onOpen={fold.openBox} />;
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        post(false);
      }}
      onFocus={collapsible ? onOpen : undefined}
      onBlur={(e) => {
        // An empty box folds back when the reader leaves it.
        if (collapsible && !reply.trim() && !e.currentTarget.contains(e.relatedTarget as Node | null)) fold.close();
      }}
      className={formShape(collapsible)}
      data-testid="reply-composer"
    >
      <FormatToolbar
        onFormat={format}
        expanded={expanded}
        onExpand={collapsible ? undefined : () => setExpanded(!expanded)}
      />
      <div ref={boxRef} data-testid="reply-box">
        <MentionTextarea
          target={targetOfThread(thread)}
          value={reply}
          onChange={setReply}
          placeholder="Reply… type @ to mention someone"
          aria-label="Reply"
          rows={collapsible ? 3 : 6}
          className={composerHeight(collapsible, expanded)}
        />
      </div>
      <ComposerErrors posting={append.isError} resolving={update.isError} />
      <ComposerActions
        canResolve={mayResolve && isOpenThread(thread)}
        disabled={!reply.trim() || busy}
        posting={append.isPending}
        onResolve={() => post(true)}
      />
    </form>
  );
}

/** formShape is the composer's frame: tight on a phone's sheet (#2705). */
function formShape(collapsible: boolean): string {
  return collapsible ? "flex-none border-t px-3 pt-1.5 pb-2" : "border-t p-3";
}

/** isOpenThread reports whether a reply may still resolve the thread. */
function isOpenThread(thread: Thread): boolean {
  return thread.status !== "resolved" && thread.status !== "wont_fix";
}

/** ComposerErrors says what failed: the reply, or the resolve after it. */
function ComposerErrors({ posting, resolving }: { posting: boolean; resolving: boolean }) {
  return (
    <>
      {posting && <p className="mt-1 text-xs text-destructive">Failed to post reply.</p>}
      {resolving && <p className="mt-1 text-xs text-destructive">The reply was posted; the thread could not be resolved.</p>}
    </>
  );
}

/** formatIn applies a format to the selection in the reply box under box. */
function formatIn(box: HTMLElement | null, reply: string, setReply: (text: string) => void, f: Format) {
  const area = box?.querySelector("textarea");
  const start = area?.selectionStart ?? reply.length;
  const end = area?.selectionEnd ?? reply.length;
  const next = applyFormat(reply, start, end, f);
  setReply(next.text);
  requestAnimationFrame(() => {
    area?.focus();
    area?.setSelectionRange(next.start, next.end);
  });
}

/** usePost posts the reply, and on "Reply and resolve" resolves the thread. */
function usePost(threadId: string, reply: string, onPosted: () => void) {
  const append = useAppendThreadEvent();
  const update = useUpdateThread();
  const post = (resolve: boolean) => {
    const body = reply.trim();
    if (!body || append.isPending) return;
    append.mutate(
      { threadId, event_type: "comment", body },
      {
        onSuccess: () => {
          onPosted();
          if (resolve) update.mutate({ id: threadId, status: "resolved" });
        },
      },
    );
  };
  return { append, update, post };
}

/** useFold is whether a collapsible box is open (#2705). One holding a draft
 * opens with it; opening it focuses the box. */
function useFold(threadId: string, collapsible: boolean, box: React.RefObject<HTMLDivElement | null>, onOpen?: () => void) {
  const [open, setOpen] = useState(() => !collapsible || readDraft(threadId) !== "");
  return {
    open,
    close: () => setOpen(false),
    openBox: () => {
      setOpen(true);
      onOpen?.();
      requestAnimationFrame(() => box.current?.querySelector("textarea")?.focus());
    },
  };
}

/** composerHeight is the reply box's size: short and fixed on a phone's sheet,
 * where the comment being answered stays above it, and tall and resizable
 * beside the page. */
function composerHeight(collapsible: boolean, expanded: boolean): string {
  if (collapsible) return "min-h-20 resize-none";
  return cn("resize-y", expanded ? "min-h-[360px]" : "min-h-[150px]");
}

/** CollapsedReply is the reply box folded to one row (#2705). */
function CollapsedReply({ onOpen }: { onOpen: () => void }) {
  return (
    <div className="flex-none border-t px-3 py-2">
      <button
        type="button"
        onClick={onOpen}
        className="h-11 w-full rounded-lg border bg-background px-3 text-left text-sm text-muted-foreground"
      >
        Reply…
      </button>
    </div>
  );
}

/** FormatToolbar is the formatting buttons over the reply box and, beside the
 * page, its expand control. */
function FormatToolbar({
  onFormat,
  expanded,
  onExpand,
}: {
  onFormat: (f: Format) => void;
  expanded: boolean;
  onExpand?: () => void;
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
      {onExpand && (
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
      )}
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
