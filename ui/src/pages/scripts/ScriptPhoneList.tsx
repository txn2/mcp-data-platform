import { BookOpen, ChevronRight } from "lucide-react";
import { HandlerTypeBadges } from "./HandlerTypeBadge";
import type { PortalScriptRow } from "@/api/portal/hooks/scripts";
import { scheduleLine, scheduleState } from "./cadence";
import { agoText, endpointTail } from "./listingFormat";
import { InertBadge, KIND_TEXT, RunDot, runMoment, unscheduledText } from "./ScriptRow";
import { runStatusLabel } from "./runFormat";

// ScriptPhoneList is the Automations listing on a phone, or wherever no table
// fits (#2601): one row per automation, its last run as a dot, its name, what
// it does (its cadence and next fire, or its endpoint), and who wrote it with
// how its last run went. The whole row is the tap target.

export function ScriptPhoneList({
  rows,
  basePath,
  onNavigate,
}: {
  rows: PortalScriptRow[];
  basePath: string;
  onNavigate: (path: string) => void;
}) {
  return (
    <ul className="-mx-4 divide-y border-t" data-testid="script-list">
      {rows.map((row) => (
        <li key={row.script.id}>
          <PhoneRow row={row} onOpen={() => onNavigate(`${basePath}/${row.script.id}`)} />
        </li>
      ))}
    </ul>
  );
}

function PhoneRow({ row, onOpen }: { row: PortalScriptRow; onOpen: () => void }) {
  const { script } = row;
  const name = script.display_name || script.name;
  return (
    <button
      type="button"
      onClick={onOpen}
      className="flex w-full min-w-0 items-start gap-2.5 px-4 py-3 text-left hover:bg-muted/60 focus-visible:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
      data-testid={`script-item-${script.id}`}
    >
      <span className="mt-1.5">
        <RunDot status={script.library ? undefined : row.last_run?.status} />
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="min-w-0 truncate text-sm font-semibold">{name}</span>
          {script.library && (
            <span role="img" aria-label={KIND_TEXT.library} title={KIND_TEXT.library} className="shrink-0 text-blue-600 dark:text-blue-300">
              <BookOpen className="size-3.5" aria-hidden />
            </span>
          )}
          <InertBadge row={row} />
        </span>
        <span className="truncate text-xs text-foreground/80">{whatItDoes(row)}</span>
        {/* The handler types (#2435) lead the third line, so the name keeps
            the whole first line at a phone's width. */}
        <span className="flex min-w-0 items-center gap-1.5">
          <HandlerTypeBadges types={row.handler_types} className="shrink-0 flex-nowrap" />
          <span className="min-w-0 truncate text-xs text-muted-foreground">{whoAndHow(row)}</span>
        </span>
      </span>
      <ChevronRight className="mt-1 size-4 shrink-0 text-muted-foreground" aria-hidden />
    </button>
  );
}

/** whatItDoes is a row's cadence and next fire, its endpoint, or what a library is for. */
export function whatItDoes(row: PortalScriptRow): string {
  if (row.script.library) return "Loaded by other automations";
  const schedules = row.schedules ?? [];
  if (schedules.length > 0) return schedulesText(schedules);
  const endpoint = row.endpoints?.[0];
  if (endpoint) {
    return `Endpoint ${endpointTail(endpoint.path)}${endpoint.enabled ? "" : " · Off"}`;
  }
  return unscheduledText(row);
}

// schedulesText is the first schedule's cadence and what it does next, and
// how many more schedules the script holds (#2434).
function schedulesText(schedules: NonNullable<PortalScriptRow["schedules"]>): string {
  const [first] = schedules;
  const line = scheduleLine(first!.cron_spec, first!.timezone);
  const state = scheduleState(first!);
  const more = schedules.length > 1 ? ` · +${schedules.length - 1} more` : "";
  if (state.kind === "paused") return `${line} · paused${more}`;
  if (state.kind === "due") return `${line} · next ${agoOrDate(state.at)}${more}`;
  return `${line}${more}`;
}

/** A next fire as the listing's short date. */
function agoOrDate(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "";
  return at.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

/** whoAndHow is the author, then the last run in a word with how long ago. */
export function whoAndHow(row: PortalScriptRow): string {
  const who = row.script.owner_email || "nobody";
  if (row.script.library) return `${who} · updated ${agoText(row.script.updated_at)}`;
  if (!row.last_run) return `${who} · never run`;
  const status = runStatusLabel(row.last_run.status).toLowerCase();
  if (row.last_run.status === "running") return `${who} · running`;
  return `${who} · ${status} ${agoText(runMoment(row.last_run))}`;
}
