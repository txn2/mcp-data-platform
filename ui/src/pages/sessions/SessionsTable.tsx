import { StatusBadge } from "@/components/cards/StatusBadge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { SessionSummary } from "@/api/admin/types";
import { formatUser } from "@/lib/formatUser";
import { kindDescription, kindLabel, shortSessionId } from "./kind";

// SessionsTable is the session list itself: one row per session, opening on
// row click like every other portal list. The columns answer the questions an
// operator brings to the list — whose session, of what kind, how long, how
// much, did anything fail, did it leave anything behind.
//
// The caller's own list drops the User column: every row would carry the
// reader's own name, which is a column that answers a question nobody reading
// their own sessions is asking. Persona stays on both, because a user does
// change which persona they work as.

const USER_COLUMN = "User";

const COLUMNS = [
  "Last active",
  "Session",
  USER_COLUMN,
  "Persona",
  "Calls",
  "Failed",
  "Produced",
] as const;

export function SessionsTable({
  sessions,
  isLoading,
  onOpen,
  showUser = true,
}: {
  sessions?: SessionSummary[];
  isLoading: boolean;
  onOpen: (sessionId: string) => void;
  /** Whether to carry the caller column. False on a reader's own sessions. */
  showUser?: boolean;
}) {
  const columns = showUser
    ? COLUMNS
    : COLUMNS.filter((label) => label !== USER_COLUMN);

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
          {sessions?.map((session) => (
            <TableRow
              key={session.session_id}
              onClick={() => onOpen(session.session_id)}
              className="cursor-pointer"
            >
              <TableCell className="px-3 text-xs">
                {new Date(session.last_active_at).toLocaleString()}
              </TableCell>
              <TableCell className="px-3">
                <span
                  className="font-mono text-xs"
                  title={session.session_id}
                >
                  {shortSessionId(session.session_id)}
                </span>
                <span
                  className="ml-2 text-xs text-muted-foreground"
                  title={kindDescription(session.kind)}
                >
                  {kindLabel(session.kind)}
                </span>
              </TableCell>
              {showUser && (
                <TableCell
                  className="max-w-[12rem] truncate px-3"
                  title={session.user_id}
                >
                  {formatUser(session.user_id, session.user_email)}
                </TableCell>
              )}
              <TableCell className="px-3 text-xs">
                {session.persona || "-"}
              </TableCell>
              <TableCell className="px-3 text-right">
                {session.call_count}
              </TableCell>
              <TableCell className="px-3 text-center">
                {session.failure_count > 0 ? (
                  <StatusBadge variant="error">
                    {session.failure_count}
                  </StatusBadge>
                ) : (
                  <span className="text-muted-foreground">-</span>
                )}
              </TableCell>
              <TableCell className="px-3 text-xs text-muted-foreground">
                {producedLabel(session)}
              </TableCell>
            </TableRow>
          ))}
          {sessions?.length === 0 && (
            <TableRow className="hover:bg-transparent">
              <TableCell
                colSpan={columns.length}
                className="py-8 text-center text-muted-foreground"
              >
                No sessions found
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  );
}

/** Summarizes what a session left behind, or a dash when it left nothing. */
function producedLabel(session: SessionSummary): string {
  const parts: string[] = [];
  if (session.asset_count > 0) {
    parts.push(`${session.asset_count} asset${session.asset_count === 1 ? "" : "s"}`);
  }
  if (session.insight_count > 0) {
    parts.push(
      `${session.insight_count} insight${session.insight_count === 1 ? "" : "s"}`,
    );
  }
  return parts.length > 0 ? parts.join(", ") : "-";
}
