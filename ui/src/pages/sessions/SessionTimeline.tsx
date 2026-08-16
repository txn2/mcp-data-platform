import { StatusBadge } from "@/components/cards/StatusBadge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { SessionTimelineEntry } from "@/api/admin/types";
import { formatDuration } from "@/lib/formatDuration";
import { formatToolName } from "@/lib/formatToolName";

// SessionTimeline is the session read in order: what the agent said it was
// doing, what it called, where, and how that turned out. Purpose leads the
// tool name because the reason is the thing the event list could never show.
//
// A row opens the full audit event where the reader can read one — the admin
// event drawer. On the user's own session there is no such surface, so onSelect
// is omitted and the rows are plain: a row that looks clickable and does
// nothing is worse than one that never invited the click.

const COLUMNS = ["Time", "Tool", "Purpose", "Connection", "Outcome", "Duration"];

export function SessionTimeline({
  entries,
  isLoading,
  onSelect,
  titleMap,
}: {
  entries?: SessionTimelineEntry[];
  isLoading: boolean;
  onSelect?: (eventId: string) => void;
  titleMap: Record<string, string>;
}) {
  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow className="bg-muted/50 hover:bg-muted/50">
            {COLUMNS.map((label) => (
              <TableHead key={label} className="px-3">
                {label}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading && (
            <TableRow className="hover:bg-transparent">
              <TableCell
                colSpan={COLUMNS.length}
                className="py-8 text-center text-muted-foreground"
              >
                Loading...
              </TableCell>
            </TableRow>
          )}
          {entries?.map((entry) => (
            <TableRow
              key={entry.event_id}
              onClick={onSelect ? () => onSelect(entry.event_id) : undefined}
              className={onSelect ? "cursor-pointer" : "hover:bg-transparent"}
            >
              {/* The full date, not just the clock: a session can span days,
                  and a bare time reads as out of order when it does. */}
              <TableCell className="px-3 text-xs whitespace-nowrap">
                {new Date(entry.timestamp).toLocaleString()}
              </TableCell>
              <TableCell className="px-3 text-xs" title={entry.tool_name}>
                {formatToolName(entry.tool_name, titleMap[entry.tool_name])}
              </TableCell>
              <TableCell
                className="max-w-[24rem] truncate px-3 text-xs text-muted-foreground"
                title={entry.purpose}
              >
                {entry.purpose || "-"}
              </TableCell>
              <TableCell className="max-w-[9rem] truncate px-3 text-xs">
                {entry.connection || "-"}
              </TableCell>
              <TableCell className="px-3 text-center">
                <StatusBadge variant={entry.success ? "success" : "error"}>
                  {entry.success ? "OK" : "ERR"}
                </StatusBadge>
              </TableCell>
              <TableCell className="px-3 text-right text-xs">
                {formatDuration(entry.duration_ms)}
              </TableCell>
            </TableRow>
          ))}
          {entries?.length === 0 && (
            <TableRow className="hover:bg-transparent">
              <TableCell
                colSpan={COLUMNS.length}
                className="py-8 text-center text-muted-foreground"
              >
                No calls on this page
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  );
}
