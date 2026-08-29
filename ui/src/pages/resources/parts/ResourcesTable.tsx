import { File, FileText } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatBytes } from "@/lib/format";
import { markdownToPlainText } from "@/lib/markdownText";
import { cn } from "@/lib/utils";
import type { Resource } from "@/api/resources/types";
import { ScopeBadge } from "./badges";
import { dragResources } from "./drag";
import { neverRead } from "./groups";
import type { Selection } from "./selection";

// lastReadLabel renders a resource's read recency for the admin table: a date
// when it has been read, and "Never" when it has not — flagged once the
// resource is old enough for that to mean something, which is the rule the
// image tile flags on too.
function lastReadLabel(r: Resource): { text: string; stale: boolean } {
  if (r.last_read_at) {
    return { text: new Date(r.last_read_at).toLocaleDateString(), stale: false };
  }
  return { text: "Never", stale: neverRead(r) };
}

interface Column {
  label: string;
  width: string;
  align?: "right";
}

// columns is the library table's shape. The admin view folds in two more
// columns — which library a resource is in, and whether anything reads it — so
// the two width sets are stated in full rather than patched at each cell. Each
// set adds up to 100%, which fixed layout requires.
//
// There is no folder column: the table is one folder's contents and the
// breadcrumb above it says which, so a column here would repeat one path down
// every row of it. A search spans the whole library and does need it, which is
// why the hit list is its own component rather than this table with a column
// switched on.
function columns(admin: boolean, selectable: boolean): Column[] {
  const select: Column[] = selectable ? [{ label: "", width: "4%" }] : [];
  if (!admin) {
    return [
      ...select,
      { label: "Name", width: selectable ? "34%" : "38%" },
      { label: "Type", width: "14%" },
      { label: "Tags", width: "14%" },
      { label: "Size", width: "7%", align: "right" },
      { label: "Uploader", width: "16%" },
      { label: "Updated", width: "8%" },
      { label: "", width: "3%" },
    ];
  }
  return [
    ...select,
    { label: "Name", width: selectable ? "24%" : "28%" },
    { label: "Scope", width: "12%" },
    { label: "Type", width: "12%" },
    { label: "Tags", width: "13%" },
    { label: "Size", width: "6%", align: "right" },
    { label: "Uploader", width: "12%" },
    { label: "Updated", width: "7%" },
    { label: "Last read", width: "7%" },
    { label: "", width: "3%" },
  ];
}

// ResourceRow renders one library entry. It is a component rather than an
// inline map body so the table function stays within the line budget.
function ResourceRow({
  resource: r,
  admin,
  selection,
  onOpen,
}: {
  resource: Resource;
  admin: boolean;
  // Absent on a table nothing can be done to in bulk, which is what leaves the
  // checkbox column off rather than showing one that does nothing.
  selection?: Selection;
  onOpen: () => void;
}) {
  const picked = selection?.has(r.id) ?? false;
  return (
    <TableRow
      onClick={onOpen}
      className={cn("cursor-pointer", picked && "bg-accent/50")}
      data-testid={`resource-row-${r.id}`}
      draggable={selection !== undefined}
      onDragStart={(e) => selection && dragResources(e.dataTransfer, r.id, selection.ids)}
    >
      {selection && <SelectCell resource={r} selection={selection} />}
      <TableCell className="max-w-0 px-4 py-2.5">
        <div className="flex items-center gap-2">
          <File className="h-4 w-4 shrink-0 text-muted-foreground" />
          <div className="min-w-0 flex-1">
            <span className="block truncate font-medium">{r.display_name}</span>
            <span className="block truncate text-xs text-muted-foreground">
              {markdownToPlainText(r.description)}
            </span>
          </div>
        </div>
      </TableCell>
      {admin && (
        <TableCell className="overflow-hidden px-4 py-2.5">
          <ScopeBadge scope={r.scope} scopeId={r.scope_id} />
        </TableCell>
      )}
      <TableCell className="max-w-0 truncate px-4 py-2.5 text-xs text-muted-foreground">
        {r.mime_type}
      </TableCell>
      <TagsCell tags={r.tags ?? []} />
      <TableCell className="px-4 py-2.5 text-right text-muted-foreground">
        {formatBytes(r.size_bytes)}
      </TableCell>
      <TableCell className="max-w-0 truncate px-4 py-2.5 text-xs text-muted-foreground">
        {r.uploader_email || r.uploader_sub}
      </TableCell>
      <TableCell className="px-4 py-2.5 text-xs text-muted-foreground">
        {new Date(r.updated_at).toLocaleDateString()}
      </TableCell>
      {admin && <LastReadCell resource={r} />}
      <TableCell className="px-2 py-2.5">
        <Button
          variant="ghost"
          size="icon-xs"
          onClick={(e) => {
            e.stopPropagation();
            onOpen();
          }}
          title="View details"
          aria-label={`View details for ${r.display_name}`}
        >
          <FileText />
        </Button>
      </TableCell>
    </TableRow>
  );
}

