import { AlertTriangle, FileX2 } from "lucide-react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import type { ScratchTable } from "@/api/tables/types";
import { sourceKindInfo, sourceKindLabel } from "./source";

// ScratchTablesTable is the listing itself: one row per registration, opening
// on row click like every other portal list.
//
// The columns are the questions a reader brings to it -- what do I write in a
// FROM clause, where does it live, which file is behind it, is it still
// reading that file's current contents, and who put it there. The qualified
// name leads because it is the only column a reader retypes.

const COLUMNS = ["Table", "Connection", "Source", "Columns", "Registered", "State"] as const;

export function ScratchTablesTable({
  rows,
  isLoading,
  onOpen,
}: {
  rows?: ScratchTable[];
  isLoading: boolean;
  onOpen: (id: string) => void;
}) {
  return (
    <div className="rounded-lg border bg-card">
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
              <TableCell colSpan={COLUMNS.length} className="py-8 text-center text-muted-foreground">
                Loading&hellip;
              </TableCell>
            </TableRow>
          )}
          {rows?.map((row) => (
            <TableRow
              key={row.id}
              onClick={() => onOpen(row.id)}
              className="cursor-pointer"
              data-testid={`scratch-table-${row.id}`}
            >
              {/* The qualified name wraps rather than truncating: it is what a
                  reader came for and what they type into a query, and half of
                  it is no use to them. It has no spaces, so the break has to
                  be allowed mid-token. */}
              <TableCell className="max-w-[26rem] px-3">
                <span className="block font-mono text-sm break-all">{row.query_table}</span>
              </TableCell>
              <TableCell className="px-3 text-xs">{row.connection}</TableCell>
              <TableCell className="max-w-[16rem] px-3">
                <SourceCell row={row} />
              </TableCell>
              <TableCell className="px-3 text-right text-xs tabular-nums">
                {row.columns.length}
              </TableCell>
              <TableCell className="px-3 text-xs">
                <span className="block truncate" title={row.registered_by}>
                  {row.registered_by}
                </span>
                <span className="block whitespace-nowrap text-muted-foreground">
                  {new Date(row.registered_at).toLocaleDateString()}
                </span>
              </TableCell>
              <TableCell className="px-3">
                <StateBadge row={row} />
              </TableCell>
            </TableRow>
          ))}
          {rows?.length === 0 && (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={COLUMNS.length} className="py-8 text-center text-muted-foreground">
                No table matches these filters.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  );
}

// SourceCell names the file the table reads. It is plain text rather than a
// link: the whole row opens the registration, and a link inside it would put
// two destinations in one click target.
function SourceCell({ row }: { row: ScratchTable }) {
  const Icon = sourceKindInfo(row.source.kind)?.icon;
  const name = row.source.name || row.source.id;
  return (
    <span className="flex items-center gap-1.5 text-xs">
      {Icon && <Icon aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />}
      <span className="truncate" title={`${sourceKindLabel(row.source.kind)}: ${name}`}>
        {row.source.missing ? <span className="text-muted-foreground">Deleted</span> : name}
      </span>
    </span>
  );
}

// StateBadge is the currency verdict, which is the second thing only a
// cross-source read can answer. A current table says nothing at all: the
// column exists to carry the exceptions, and a row of "Current" badges would
// bury them.
function StateBadge({ row }: { row: ScratchTable }) {
  if (row.source.missing) {
    return (
      <Badge variant="danger" className="gap-1 whitespace-nowrap">
        <FileX2 aria-hidden className="size-3" />
        Source deleted
      </Badge>
    );
  }
  if (row.stale) {
    return (
      <Badge variant="warning" className="gap-1 whitespace-nowrap">
        <AlertTriangle aria-hidden className="size-3" />
        Behind the file
      </Badge>
    );
  }
  return <span className="text-xs text-muted-foreground">Current</span>;
}
