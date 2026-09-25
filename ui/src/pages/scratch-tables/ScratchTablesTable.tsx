import { AlertTriangle, FileX2, Pin } from "lucide-react";
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
// FROM clause, where does it live, which file is behind it, and where did it
// come from. The qualified name leads because it is the only column a reader
// retypes.
//
// Provenance is one column, not two (#1796). Registered and State used to sit
// side by side and spend a third of the width saying the same two things on
// every row: an email over a date, and "Follows the file", which is what a
// registration says unless something is wrong with it. Together they are one
// cell that reads as a sentence, and the verdicts worth noticing -- behind the
// file, source deleted, pinned -- are the only loud thing in it.

const COLUMNS = ["Table", "Connection", "Source", "Columns", "Registered"] as const;

// The widths the table is laid out with. They are declared here, on a
// `table-fixed` layout, because a `max-w` utility on a cell does nothing under
// `table-layout: auto`: the algorithm sizes a column to its content and a long
// `catalog.schema.table` painted straight over the two columns beside it
// (#1796). Fixed layout is also what makes `break-all` and `truncate` mean
// anything here -- both need a cell that was actually narrowed.
const WIDTHS = ["34%", "13%", "23%", "8%", "22%"] as const;

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
      <Table className="table-fixed">
        <colgroup>
          {WIDTHS.map((width, i) => (
            <col key={COLUMNS[i]} style={{ width }} />
          ))}
        </colgroup>
        <TableHeader>
          <TableRow className="bg-muted/50 hover:bg-muted/50">
            {COLUMNS.map((label) => (
              <TableHead
                key={label}
                className={label === "Columns" ? "px-3 text-right" : "px-3"}
              >
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
              className="cursor-pointer align-top"
              data-testid={`scratch-table-${row.id}`}
            >
              {/* The qualified name wraps rather than truncating: it is what a
                  reader came for and what they type into a query, and half of
                  it is no use to them. It has no spaces, so the break has to
                  be allowed mid-token.
                  `whitespace-normal` is load-bearing: TableCell ships
                  `whitespace-nowrap`, which is the other half of why the old
                  cell overflowed -- a narrowed cell still cannot wrap while
                  its own class forbids wrapping. */}
              <TableCell className="px-3 whitespace-normal">
                <span className="block font-mono text-sm break-all">{row.query_table}</span>
              </TableCell>
              <TableCell className="px-3 text-xs">
                <span className="block truncate" title={row.connection}>
                  {row.connection}
                </span>
              </TableCell>
              <TableCell className="px-3">
                <SourceCell row={row} />
              </TableCell>
              <TableCell className="px-3 text-right text-xs tabular-nums">
                {row.columns.length}
              </TableCell>
              <TableCell className="px-3 text-xs">
                <ProvenanceCell row={row} />
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

// ProvenanceCell says who registered the table, when, and whether it is still
// reading its file's current contents.
//
// The currency verdict sits under the attribution rather than in a column of
// its own because on nearly every row it is "Follows the file" -- true, and
// not why anyone opened this page. Rendered quietly it leaves the exceptions
// as the only badges in the column, which is what a reader is scanning for.
function ProvenanceCell({ row }: { row: ScratchTable }) {
  return (
    <div className="space-y-0.5">
      <span className="block truncate" title={row.registered_by}>
        {row.registered_by}
      </span>
      <span className="block whitespace-nowrap text-muted-foreground">
        {new Date(row.registered_at).toLocaleDateString()}
      </span>
      <StateLine row={row} />
    </div>
  );
}

// StateLine is the currency verdict. A table that follows its file is current
// by construction and says so quietly; a pinned one is current only until the
// file moves, and says it is pinned so a reader knows the next version will
// not reach it. The two that need acting on are badges.
function StateLine({ row }: { row: ScratchTable }) {
  if (row.source.missing) {
    return (
      <Badge variant="danger" className="mt-0.5 gap-1 whitespace-nowrap">
        <FileX2 aria-hidden className="size-3" />
        Source deleted
      </Badge>
    );
  }
  if (row.stale) {
    return (
      <Badge
        variant="warning"
        className="mt-0.5 gap-1 whitespace-nowrap"
        title={row.follow && row.follow_error ? row.follow_error : undefined}
      >
        <AlertTriangle aria-hidden className="size-3" />
        Behind the file
      </Badge>
    );
  }
  if (row.source.kind === "webhook") {
    // A webhook source's table is over every window the source has received,
    // not over one file, so it neither follows nor is pinned (#1870).
    return <span className="block text-muted-foreground">Webhook source</span>;
  }
  if (row.follow) {
    return <span className="block text-muted-foreground">Follows the file</span>;
  }
  return (
    <span className="flex items-center gap-1 text-muted-foreground">
      <Pin aria-hidden className="size-3" />
      Pinned
    </span>
  );
}