/** The pick box, in a cell that swallows the click so the row does not open. */
function SelectCell({ resource: r, selection }: { resource: Resource; selection: Selection }) {
  return (
    <TableCell className="px-4 py-2.5" onClick={(e) => e.stopPropagation()}>
      <input
        type="checkbox"
        checked={selection.has(r.id)}
        onChange={() => selection.toggle(r.id)}
        aria-label={`Select ${r.display_name}`}
      />
    </TableCell>
  );
}

/** The first three tags and a count of whatever is left. */
function TagsCell({ tags }: { tags: string[] }) {
  return (
    <TableCell className="max-w-0 px-4 py-2.5">
      <div className="flex flex-wrap gap-1">
        {tags.slice(0, 3).map((t) => (
          <Badge key={t} variant="muted" className="max-w-[80px] truncate px-1.5">
            {t}
          </Badge>
        ))}
        {tags.length > 3 && (
          <span className="text-xs text-muted-foreground">+{tags.length - 3}</span>
        )}
      </div>
    </TableCell>
  );
}

/** Read recency, flagged once a never-read file is old enough for it to mean
 * something. */
function LastReadCell({ resource: r }: { resource: Resource }) {
  const lastRead = lastReadLabel(r);
  return (
    <TableCell
      className={cn(
        "px-4 py-2.5 text-xs",
        lastRead.stale ? "text-amber-600 dark:text-amber-400" : "text-muted-foreground",
      )}
      data-testid={`resource-last-read-${r.id}`}
      title={lastRead.stale ? "No reads since it was uploaded" : undefined}
    >
      {lastRead.text}
    </TableCell>
  );
}

// ResourcesTable is the library's list body: one row per resource, with the
// admin-only scope and last-read columns folded in for the admin view, and a
// selection column wherever a selection can be acted on.
export function ResourcesTable({
  resources,
  admin,
  selection,
  onOpen,
}: {
  resources: Resource[];
  admin: boolean;
  selection?: Selection;
  onOpen: (resource: Resource) => void;
}) {
  const allPicked =
    selection !== undefined && resources.length > 0 && resources.every((r) => selection.has(r.id));
  return (
    <div className="overflow-hidden rounded-lg border bg-card">
      {/* ui/table puts `whitespace-nowrap` on every cell, so under the default
          auto layout the uploader and type columns claim their full intrinsic
          width and squeeze the name column down to a stub. Fixed layout makes
          the stated column widths the ones that apply, and the truncating cells
          then truncate instead of pushing — which is also why the widths below
          have to add up to 100: with fixed layout a set that overflows spills
          each column's content into the next. */}
      <Table className="table-fixed">
        <TableHeader>
          <TableRow className="bg-muted/50 hover:bg-muted/50">
            {columns(admin, selection !== undefined).map((c, i) => (
              <TableHead
                key={c.label || `col-${i}`}
                className={cn("px-4 text-muted-foreground", c.align === "right" && "text-right")}
                style={{ width: c.width }}
              >
                {selection && i === 0 ? (
                  <input
                    type="checkbox"
                    checked={allPicked}
                    onChange={() =>
                      allPicked
                        ? selection.clear()
                        : selection.add(resources.map((r) => r.id))
                    }
                    aria-label={allPicked ? "Clear selection" : "Select every file here"}
                  />
                ) : (
                  c.label
                )}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {resources.map((r) => (
            <ResourceRow
              key={r.id}
              resource={r}
              admin={admin}
              selection={selection}
              onOpen={() => onOpen(r)}
            />
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
