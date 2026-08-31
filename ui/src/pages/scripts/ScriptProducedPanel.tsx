import { FileText, Paperclip } from "lucide-react";
import { useScriptProduced, type ProducedItem } from "@/api/portal/hooks/producers";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatWhen } from "./runFormat";

// ScriptProducedPanel lists everything this script has ever written (#1569):
// every portal asset and managed resource it created or modified, across every
// run, most recently written first.
//
// The run history above it answers a different question -- what did THIS run
// do -- and stays as it is. Neither the history nor the run rows in it can
// answer this one: a script that has run three hundred times has three hundred
// output lists, a file the script modified without declaring it as an output
// appears in none of them, and "what does this script touch" and "what goes
// stale if I retire it" are the questions somebody actually has.
export function ScriptProducedPanel({
  scriptId,
  filePath,
  onNavigate,
}: {
  scriptId: string;
  /** Where one produced file opens for this reader. The portal and the admin
   * console hold the same file at different addresses, so the surface supplies
   * this; absent, a row names the file without linking to it. */
  filePath?: (kind: ProducedItem["target_kind"], id: string) => string;
  onNavigate?: (path: string) => void;
}) {
  const { data, isLoading, error } = useScriptProduced(scriptId);
  const items = data?.data ?? [];

  return (
    // "Files written" rather than "Produced": the run history directly above
    // already has a Produced column, which says what ONE run produced. Two
    // headings reading the same word on one page, meaning one run and every
    // run, is a reader's problem the label can solve.
    <SectionCard data-testid="script-produced" title={`Files written (${items.length})`}>
      {isLoading && <p className="text-sm text-muted-foreground">Loading...</p>}
      {!isLoading && error && (
        <p className="text-sm text-muted-foreground">
          What this script has written could not be read.
        </p>
      )}
      {!isLoading && !error && items.length === 0 && (
        <p className="text-sm text-muted-foreground">
          This script has not written an asset or a managed resource yet.
        </p>
      )}
      {items.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              {/* The file's name is the column that has to give: the three
                  beside it are a word, a number and a date, and letting the
                  table share width evenly squeezed every name down to one
                  letter. */}
              <TableHead className="w-1/2">File</TableHead>
              <TableHead className="whitespace-nowrap">Kind</TableHead>
              <TableHead className="whitespace-nowrap">Writes</TableHead>
              <TableHead className="whitespace-nowrap">Last written</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((item) => (
              <ProducedRow
                key={`${item.target_kind}:${item.target_id}`}
                item={item}
                href={hrefFor(item, filePath)}
                onNavigate={onNavigate}
              />
            ))}
          </TableBody>
        </Table>
      )}
    </SectionCard>
  );
}

// hrefFor is where a produced file opens, or undefined for one that no longer
// exists -- the row stays, because that this script wrote it is still true.
function hrefFor(
  item: ProducedItem,
  filePath?: (kind: ProducedItem["target_kind"], id: string) => string,
): string | undefined {
  if (item.deleted || !filePath) return undefined;
  return filePath(item.target_kind, item.target_id);
}

function ProducedRow({
  item,
  href,
  onNavigate,
}: {
  item: ProducedItem;
  href?: string;
  onNavigate?: (path: string) => void;
}) {
  const open = href && onNavigate ? () => onNavigate(href) : undefined;
  const Icon = item.target_kind === "asset" ? FileText : Paperclip;
  return (
    <TableRow className={open ? "cursor-pointer" : undefined} onClick={open}>
      <TableCell>
        <div className="flex min-w-0 items-center gap-2">
          <Icon aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="truncate">{displayName(item)}</span>
          <Badge
            variant={item.created ? "default" : "secondary"}
            className="shrink-0 text-[10px]"
          >
            {item.created ? "created" : "modified"}
          </Badge>
          {item.deleted && (
            <Badge variant="muted" className="shrink-0 text-[10px]">
              deleted
            </Badge>
          )}
        </div>
      </TableCell>
      <TableCell className="whitespace-nowrap text-muted-foreground">
        {item.target_kind === "asset" ? "Asset" : "Resource"}
      </TableCell>
      <TableCell className="text-muted-foreground">{item.write_count}</TableCell>
      <TableCell className="whitespace-nowrap text-muted-foreground">
        {formatWhen(item.last_write_at)}
      </TableCell>
    </TableRow>
  );
}

// displayName is what to call a produced file. A deleted one has no name left
// to read, so it is named by the id the record kept.
function displayName(item: ProducedItem): string {
  return item.name || item.target_id;
}
