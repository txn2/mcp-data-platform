import type { Insight } from "@/api/admin/types";
import { KnowledgeStatusBadge } from "@/components/knowledge/KnowledgeStatusBadge";
import { StatusBadge } from "@/components/cards/StatusBadge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatUser } from "@/lib/formatUser";
import {
  ageBucketVariant,
  ageInDays,
  confidenceVariant,
  formatAge,
  formatCategory,
} from "./helpers";

// InsightsTable is the reviewer's queue: one row per captured insight, opening
// its detail. Age is a badge only while an insight is pending — once it has been
// reviewed, how long it waited is history, not a call to act.
export function InsightsTable({
  insights,
  loading,
  userLabels,
  onSelect,
}: {
  insights: Insight[] | undefined;
  loading: boolean;
  userLabels: Record<string, string>;
  onSelect: (insight: Insight) => void;
}) {
  return (
    <div className="rounded-lg border bg-card">
      <Table>
        <TableHeader>
          <TableRow className="bg-muted/50">
            <TableHead>Created At</TableHead>
            <TableHead className="text-center">Age</TableHead>
            <TableHead>Captured By</TableHead>
            <TableHead>Category</TableHead>
            <TableHead className="text-center">Confidence</TableHead>
            <TableHead>Insight</TableHead>
            <TableHead className="text-center">Status</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {loading && (
            <TableRow>
              <TableCell colSpan={7} className="py-8 text-center text-muted-foreground">
                Loading...
              </TableCell>
            </TableRow>
          )}
          {insights?.map((insight) => (
            <TableRow
              key={insight.id}
              onClick={() => onSelect(insight)}
              className="cursor-pointer"
            >
              {/* Prose and timestamps opt out of the table's nowrap so the
                  trailing Status column cannot slide behind a scrollbar. */}
              <TableCell className="whitespace-normal text-xs">
                {new Date(insight.created_at).toLocaleString()}
              </TableCell>
              <TableCell className="text-center">
                <AgeCell createdAt={insight.created_at} pending={insight.status === "pending"} />
              </TableCell>
              <TableCell className="whitespace-normal text-xs" title={insight.captured_by}>
                {formatUser(insight.captured_by, userLabels[insight.captured_by])}
              </TableCell>
              <TableCell className="text-xs">{formatCategory(insight.category)}</TableCell>
              <TableCell className="text-center">
                <StatusBadge variant={confidenceVariant(insight.confidence)}>
                  {insight.confidence}
                </StatusBadge>
              </TableCell>
              <TableCell className="max-w-xs truncate text-xs">
                {insight.insight_text}
              </TableCell>
              <TableCell className="text-center">
                <KnowledgeStatusBadge status={insight.status} />
              </TableCell>
            </TableRow>
          ))}
          {insights?.length === 0 && (
            <TableRow>
              <TableCell colSpan={7} className="py-8 text-center text-muted-foreground">
                No insights found
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  );
}

function AgeCell({ createdAt, pending }: { createdAt: string; pending: boolean }) {
  const days = ageInDays(createdAt);
  if (!pending) {
    return <span className="text-xs text-muted-foreground">{formatAge(days)}</span>;
  }
  return <StatusBadge variant={ageBucketVariant(days)}>{formatAge(days)}</StatusBadge>;
}
