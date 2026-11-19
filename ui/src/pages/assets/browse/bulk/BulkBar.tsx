import { ChevronUp } from "lucide-react";
import type { BulkAction } from "@/api/portal/hooks/bulk";
import { MAX_BULK_ASSETS } from "@/api/portal/hooks/bulk";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

/** The dialogs a bulk action opens: one per action, status covering set and clear. */
export type BulkDialogKind = "add_tags" | "remove_tags" | "add_to_collection" | "status" | "share" | "delete";

const actions: { kind: BulkDialogKind; label: string; danger?: boolean }[] = [
  { kind: "add_tags", label: "Add tags" },
  { kind: "remove_tags", label: "Remove tags" },
  { kind: "add_to_collection", label: "Add to collection" },
  { kind: "status", label: "Set status" },
  { kind: "share", label: "Share" },
  { kind: "delete", label: "Delete", danger: true },
];

/** The actions of the given kinds, in the order named. */
function pick(...kinds: BulkDialogKind[]) {
  return actions.filter((a) => kinds.includes(a.kind));
}

const barButton =
  "h-11 rounded-md border border-slate-600 px-3 text-sm text-white hover:bg-white/10 sm:h-9";

/**
 * BulkBar is the toolbar a selection brings up (#2507): how many assets are
 * selected and the six actions, docked at the top of the list on a wide
 * screen and at the bottom on a phone, where it is Tags, Status and More.
 */
export function BulkBar({
  count,
  onOpen,
  onClear,
}: {
  count: number;
  onOpen: (kind: BulkDialogKind) => void;
  onClear: () => void;
}) {
  if (count === 0) return null;
  return (
    <>
      <div
        role="toolbar"
        aria-label="Bulk actions"
        className="sticky top-0 z-20 hidden flex-wrap items-center gap-2 rounded-lg bg-slate-900 px-3 py-2 text-white sm:flex"
      >
        <b className="mr-1.5 text-sm font-semibold">{count} selected</b>
        {actions.map((a) => (
          <button
            key={a.kind}
            type="button"
            className={a.danger ? `${barButton} border-red-400 text-red-200` : barButton}
            onClick={() => onOpen(a.kind)}
          >
            {a.label}
          </button>
        ))}
        <button
          type="button"
          className="ml-auto h-9 rounded-md px-3 text-sm text-slate-300 hover:bg-white/10"
          onClick={onClear}
        >
          Clear
        </button>
      </div>
      <PhoneBar count={count} onOpen={onOpen} onClear={onClear} />
    </>
  );
}

/** The phone form: a header line with the count and Clear, three buttons at the bottom. */
function PhoneBar({
  count,
  onOpen,
  onClear,
}: {
  count: number;
  onOpen: (kind: BulkDialogKind) => void;
  onClear: () => void;
}) {
  return (
    <div className="sm:hidden">
      <div className="flex items-center justify-between">
        <b className="text-base font-semibold">{count} selected</b>
        <button type="button" className="h-11 px-3 text-sm text-primary" onClick={onClear}>
          Clear
        </button>
      </div>
      <div
        role="toolbar"
        aria-label="Bulk actions"
        className="fixed inset-x-0 bottom-0 z-30 grid grid-cols-3 gap-2 border-t bg-slate-900 px-3 py-2.5"
      >
        <PhoneMenu label="Tags" items={pick("add_tags", "remove_tags")} onOpen={onOpen} />
        <button type="button" className={barButton} onClick={() => onOpen("status")}>
          Status
        </button>
        <PhoneMenu label="More" items={pick("add_to_collection", "share", "delete")} onOpen={onOpen} />
      </div>
    </div>
  );
}

function PhoneMenu({
  label,
  items,
  onOpen,
}: {
  label: string;
  items: { kind: BulkDialogKind; label: string }[];
  onOpen: (kind: BulkDialogKind) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button type="button" className={`${barButton} inline-flex items-center justify-center gap-1`}>
          {label}
          <ChevronUp className="size-3.5" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="center">
        {items.map((a) => (
          <DropdownMenuItem key={a.kind} className="min-h-11" onSelect={() => onOpen(a.kind)}>
            {a.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * SelectAllBanner offers to widen a selection of the whole page to every
 * asset the filters cover, up to the bulk limit.
 */
export function SelectAllBanner({
  pageAllSelected,
  pageCount,
  total,
  selectedCount,
  loading,
  onSelectAll,
}: {
  pageAllSelected: boolean;
  pageCount: number;
  total?: number;
  selectedCount: number;
  loading: boolean;
  onSelectAll: () => void;
}) {
  if (!pageAllSelected || total === undefined || total <= pageCount || selectedCount >= total) return null;
  const tooMany = total > MAX_BULK_ASSETS;
  return (
    <div
      role="status"
      className="flex flex-wrap items-center gap-2.5 rounded-lg border border-blue-200 bg-blue-50 px-3 py-2 text-sm dark:border-blue-900 dark:bg-blue-950/40"
    >
      <span>All {pageCount} on this page are selected.</span>
      <button
        type="button"
        disabled={tooMany || loading}
        className="h-11 rounded-md border border-blue-700 bg-background px-2.5 text-blue-800 disabled:opacity-50 sm:h-8 dark:text-blue-200"
        onClick={onSelectAll}
      >
        {loading ? "Selecting..." : `Select all ${total} matching`}
      </button>
      <span className="text-muted-foreground">
        Over {MAX_BULK_ASSETS} at once is refused; narrow the filters.
      </span>
    </div>
  );
}

/** The verb a result is headed with. */
export function actionVerb(action: BulkAction): string {
  switch (action) {
    case "add_tags":
      return "Tagged";
    case "remove_tags":
      return "Untagged";
    case "add_to_collection":
      return "Added to collection";
    case "set_status":
      return "Status set";
    case "clear_status":
      return "Status cleared";
    case "share":
      return "Shared";
    default:
      return "Deleted";
  }
}
