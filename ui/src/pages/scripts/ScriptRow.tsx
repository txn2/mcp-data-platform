import { BookOpen, FileCode2 } from "lucide-react";
import { PrincipalLabel } from "@/components/PrincipalLabel";
import { OpenRowChevron } from "@/components/patterns/OpenRowChevron";
import { Badge } from "@/components/ui/badge";
import { HandlerTypeBadges } from "./HandlerTypeBadge";
import { TableCell, TableRow } from "@/components/ui/table";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { PortalScriptRow } from "@/api/portal/hooks/scripts";
import { openRowProps } from "@/lib/openRow";
import { cn } from "@/lib/utils";
import { scheduleLine, scheduleState } from "./cadence";
import { agoText, endpointTail, endpointURL, exactWhen, nextFireText } from "./listingFormat";
import { runStatusLabel, runStatusVariant } from "./runFormat";

// One row of the scripts listing, and the cells that need more than a string.
// Split out of ScriptListing for that file's line budget; the listing above it
// is the bar, the health line and the table, and this is what a row of that
// table says.
//
// The row is laid out for a fixed-width table (#2601): every cell is at most
// two lines, each line truncates rather than wrapping, and the full value is on
// hover. Which columns show is the listing's call, from the width it has.

/** RowColumns is which of the optional columns the listing has room for. */
export interface RowColumns {
  endpoint: boolean;
  updated: boolean;
}

const ALL_COLUMNS: RowColumns = { endpoint: true, updated: true };

export function ScriptRow({
  row,
  basePath,
  onNavigate,
  columns = ALL_COLUMNS,
}: {
  row: PortalScriptRow;
  basePath: string;
  onNavigate: (path: string) => void;
  columns?: RowColumns;
}) {
  const { script } = row;
  const open = openRowProps(() => onNavigate(`${basePath}/${script.id}`));
  return (
    // The whole row opens the script (#2601, #2587): a click anywhere on it,
    // or Enter on the focused row, with a chevron at its end saying so.
    <TableRow {...open} className={cn(open.className, "align-top")} data-testid={`script-row-${script.id}`}>
      <TableCell className="py-2.5 pr-0 pl-3">
        <KindIcon library={!!script.library} />
      </TableCell>
      <TableCell className="min-w-0 py-2.5 pl-2">
        <NameCell row={row} chip={!columns.endpoint} />
      </TableCell>
      <TableCell className="py-2.5 text-xs">
        {script.owner_email ? (
          <PrincipalLabel userId={script.owner_email} email={script.owner_email} />
        ) : (
          <span className="text-muted-foreground">nobody</span>
        )}
      </TableCell>
      <TableCell className="py-2.5">
        <ScheduleCell row={row} />
      </TableCell>
      {columns.endpoint && (
        <TableCell className="py-2.5">
          <EndpointCell row={row} />
        </TableCell>
      )}
      <TableCell className="py-2.5">
        <LastRunCell row={row} />
      </TableCell>
      {columns.updated && (
        <TableCell className="py-2.5 text-xs whitespace-nowrap text-muted-foreground">
          <span title={exactWhen(script.updated_at)} data-testid="script-updated">
            {agoText(script.updated_at)}
          </span>
        </TableCell>
      )}
      <OpenRowChevron />
    </TableRow>
  );
}

/** The name on one line with its badge, then the slug and the category. */
function NameCell({ row, chip }: { row: PortalScriptRow; chip: boolean }) {
  const { script } = row;
  const name = script.display_name || script.name;
  const endpoint = chip ? firstEndpoint(row) : undefined;
  return (
    <div className="min-w-0">
      <div className="flex min-w-0 items-center gap-1.5">
        <span className="min-w-0 truncate font-medium" title={name} data-testid="script-name">
          {name}
        </span>
        <InertBadge row={row} />
      </div>
      {/* The slug and the category, its handler types (#2435), and where
          Endpoint has no column a bound endpoint as a chip at the line's end,
          so the row stays two lines and the name keeps the first. The chip
          stands for the Endpoint badge: the address says it is one. */}
      <div className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
        <span className="min-w-[3rem] flex-1 truncate">
          <span className="font-mono">{script.name}</span>
          {script.category && (
            <>
              <span aria-hidden> · </span>
              <span>{script.category}</span>
            </>
          )}
        </span>
        <HandlerTypeBadges types={row.handler_types} omit={endpoint ? ["endpoint"] : []} className="shrink-0" />
        {endpoint && (
          <span className="inline-flex max-w-[50%] min-w-0 items-center gap-1 rounded border bg-background px-1.5 text-[11px] text-foreground">
            <EndpointText path={endpoint.path} />
            {!endpoint.enabled && <span className="shrink-0 text-muted-foreground">· Off</span>}
            <MoreCount count={(row.endpoints?.length ?? 0) - 1} />
          </span>
        )}
      </div>
    </div>
  );
}

