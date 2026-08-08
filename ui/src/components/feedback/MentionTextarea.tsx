import { useEffect, useRef, useState } from "react";
import {
  useMentionCandidates,
  useMentionEligibility,
  type MentionCandidate,
} from "@/api/portal/hooks";
import type { FeedbackTarget } from "@/api/portal/types";
import { activeMentionQuery, replaceMentionQuery, scanMentions } from "@/lib/mentions";
import { useAuthStore } from "@/stores/auth";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

interface Props {
  target: FeedbackTarget;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  rows?: number;
  required?: boolean;
  className?: string;
  "aria-label"?: string;
}

function candidateName(c: MentionCandidate): string {
  return [c.first_name, c.last_name].filter(Boolean).join(" ");
}

/**
 * DeliveryHint states who this comment will actually reach, before it is sent.
 * Someone the item is not shared with by email cannot be notified, and saying
 * so while the author is still typing is the difference between a mention that
 * quietly goes nowhere and one they can fix.
 */
function DeliveryHint({ willNotify, unreachable }: { willNotify: string[]; unreachable: string[] }) {
  return (
    <>
      {willNotify.length > 0 && (
        <p className="mt-1 text-xs text-muted-foreground">Notifying {willNotify.join(", ")}</p>
      )}
      {unreachable.length > 0 && (
        <p className="mt-1 text-xs text-amber-600 dark:text-amber-500">
          {unreachable.join(", ")} {unreachable.length === 1 ? "is" : "are"} not among the people
          this item is shared with by email, so they will not be notified. Share it with them
          first.
        </p>
      )}
    </>
  );
}

/**
 * MentionTextarea is the comment composer: a plain textarea that opens a
 * type-ahead when the caret sits in an "@..." token, and inserts the chosen
 * person as @local(domain).
 *
 * The suggestions are the people who can open the thread's target, so the
 * composer only offers mentions that will actually be delivered. A hand-typed
 * name outside that set is left alone -- it posts as ordinary text and notifies
 * nobody -- and the hint below the box says so before the comment is sent. The
 * author's own address is excluded from both: you cannot notify yourself, and
 * saying you lack access to your own item would be false.
 */
export function MentionTextarea({
  target,
  value,
  onChange,
  placeholder,
  rows = 3,
  required,
  className,
  "aria-label": ariaLabel,
}: Props) {
  const me = useAuthStore((s) => s.user);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const [active, setActive] = useState<ReturnType<typeof activeMentionQuery>>(null);
  const [debounced, setDebounced] = useState("");
  const [highlight, setHighlight] = useState(0);
  // Pending caret position after an insertion; applied once React has painted
  // the new value, otherwise the browser drops the caret at the end.
  const [pendingCaret, setPendingCaret] = useState<number | null>(null);

  useEffect(() => {
    const t = setTimeout(() => setDebounced(active?.query.trim() ?? ""), 150);
    return () => clearTimeout(t);
  }, [active]);

  const { data } = useMentionCandidates(target, debounced, active !== null);
  const suggestions = data?.candidates ?? [];
  const open = active !== null && suggestions.length > 0;

  useEffect(() => setHighlight(0), [debounced, data]);

  useEffect(() => {
    if (pendingCaret === null) return;
    textareaRef.current?.setSelectionRange(pendingCaret, pendingCaret);
    setPendingCaret(null);
  }, [pendingCaret, value]);

  // Close the picker on a click outside the composer.
  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) setActive(null);
    };
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, [open]);

  const syncTrigger = (text: string, caret: number) => setActive(activeMentionQuery(text, caret));

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    onChange(e.target.value);
    syncTrigger(e.target.value, e.target.selectionStart ?? e.target.value.length);
  };

  const select = (candidate: MentionCandidate) => {
    if (!active) return;
    const next = replaceMentionQuery(value, active, candidate.email);
    onChange(next.text);
    setActive(null);
    setPendingCaret(next.caret);
    textareaRef.current?.focus();
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (!open) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setHighlight((h) => Math.min(h + 1, suggestions.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setHighlight((h) => Math.max(h - 1, 0));
    } else if (e.key === "Enter" || e.key === "Tab") {
      e.preventDefault();
      select(suggestions[highlight]!);
    } else if (e.key === "Escape") {
      e.preventDefault();
      setActive(null);
    }
  };

  // A mention of yourself notifies nobody: the write path drops it before the
  // audience lookup, so it is neither checked nor reported on here.
  const myEmail = (me?.email ?? "").toLowerCase();
  const mentioned = scanMentions(value).filter((m) => m.email !== myEmail);
  const eligibility = useMentionEligibility(
    target,
    mentioned.map((m) => m.email),
  );
  const willNotify = mentioned.filter((m) => eligibility[m.email] === true).map((m) => m.email);
  const unreachable = mentioned.filter((m) => eligibility[m.email] === false).map((m) => m.email);

  return (
    <div ref={containerRef} className="relative">
      <Textarea
        ref={textareaRef}
        aria-label={ariaLabel}
        value={value}
        rows={rows}
        required={required}
        placeholder={placeholder}
        onChange={handleChange}
        onKeyDown={onKeyDown}
        onKeyUp={(e) => syncTrigger(e.currentTarget.value, e.currentTarget.selectionStart ?? 0)}
        onClick={(e) => syncTrigger(e.currentTarget.value, e.currentTarget.selectionStart ?? 0)}
        onBlur={() => setPendingCaret(null)}
        // ui/textarea sizes to its content by default, which silently overrides
        // the caller's `rows`; the composer asks for a fixed height so the
        // suggestion list below it does not move as the body is typed.
        className={cn("field-sizing-fixed min-h-0 py-1.5 text-sm", className)}
      />
      {open && (
        <ul
          role="listbox"
          aria-label="Mention a teammate"
          className="absolute z-20 mt-1 max-h-56 w-full overflow-auto rounded-md border bg-popover shadow-md"
        >
          {suggestions.map((c, i) => (
            <li key={c.email}>
              <button
                type="button"
                role="option"
                aria-selected={i === highlight}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => select(c)}
                onMouseEnter={() => setHighlight(i)}
                className={cn(
                  "flex w-full items-baseline gap-2 px-2 py-1.5 text-left text-sm",
                  i === highlight && "bg-accent",
                )}
              >
                <span className="font-medium">{candidateName(c) || c.email}</span>
                {candidateName(c) && (
                  <span className="truncate text-xs text-muted-foreground">{c.email}</span>
                )}
                {!c.confirmed && (
                  <span className="ml-auto shrink-0 text-[10px] uppercase text-muted-foreground">
                    not signed in yet
                  </span>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
      <DeliveryHint willNotify={willNotify} unreachable={unreachable} />
    </div>
  );
}
