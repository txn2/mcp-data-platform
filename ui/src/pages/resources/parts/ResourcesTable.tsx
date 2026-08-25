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
import { neverRead } from "./groups";

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
// There is no category column: the table is a category's own section and the
// section header names it (#1471), so a column here would repeat one word down
// every row of it.
function columns(admin: boolean): Column[] {
  if (!admin) {
    return [
      { label: "Name", width: "38%" },
      { label: "Type", width: "14%" },
      { label: "Tags", width: "14%" },
      { label: "Size", width: "7%", align: "right" },
      { label: "Uploader", width: "16%" },
      { label: "Updated", width: "8%" },
      { label: "", width: "3%" },
    ];
  }
  return [
    { label: "Name", width: "28%" },
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
  onOpen,
}: {
  resource: Resource;
  admin: boolean;
  onOpen: () => void;
}) {
  const lastRead = lastReadLabel(r);
  return (
    <TableRow onClick={onOpen} className="cursor-pointer">
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
      <TableCell className="max-w-0 px-4 py-2.5">
        <div className="flex flex-wrap gap-1">
          {(r.tags ?? []).slice(0, 3).map((t) => (
            <Badge key={t} variant="muted" className="max-w-[80px] truncate px-1.5">
              {t}
            </Badge>
          ))}
          {(r.tags ?? []).length > 3 && (
            <span className="text-xs text-muted-foreground">+{(r.tags ?? []).length - 3}</span>
          )}
        </div>
      </TableCell>
      <TableCell className="px-4 py-2.5 text-right text-muted-foreground">
        {formatBytes(r.size_bytes)}
      </TableCell>
      <TableCell className="max-w-0 truncate px-4 py-2.5 text-xs text-muted-foreground">
        {r.uploader_email || r.uploader_sub}
      </TableCell>
      <TableCell className="px-4 py-2.5 text-xs text-muted-foreground">
        {new Date(r.updated_at).toLocaleDateString()}
      </TableCell>
      {admin && (
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
      )}
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

// ResourcesTable is the library's list body: one row per resource, with the
// admin-only scope and last-read columns folded in for the admin view.
export function ResourcesTable({
  resources,
  admin,
  onOpen,
}: {
  resources: Resource[];
  admin: boolean;
  onOpen: (resource: Resource) => void;
}) {
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
            {columns(admin).map((c) => (
              <TableHead
                key={c.label || "actions"}
                className={cn("px-4 text-muted-foreground", c.align === "right" && "text-right")}
                style={{ width: c.width }}
              >
                {c.label}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {resources.map((r) => (
            <ResourceRow key={r.id} resource={r} admin={admin} onOpen={() => onOpen(r)} />
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
