import { useMemo, useState } from "react";
import type { WebhookRejection } from "@/api/admin/types";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { outcomeLabel } from "./webhookOverview";

// The rejected-requests table the Webhooks page and a source's page share
// (#2001). Each row is a run of rejections with one outcome and reason, kept
// per outcome so a burst of one cannot push another out; the Outcome filter
// narrows the list to one, in the words the Outcome column uses.

/** ALL is the filter value that shows every outcome. */
const ALL = "all";

type Row = WebhookRejection & { source?: string };

/** rejectionTimes is a row's time as the When column shows it, and the span
 * the Count cell's tooltip names for a run of more than one. */
export function rejectionTimes(r: WebhookRejection): { when: string; span?: string } {
  const when = new Date(r.at).toLocaleString();
  if (r.count <= 1 || !r.first_at || r.first_at === r.at) return { when };
  return { when, span: `${new Date(r.first_at).toLocaleString()} to ${when}` };
}

/** outcomesOf are the outcomes present, in the order they first appear. */
export function outcomesOf(rows: WebhookRejection[]): string[] {
  return [...new Set(rows.map((r) => r.outcome))];
}

export function RejectionsTable<T extends Row>({
  rows,
  showSource = false,
  onOpen,
}: {
  rows: T[];
  showSource?: boolean;
  onOpen?: (row: T) => void;
}) {
  const [outcome, setOutcome] = useState(ALL);
  const outcomes = useMemo(() => outcomesOf(rows), [rows]);
  const shown = outcome === ALL ? rows : rows.filter((r) => r.outcome === outcome);
  return (
    <div className="space-y-2">
      {outcomes.length > 1 && (
        <div className="flex items-center gap-2">
          <span className="text-xs text-muted-foreground">Outcome</span>
          <Select value={outcome} onValueChange={setOutcome}>
            <SelectTrigger className="h-8 w-44 text-xs" aria-label="Outcome">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>All</SelectItem>
              {outcomes.map((o) => (
                <SelectItem key={o} value={o}>
                  {outcomeLabel(o)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              {showSource && <TableHead>Source</TableHead>}
              <TableHead>When</TableHead>
              <TableHead>Outcome</TableHead>
              <TableHead>Reason</TableHead>
              <TableHead className="text-right">Count</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {shown.map((r, i) => {
              const t = rejectionTimes(r);
              return (
                <TableRow
                  key={`${r.source ?? ""}-${r.at}-${r.outcome}-${i}`}
                  className={onOpen ? "cursor-pointer" : undefined}
                  onClick={onOpen ? () => onOpen(r) : undefined}
                >
                  {showSource && <TableCell className="text-xs font-medium">{r.source}</TableCell>}
                  <TableCell className="whitespace-nowrap text-xs">{t.when}</TableCell>
                  <TableCell className="text-xs">{outcomeLabel(r.outcome)}</TableCell>
                  <TableCell className="text-xs">{r.reason}</TableCell>
                  <TableCell className="text-right text-xs tabular-nums" title={t.span}>
                    {(r.count ?? 1).toLocaleString()}
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
