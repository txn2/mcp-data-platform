import type { PortalScriptRow } from "@/api/portal/hooks/scripts";
import type { ListSort, ScriptSortKey } from "@/components/listSort";
import { OpenRowHead } from "@/components/patterns/OpenRowChevron";
import { SortableHead } from "@/components/patterns/SortableHead";
import { Table, TableBody, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ScriptPhoneList } from "./ScriptPhoneList";
import { ScriptRow } from "./ScriptRow";
import { useListingLayout, type ListingLayout } from "./useListingLayout";

// COLUMN_WIDTHS are the fixed columns' widths per table layout (#2601); the
// Automation column takes what is left. table-layout: fixed is what holds them:
// with automatic layout the widest content took the width, so a library row
// pushed the name to four lines while blank Schedule and Endpoint cells got the
// widest columns on the page.
const COLUMN_WIDTHS: Record<Exclude<ListingLayout, "list">, { author: string; schedule: string; lastRun: string }> = {
  wide: { author: "w-[190px]", schedule: "w-[240px]", lastRun: "w-[140px]" },
  medium: { author: "w-[170px]", schedule: "w-[210px]", lastRun: "w-[116px]" },
  narrow: { author: "w-[150px]", schedule: "w-[190px]", lastRun: "w-[108px]" },
};

/**
 * ScriptTable is the listing as a table, at the layout its width fits: every
 * column wide, Endpoint folded under the name in the middle, Updated dropped
 * when narrow, and a list on a phone (useListingLayout).
 */
export function ScriptTable({
  rows,
  basePath,
  sort,
  onSort,
  onNavigate,
}: {
  rows: PortalScriptRow[];
  basePath: string;
  sort: ListSort<ScriptSortKey>;
  onSort: (key: ScriptSortKey) => void;
  onNavigate: (path: string) => void;
}) {
  const { ref, layout } = useListingLayout();
  if (layout === "list") {
    return (
      <div ref={ref}>
        <ScriptPhoneList rows={rows} basePath={basePath} onNavigate={onNavigate} />
      </div>
    );
  }
  const widths = COLUMN_WIDTHS[layout];
  const columns = { endpoint: layout === "wide", updated: layout !== "narrow" };
  return (
    <div ref={ref} data-testid="script-table" data-layout={layout}>
      <TooltipProvider delayDuration={200}>
        <Table className="table-fixed">
          <TableHeader>
            <TableRow>
              {/* What kind each row is (#1912, #1941), as an icon whose tooltip
                  says what the kind is (#2601). */}
              <TableHead className="w-[34px] pr-0 pl-3">
                <span className="sr-only">Kind</span>
              </TableHead>
              <SortableHead
                label="Automation"
                sortKey="display_name"
                sortBy={sort.key}
                sortDir={sort.dir}
                onSort={onSort}
                className="pl-2"
              />
              <SortableHead
                label="Author"
                sortKey="owner_email"
                sortBy={sort.key}
                sortDir={sort.dir}
                onSort={onSort}
                className={widths.author}
              />
              <TableHead className={widths.schedule}>Schedule</TableHead>
              {/* The endpoint a script answers HTTP requests at (#2259). */}
              {columns.endpoint && <TableHead className="w-[180px]">Endpoint</TableHead>}
              {/* Last run carries no sort affordance: it is attached to this page
                  after the query, so an ordering over it would order the page. */}
              <TableHead className={widths.lastRun}>Last run</TableHead>
              {columns.updated && (
                <SortableHead
                  label="Updated"
                  sortKey="updated_at"
                  sortBy={sort.key}
                  sortDir={sort.dir}
                  onSort={onSort}
                  className="w-[92px]"
                />
              )}
              <OpenRowHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => (
              <ScriptRow key={row.script.id} row={row} basePath={basePath} onNavigate={onNavigate} columns={columns} />
            ))}
          </TableBody>
        </Table>
      </TooltipProvider>
    </div>
  );
}
