import { ChevronLeft, ChevronRight, X } from "lucide-react";
import { ModalShell } from "@/components/ModalShell";
import { CopyButton } from "@/components/provenance/parts";
import { Button } from "@/components/ui/button";
import { cellText, detailText } from "./cellText";

/**
 * One record of a tabular file, read as its fields.
 *
 * A table cell is truncated so the table can be scanned, which leaves no way
 * to read a value longer than the cell. The only thing standing in for that
 * was the browser's own `title` tooltip: it cannot be selected or copied, does
 * not wrap usefully for a paragraph, does not exist on touch, and disappears
 * when the pointer moves. On an export whose Title and Description hold whole
 * paragraphs the table was a wall of ellipses over data that was all there
 * (#1781).
 *
 * The pattern this settles, for the next dense table as much as this one:
 * truncate in the table, open the record in a dialog, and never leave
 * something a person needs to read reachable only through `title`. The table's
 * job is to find a row; this one's is to read it.
 *
 * An ABSENT field is not rendered as an empty value. A CSV record may end
 * before the header does -- an exporter writing nothing rather than trailing
 * commas -- and "the column is not in this record" is a different fact from
 * "the column is here and empty" (#1779). A reader deciding whether a table
 * over this file will serve them a null needs to see which it is.
 */
export function RowDetailDialog({
  columns,
  row,
  position,
  total,
  onPrev,
  onNext,
  onClose,
}: {
  columns: string[];
  row: Record<string, unknown>;
  /** The row's 1-based place among the rows on screen. */
  position: number;
  total: number;
  onPrev?: () => void;
  onNext?: () => void;
  onClose: () => void;
}) {
  const asText = (col: string) => (col in row ? detailText(row[col]) : null);
  // One line per column: detailText indents a nested value over several lines,
  // which would break the copied block's column-per-line shape, so the copy
  // takes the compact form of each value.
  const wholeRow = columns
    .map((col) => `${col}\t${col in row ? cellText(row[col]) : ""}`)
    .join("\n");

  return (
    <ModalShell
      onClose={onClose}
      label="Row detail"
      width="max-w-3xl"
      bodyClass="p-0"
      header={
        <div className="flex items-center justify-between gap-2 border-b p-4">
          <div className="min-w-0">
            <h2 className="text-lg font-semibold">Row {position}</h2>
            <p className="text-xs text-muted-foreground">of {total} shown</p>
          </div>
          <div className="flex shrink-0 items-center gap-1">
            <CopyButton text={wholeRow} label="Copy row" />
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={onPrev}
              disabled={!onPrev}
              aria-label="Previous row"
            >
              <ChevronLeft />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={onNext}
              disabled={!onNext}
              aria-label="Next row"
            >
              <ChevronRight />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={onClose}
              aria-label="Close"
            >
              <X />
            </Button>
          </div>
        </div>
      }
    >
      <dl className="divide-y" data-testid="row-detail-fields">
        {columns.map((col) => {
          const value = asText(col);
          return (
            <div
              key={col}
              className="grid gap-1 p-4 sm:grid-cols-[minmax(0,12rem)_minmax(0,1fr)] sm:gap-4"
            >
              <dt className="min-w-0 text-sm font-medium break-words text-muted-foreground">
                {col}
              </dt>
              <dd className="flex min-w-0 items-start gap-2">
                <span className="min-w-0 flex-1 text-sm break-words whitespace-pre-wrap">
                  {value === null ? (
                    <span className="text-muted-foreground italic">
                      not in this row
                    </span>
                  ) : value === "" ? (
                    <span className="text-muted-foreground italic">empty</span>
                  ) : (
                    value
                  )}
                </span>
                {value ? (
                  <CopyButton text={value} label={`Copy ${col}`} />
                ) : null}
              </dd>
            </div>
          );
        })}
      </dl>
    </ModalShell>
  );
}
