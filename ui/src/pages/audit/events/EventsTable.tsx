import { SortableHead } from "@/components/patterns/SortableHead";
import { StatusBadge } from "@/components/cards/StatusBadge";
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { AuditEvent, AuditSortColumn, SortOrder } from "@/api/admin/types";
import { formatDuration } from "@/lib/formatDuration";
import { formatToolName } from "@/lib/formatToolName";
import { formatUser } from "@/lib/formatUser";
import { COLUMNS, sourceLabel } from "./columns";

// EventsTable is the audit log itself: one row per tool call, sortable on
// every column the API sorts on, each row opening the event drawer.
// Extracted from EventsTab.tsx (#1207).
export function EventsTable({
  events,
  isLoading,
  sortBy,
  sortOrder,
  onSort,
  onSelect,
  titleMap,
}: {
  events?: AuditEvent[];
  isLoading: boolean;
  sortBy: AuditSortColumn;
  sortOrder: SortOrder;
  onSort: (column: AuditSortColumn) => void;
  onSelect: (event: AuditEvent) => void;
  titleMap: Record<string, string>;
}) {
  return (
    <div className="rounded-lg border bg-card">
      <Table>
        <TableHeader>
          <TableRow className="bg-muted/50 hover:bg-muted/50">
            {COLUMNS.map((col) => (
              <SortableHead
                key={col.key}
                label={col.label}
                sortKey={col.key}
                sortBy={sortBy}
                sortDir={sortOrder}
                onSort={onSort}
                className={col.className}
              />
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading && (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={COLUMNS.length} className="py-8 text-center text-muted-foreground">
                Loading...
              </TableCell>
            </TableRow>
          )}
          {events?.map((event) => (
            <TableRow
              key={event.id}
              onClick={() => onSelect(event)}
              className="cursor-pointer"
            >
              <TableCell className="px-3 text-xs">
                {new Date(event.timestamp).toLocaleString()}
              </TableCell>
              <TableCell className="px-3" title={event.user_id}>
                {formatUser(event.user_id, event.user_email)}
              </TableCell>
              <TableCell className="px-3 text-xs" title={event.tool_name}>
                {formatToolName(event.tool_name, titleMap[event.tool_name])}
              </TableCell>
              <TableCell className="px-3">{event.toolkit_kind}</TableCell>
              <TableCell className="px-3 text-xs" title={sourceLabel(event.source)}>
                {event.source || "-"}
              </TableCell>
              <TableCell className="px-3 text-xs">{event.connection}</TableCell>
              <TableCell className="px-3 text-right">{formatDuration(event.duration_ms)}</TableCell>
              <TableCell className="px-3 text-center">
                <StatusBadge variant={event.success ? "success" : "error"}>
                  {event.success ? "OK" : "ERR"}
                </StatusBadge>
              </TableCell>
              <TableCell className="px-3 text-center">
                {event.enrichment_applied ? (
                  <StatusBadge variant="success">Yes</StatusBadge>
                ) : (
                  <StatusBadge variant="neutral">No</StatusBadge>
                )}
              </TableCell>
            </TableRow>
          ))}
          {events?.length === 0 && (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={COLUMNS.length} className="py-8 text-center text-muted-foreground">
                No events found
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  );
}
