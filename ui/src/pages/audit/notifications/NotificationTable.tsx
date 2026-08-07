import type { NotificationRow, NotificationStatus } from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

// STATUS_VARIANT tints a queue row's delivery state: a bounced send is an
// error, an in-flight one a warning, a queued one merely neutral.
export const STATUS_VARIANT: Record<NotificationStatus, "success" | "error" | "warning" | "neutral"> =
  {
    sent: "success",
    failed: "error",
    sending: "warning",
    pending: "neutral",
  };

// NotificationTable lists the recent queue rows, each opening its own detail.
// Extracted from NotificationsTab.tsx (#1207).
export function NotificationTable({
  isLoading,
  rows,
  onSelect,
}: {
  isLoading: boolean;
  rows?: NotificationRow[];
  onSelect: (row: NotificationRow) => void;
}) {
  return (
    <div className="rounded-lg border bg-card">
      <Table>
        <TableHeader>
          <TableRow className="bg-muted/50 hover:bg-muted/50">
            <TableHead className="px-3">Queued</TableHead>
            <TableHead className="px-3">Recipient</TableHead>
            <TableHead className="px-3">Subject</TableHead>
            <TableHead className="px-3">Category</TableHead>
            <TableHead className="px-3 text-center">Status</TableHead>
            <TableHead className="px-3 text-right">Attempts</TableHead>
            <TableHead className="px-3">Sent</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading && (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={7} className="py-8 text-center text-muted-foreground">
                Loading...
              </TableCell>
            </TableRow>
          )}
          {rows?.map((row) => (
            <TableRow key={row.id} onClick={() => onSelect(row)} className="cursor-pointer">
              {/* ui/table sets whitespace-nowrap on every cell; with seven
                  columns the two timestamps have to wrap or the trailing Sent
                  column is pushed behind a horizontal scroll and lost. */}
              <TableCell className="whitespace-normal px-3 text-xs">
                {new Date(row.created_at).toLocaleString()}
              </TableCell>
              <TableCell className="px-3">{row.recipient}</TableCell>
              <TableCell className="max-w-md truncate px-3 text-xs" title={row.subject}>
                {row.subject}
              </TableCell>
              <TableCell className="px-3 text-xs">
                {row.category}
                {row.digest && <span className="ml-1 text-muted-foreground">(digest)</span>}
              </TableCell>
              <TableCell className="px-3 text-center">
                <StatusBadge variant={STATUS_VARIANT[row.status] ?? "neutral"}>
                  {row.status}
                </StatusBadge>
              </TableCell>
              <TableCell className="px-3 text-right tabular-nums">{row.attempts}</TableCell>
              <TableCell className="whitespace-normal px-3 text-xs">
                {row.sent_at ? new Date(row.sent_at).toLocaleString() : "-"}
              </TableCell>
            </TableRow>
          ))}
          {rows?.length === 0 && (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={7} className="py-8 text-center text-muted-foreground">
                No notifications match these filters.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  );
}
