import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { CallRecord } from "@/api/admin/types";
import { PrincipalLabel } from "@/components/PrincipalLabel";
import { callSummary, OutcomeBadge } from "./outcome";

// CallsTable is the catalog itself: one row per recorded call, opening on row
// click like every other portal list.
//
// The columns are the questions a reader brings to it — what was it for, what
// ran, against what, how did it end, has anyone else re-run it. The purpose
// leads because it is the only column written by a person; the statement is
// under it, truncated, because a reader scanning for the right query
// recognizes it by shape long before they read it in full.
//
// The caller's own list drops the User column: every row would carry the
// reader's own name.

const USER_COLUMN = "User";

const COLUMNS = ["When", "Purpose", USER_COLUMN, "Connection", "Outcome", "Reuse"] as const;

export function CallsTable({
  records,
  isLoading,
  onOpen,
  showUser = true,
}: {
  records?: CallRecord[];
  isLoading: boolean;
  onOpen: (id: string) => void;
  /** Whether to carry the caller column. False on a reader's own calls. */
  showUser?: boolean;
}) {
  const columns = showUser ? COLUMNS : COLUMNS.filter((label) => label !== USER_COLUMN);

  return (
    <div className="rounded-lg border bg-card">
      <Table>
        <TableHeader>
          <TableRow className="bg-muted/50 hover:bg-muted/50">
            {columns.map((label) => (
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
                colSpan={columns.length}
                className="py-8 text-center text-muted-foreground"
              >
                Loading...
              </TableCell>
            </TableRow>
          )}
          {records?.map((record) => (
            <TableRow
              key={record.id}
              onClick={() => onOpen(record.id)}
              className="cursor-pointer"
            >
              <TableCell className="px-3 text-xs whitespace-nowrap">
                {new Date(record.created_at).toLocaleString()}
              </TableCell>
              <TableCell className="max-w-[26rem] px-3">
                <div className="truncate" title={record.purpose}>
                  {record.purpose || (
                    <span className="text-muted-foreground">No purpose stated</span>
                  )}
                </div>
                <div
                  className="truncate font-mono text-xs text-muted-foreground"
                  title={callSummary(record)}
                >
                  {callSummary(record)}
                </div>
              </TableCell>
              {showUser && (
                <TableCell className="max-w-[12rem] px-3">
                  <PrincipalLabel userId={record.user_id ?? ""} email={record.user_email} />
                </TableCell>
              )}
              <TableCell className="px-3 text-xs">
                {record.connection || "-"}
                <span className="ml-2 text-muted-foreground uppercase">{record.kind}</span>
              </TableCell>
              <TableCell className="px-3">
                <OutcomeBadge outcome={record.outcome} />
              </TableCell>
              <TableCell className="px-3 text-right text-xs" title="Later sessions that read this record and then ran what it holds">
                {record.reuse_count > 0 ? record.reuse_count : "-"}
              </TableCell>
            </TableRow>
          ))}
          {records?.length === 0 && (
            <TableRow className="hover:bg-transparent">
              <TableCell
                colSpan={columns.length}
                className="py-8 text-center text-muted-foreground"
              >
                No calls found
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  );
}
