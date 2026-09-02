import { FileText, FolderOpen, Paperclip } from "lucide-react";
import { useScriptProduced, type ProducedItem } from "@/api/portal/hooks/producers";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
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
//
// A file whose row names somebody other than the script's owner is marked as
// theirs (#1588). That is the state a transfer leaves when the files are kept:
// the runs go on writing new versions into a file the script's owner cannot
// open, share or delete, and this is where that is visible.
export function ScriptProducedPanel({
  scriptId,
  owner,
  filePath,
  onNavigate,
}: {
  scriptId: string;
  /** The script's owner, which a file's own owner is compared to. Absent, no
   * file is marked as out of reach. */
  owner?: string;
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
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : error ? (
        <p className="text-sm text-muted-foreground">
          What this script has written could not be read.
        </p>
      ) : (
        <ProducedList items={items} owner={owner} filePath={filePath} onNavigate={onNavigate} />
      )}
    </SectionCard>
  );
}

// ProducedList is the panel once the list is in hand: the note about files the
// owner cannot reach, then the table, or the statement that there is nothing.
function ProducedList({
  items,
  owner,
  filePath,
  onNavigate,
}: {
  items: ProducedItem[];
  owner?: string;
  filePath?: (kind: ProducedItem["target_kind"], id: string) => string;
  onNavigate?: (path: string) => void;
}) {
  if (items.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        This script has not written an asset, a collection or a managed resource yet.
      </p>
    );
  }
  const elsewhere = items.filter((item) => ownedByAnother(item, owner)).length;
  return (
    <>
      {elsewhere > 0 && owner && <ElsewhereNote count={elsewhere} owner={owner} />}
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
              elsewhere={ownedByAnother(item, owner)}
              href={hrefFor(item, filePath)}
              onNavigate={onNavigate}
            />
          ))}
        </TableBody>
      </Table>
    </>
  );
}

// ElsewhereNote states what it means that some of these files belong to
// somebody other than the script's owner: the runs keep refreshing files the
// owner cannot open.
function ElsewhereNote({ count, owner }: { count: number; owner: string }) {
  const them = count === 1 ? "it" : "them";
  return (
    <Alert className="mb-3" data-testid="script-produced-elsewhere">
      <AlertDescription>
        {count === 1
          ? "One of these files belongs to somebody else. "
          : `${count} of these files belong to somebody else. `}
        {owner} cannot open, share or delete {them}, and each run goes on writing a new
        version into {them}.
      </AlertDescription>
    </Alert>
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

// ownedByAnother reports a live file whose row names somebody other than the
// script's owner. A resource records no address and is never marked; a file
// with no owner to compare against is not either.
export function ownedByAnother(item: ProducedItem, owner?: string): boolean {
  if (!owner || !item.owner_email || item.deleted) return false;
  return item.owner_email.toLowerCase() !== owner.toLowerCase();
}

// kindLabel is the column's word for what was written.
function kindLabel(kind: ProducedItem["target_kind"]): string {
  switch (kind) {
    case "asset":
      return "Asset";
    case "collection":
      return "Collection";
    default:
      return "Resource";
  }
}

function kindIcon(kind: ProducedItem["target_kind"]) {
  switch (kind) {
    case "asset":
      return FileText;
    case "collection":
      return FolderOpen;
    default:
      return Paperclip;
  }
}

function ProducedRow({
  item,
  elsewhere,
  href,
  onNavigate,
}: {
  item: ProducedItem;
  /** Whether the file's owner is somebody other than the script's owner. */
  elsewhere: boolean;
  href?: string;
  onNavigate?: (path: string) => void;
}) {
  const open = href && onNavigate ? () => onNavigate(href) : undefined;
  const Icon = kindIcon(item.target_kind);
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
          {elsewhere && (
            <Badge variant="outline" className="shrink-0 text-[10px]">
              owned by {item.owner_email}
            </Badge>
          )}
        </div>
      </TableCell>
      <TableCell className="whitespace-nowrap text-muted-foreground">
        {kindLabel(item.target_kind)}
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
