import { Check, ChevronDown } from "lucide-react";
import type { ThreadEvent, ThreadStatus } from "@/api/portal/types";
import { STATUS_UNDO_MS } from "@/api/portal/hooks";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { STATUS_LABEL } from "../meta";

// StatusMenu is a moderator's status control in the thread header (#2532): a
// button showing the current status, opening the statuses grouped by whether
// the thread stays open, each with a line saying what it means.

/** What each status says to the person picking it. */
export const STATUS_DESCRIPTION: Record<ThreadStatus, string> = {
  open: "Waiting on the owner. Shows in their worklist.",
  answered: "A reply was given; waiting on the person who asked.",
  acknowledged: "Seen and accepted, not acted on yet. Stays open.",
  resolved: "Done. Leaves every worklist.",
  wont_fix: "Closed without a change. Add a reason below.",
};

/** The dot each status is drawn with, in the menu and on the timeline. */
export const STATUS_DOT: Record<ThreadStatus, string> = {
  open: "bg-amber-400",
  answered: "bg-blue-400",
  acknowledged: "bg-violet-400",
  resolved: "bg-green-400",
  wont_fix: "bg-zinc-400",
};

const GROUPS: { label: string; statuses: ThreadStatus[] }[] = [
  { label: "Still open", statuses: ["open", "answered", "acknowledged"] },
  { label: "Closes the thread", statuses: ["resolved", "wont_fix"] },
];

export function StatusMenu({
  status,
  disabled,
  onChange,
}: {
  status: ThreadStatus;
  disabled: boolean;
  onChange: (status: ThreadStatus) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="sm" variant="outline" disabled={disabled} aria-label={`Status: ${STATUS_LABEL[status]}`} className="h-7 gap-1.5 text-xs">
          <span aria-hidden="true" className={`size-2 rounded-full ${STATUS_DOT[status]}`} />
          {STATUS_LABEL[status]}
          <ChevronDown aria-hidden="true" className="size-3.5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-72">
        {GROUPS.map((g, i) => (
          <DropdownMenuGroup key={g.label}>
            {i > 0 && <DropdownMenuSeparator />}
            <DropdownMenuLabel className="text-xs font-semibold text-muted-foreground">{g.label}</DropdownMenuLabel>
            {g.statuses.map((s) => (
              <DropdownMenuItem key={s} onSelect={() => s !== status && onChange(s)} className="items-start">
                <span aria-hidden="true" className={`mt-1.5 size-2 shrink-0 rounded-full ${STATUS_DOT[s]}`} />
                <span className="flex flex-1 flex-col">
                  <span className="font-medium">{STATUS_LABEL[s]}</span>
                  <span className="text-xs text-muted-foreground">{STATUS_DESCRIPTION[s]}</span>
                </span>
                <Check aria-hidden="true" className={s === status ? "mt-0.5" : "invisible mt-0.5"} />
              </DropdownMenuItem>
            ))}
          </DropdownMenuGroup>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** isStatusEvent reports an event a status change wrote. */
export function isStatusEvent(e: ThreadEvent): boolean {
  return e.event_type === "status_change" || e.event_type === "resolution";
}

/**
 * undoableStatusEvent is the status change the reader may still undo: the
 * last one in the thread, theirs, within STATUS_UNDO_MS, with nothing anyone
 * else added after it. The server checks the same.
 */
export function undoableStatusEvent(events: ThreadEvent[] | undefined, myEmail: string, now: number): ThreadEvent | null {
  if (!events || !myEmail) return null;
  const me = myEmail.toLowerCase();
  for (let i = events.length - 1; i >= 0; i--) {
    const e = events[i]!;
    const mine = e.author_email.toLowerCase() === me;
    if (isStatusEvent(e)) {
      return mine && now - new Date(e.created_at).getTime() <= STATUS_UNDO_MS ? e : null;
    }
    if (!mine) return null;
  }
  return null;
}
