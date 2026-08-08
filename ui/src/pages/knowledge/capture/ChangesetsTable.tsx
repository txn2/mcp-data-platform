import type { Changeset } from "@/api/admin/types";
import { KnowledgeStatusBadge } from "@/components/knowledge/KnowledgeStatusBadge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatUser } from "@/lib/formatUser";
import { formatCategory } from "./helpers";

// changesetStatus maps the stored flag onto the shared knowledge lifecycle
// vocabulary, so a rolled-back changeset is the same red as a rolled-back
// insight rather than a colour of its own.
export function changesetStatus(rolledBack: boolean): string {
  return rolledBack ? "rolled_back" : "active";
}

// ChangesetsTable is the apply audit: one row per write apply_knowledge made,
// opening the detail that can undo it.
export function ChangesetsTable({
  changesets,
  loading,
  userLabels,
  onSelect,
}: {
  changesets: Changeset[] | undefined;
  loading: boolean;
  userLabels: Record<string, string>;
  onSelect: (changeset: Changeset) => void;
}) {
  return (
    <div className="rounded-lg border bg-card">
      <Table>
        <TableHeader>
          <TableRow className="bg-muted/50">
            <TableHead>Created At</TableHead>
            <TableHead>Target URN</TableHead>
            <TableHead>Change Type</TableHead>
            <TableHead>Applied By</TableHead>
            <TableHead className="text-center">Status</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {loading && (
            <TableRow>
              <TableCell colSpan={5} className="py-8 text-center text-muted-foreground">
                Loading...
              </TableCell>
            </TableRow>
          )}
          {changesets?.map((changeset) => (
            <TableRow
              key={changeset.id}
              onClick={() => onSelect(changeset)}
              className="cursor-pointer"
            >
              {/* Prose and timestamps opt out of the table's nowrap so the
                  trailing Status column cannot slide behind a scrollbar. */}
              <TableCell className="whitespace-normal text-xs">
                {new Date(changeset.created_at).toLocaleString()}
              </TableCell>
              <TableCell className="max-w-xs truncate font-mono text-xs">
                {changeset.target_urn}
              </TableCell>
              <TableCell className="text-xs">
                {formatCategory(changeset.change_type)}
              </TableCell>
              <TableCell className="whitespace-normal text-xs" title={changeset.applied_by}>
                {formatUser(changeset.applied_by, userLabels[changeset.applied_by])}
              </TableCell>
              <TableCell className="text-center">
                <KnowledgeStatusBadge status={changesetStatus(changeset.rolled_back)} />
              </TableCell>
            </TableRow>
          ))}
          {changesets?.length === 0 && (
            <TableRow>
              <TableCell colSpan={5} className="py-8 text-center text-muted-foreground">
                No changesets found
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  );
}
