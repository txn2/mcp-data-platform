import type { Insight } from "@/api/admin/types";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatUser } from "@/lib/formatUser";
import { LabeledBlock, MetaField, MetaGrid } from "./fields";

// InsightTables shows what the capture proposed and what it was about: the
// suggested catalog actions, and the columns it named. Both are absent on most
// insights, so each renders only when it has rows.
export function InsightTables({ insight }: { insight: Insight }) {
  return (
    <>
      {insight.suggested_actions.length > 0 && (
        <LabeledBlock label="Suggested Actions">
          <div className="rounded border">
            <Table className="text-xs">
              <TableHeader>
                <TableRow className="bg-muted/50">
                  <TableHead className="h-8">Type</TableHead>
                  <TableHead className="h-8">Target</TableHead>
                  <TableHead className="h-8">Detail</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {insight.suggested_actions.map((a, i) => (
                  <TableRow key={i}>
                    <TableCell className="font-mono">{a.action_type}</TableCell>
                    <TableCell className="max-w-[120px] truncate font-mono">
                      {a.target}
                    </TableCell>
                    <TableCell className="whitespace-normal">{a.detail}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </LabeledBlock>
      )}

      {insight.related_columns.length > 0 && (
        <LabeledBlock label="Related Columns">
          <div className="rounded border">
            <Table className="text-xs">
              <TableHeader>
                <TableRow className="bg-muted/50">
                  <TableHead className="h-8">URN</TableHead>
                  <TableHead className="h-8">Column</TableHead>
                  <TableHead className="h-8">Relevance</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {insight.related_columns.map((c, i) => (
                  <TableRow key={i}>
                    <TableCell className="max-w-[120px] truncate font-mono">
                      {c.urn}
                    </TableCell>
                    <TableCell className="font-mono">{c.column}</TableCell>
                    <TableCell className="whitespace-normal">{c.relevance}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </LabeledBlock>
      )}
    </>
  );
}

// InsightLifecycle records what has happened to the insight since capture: who
// reviewed it, who applied it, and the changeset that write produced. Each block
// appears only once that step has happened.
export function InsightLifecycle({
  insight,
  userLabels,
}: {
  insight: Insight;
  userLabels: Record<string, string>;
}) {
  const at = (iso: string | undefined) => (iso ? new Date(iso).toLocaleString() : "-");
  return (
    <>
      {insight.reviewed_by && (
        <MetaGrid className="border-t pt-3">
          <MetaField label="Reviewed By" title={insight.reviewed_by}>
            {formatUser(insight.reviewed_by, userLabels[insight.reviewed_by])}
          </MetaField>
          <MetaField label="Reviewed At">{at(insight.reviewed_at)}</MetaField>
        </MetaGrid>
      )}

      {insight.applied_by && (
        <MetaGrid className="border-t pt-3">
          <MetaField label="Applied By" title={insight.applied_by}>
            {formatUser(insight.applied_by, userLabels[insight.applied_by])}
          </MetaField>
          <MetaField label="Applied At">{at(insight.applied_at)}</MetaField>
          {insight.changeset_ref && (
            <MetaField label="Changeset Ref" mono>
              {insight.changeset_ref}
            </MetaField>
          )}
        </MetaGrid>
      )}
    </>
  );
}