// KIND_TEXT says what each kind is, as the icon's tooltip and its accessible
// name (#2601). The words are the Kind filter's.
export const KIND_TEXT = {
  script: "Script: runs on a schedule, on demand, or when its endpoint is called.",
  library: "Library: code other automations load. It never runs on its own.",
} as const;

/** KindIcon is a row's kind as an icon, a file-code for a script and a book
 * for a library, which replaces the Library badge beside the name. */
export function KindIcon({ library }: { library: boolean }) {
  const text = library ? KIND_TEXT.library : KIND_TEXT.script;
  const Icon = library ? BookOpen : FileCode2;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          role="img"
          aria-label={text}
          data-testid="automation-kind"
          data-kind={library ? "library" : "script"}
          className={cn(
            "mt-0.5 inline-flex size-[22px] items-center justify-center rounded-md",
            library ? "bg-blue-100 text-blue-700 dark:bg-blue-500/10 dark:text-blue-300" : "bg-muted text-muted-foreground",
          )}
        >
          <Icon className="size-[15px]" aria-hidden />
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-64">{text}</TooltipContent>
    </Tooltip>
  );
}

// LibraryBadge marks a library (#1941) on a grid card, whose drawn tile may not
// say so: code other scripts load, never run or scheduled itself.
export function LibraryBadge({ row }: { row: PortalScriptRow }) {
  if (!row.script.library) return null;
  return <Badge variant="info">Library</Badge>;
}

// InertBadge marks a script that will execute nothing — disabled, or retired.
// It is a badge on the name rather than a column of its own (#1407): what a
// reader scans a listing for is the exception.
export function InertBadge({ row }: { row: PortalScriptRow }) {
  const { script } = row;
  if (script.enabled && script.status === "active") return null;
  return <Badge variant="muted">{script.enabled ? script.status : "disabled"}</Badge>;
}

// ScheduleCell reports the cadence and what it is doing, in the words the
// schedule editor states them in (#1358), never as a cron expression (#1405):
// the cadence on the first line, and on the second when it fires next with its
// zone, or that it is paused.
export function ScheduleCell({ row }: { row: PortalScriptRow }) {
  const schedules = row.schedules ?? [];
  const schedule = schedules[0];
  // A library is never scheduled, so "On demand" would promise a run it
  // cannot have.
  if (row.script.library) {
    return <span className="text-xs text-muted-foreground">—</span>;
  }
  if (!schedule) {
    return <span className="text-xs text-muted-foreground">{unscheduledText(row)}</span>;
  }
  const line = scheduleLine(schedule.cron_spec, schedule.timezone);
  const when = scheduleWhen(schedule);
  return (
    <div className="min-w-0 text-xs">
      <div className="truncate" title={line}>
        {line}
        <MoreCount count={schedules.length - 1} />
      </div>
      <div className="truncate text-muted-foreground" title={when.title}>
        {when.text}
      </div>
    </div>
  );
}

// unscheduledText is what starts a script with no schedule (#2435): each
// event of a webhook source, a request to an endpoint or form, or someone
// asking for a run.
export function unscheduledText(row: PortalScriptRow): string {
  const types = row.handler_types ?? [];
  if (types.includes("webhook")) return "Each event";
  if (types.includes("endpoint") || types.includes("form")) return "On request";
  return "On demand";
}

// MoreCount says how many more handlers of the kind a script holds than the
// one shown, nothing for none (#2434).
function MoreCount({ count }: { count: number }) {
  if (count <= 0) return null;
  return <span className="ml-1 text-muted-foreground">+{count} more</span>;
}

// firstEndpoint is the endpoint a row shows; the rest are counted beside it.
export function firstEndpoint(row: PortalScriptRow) {
  return row.endpoints?.[0];
}

// scheduleWhen is the schedule's state in the few words this column has. The
// editor says the same thing at greater length; both read it off scheduleState,
// so neither can call a paused schedule due.
function scheduleWhen(schedule: NonNullable<PortalScriptRow["schedules"]>[number]): { text: string; title?: string } {
  const state = scheduleState(schedule);
  switch (state.kind) {
    case "paused":
      return { text: "Paused" };
    case "idle":
      return { text: "No fire due" };
    case "due": {
      const zone = schedule.timezone.trim() || "UTC";
      return { text: `Next ${nextFireText(state.at)} · ${zone}`, title: exactWhen(state.at) };
    }
  }
}

/**
 * EndpointText is an endpoint's path without the /endpoints/ prefix every one
 * shares, cut from the LEFT when it is still too long, so the end that tells
 * two endpoints apart stays visible; the full address is on hover.
 */
export function EndpointText({ path }: { path: string }) {
  return (
    <span
      dir="rtl"
      className="block min-w-0 truncate text-left font-mono"
      title={endpointURL(path)}
      data-testid="endpoint-path"
    >
      <span dir="ltr">{endpointTail(path)}</span>
    </span>
  );
}

// EndpointCell is the address a script answers HTTP requests at (#2259), and
// Off beside it when its binding is turned off; a dash for a script bound to
// no endpoint, like the other columns.
export function EndpointCell({ row }: { row: PortalScriptRow }) {
  const endpoint = firstEndpoint(row);
  if (!endpoint) return <span className="text-xs text-muted-foreground">—</span>;
  return (
    <div className="flex min-w-0 items-center gap-1.5 text-xs" data-testid="script-endpoint">
      <EndpointText path={endpoint.path} />
      {!endpoint.enabled && <Badge variant="muted">Off</Badge>}
      <MoreCount count={(row.endpoints?.length ?? 0) - 1} />
    </div>
  );
}

// RUN_DOT is a run status as a colored dot, beside the status in words.
const RUN_DOT: Record<ReturnType<typeof runStatusVariant>, string> = {
  success: "bg-green-500",
  danger: "bg-red-500",
  info: "bg-blue-500",
  warning: "bg-amber-500",
  muted: "bg-slate-400 dark:bg-slate-600",
};

/** RunDot is a run's status as a dot, for the listing and the phone list. */
export function RunDot({ status }: { status?: string }) {
  const variant = status ? runStatusVariant(status) : "muted";
  return <span aria-hidden className={cn("inline-block size-2 shrink-0 rounded-full", RUN_DOT[variant])} />;
}

/** runMoment is when a run happened: finished, else started, else its fire. */
export function runMoment(run: NonNullable<PortalScriptRow["last_run"]>): string | undefined {
  return run.finished_at ?? run.started_at ?? run.fire_time;
}

// LastRunCell reports the most recent run, for every row: how a script is
// going is every reader's (#1994), what its run was given and printed is not.
// The status with its dot, then how long ago.
export function LastRunCell({ row }: { row: PortalScriptRow }) {
  // A library never runs, so "Never run" would read as a script waiting for
  // its first run.
  if (row.script.library) {
    return <span className="text-xs text-muted-foreground">—</span>;
  }
  if (!row.last_run) {
    return <span className="text-xs text-muted-foreground">Never run</span>;
  }
  const run = row.last_run;
  const at = runMoment(run);
  const ago = agoText(at);
  return (
    <div className="text-xs" data-testid="script-last-run">
      <div className="flex items-center gap-1.5 whitespace-nowrap">
        <RunDot status={run.status} />
        <span>{runStatusLabel(run.status)}</span>
      </div>
      <div className="text-muted-foreground" title={exactWhen(at)}>
        {run.status === "running" ? `started ${ago}` : ago}
      </div>
    </div>
  );
}
