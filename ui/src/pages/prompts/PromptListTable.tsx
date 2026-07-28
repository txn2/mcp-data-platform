import { ChevronDown, ChevronUp, ChevronsUpDown, FolderOpen, Users } from "lucide-react";
import type { Prompt, PromptCollection, PromptUsage } from "@/api/admin/types";
import { markdownToPlainText } from "@/lib/markdownText";
import { cn } from "@/lib/utils";
import { PromptStatusBadge } from "./PromptStatusBadge";
import { formatLastRun, isInactive, staleAfterDays } from "./promptUsage";
import type { Row, SortDir, SortKey } from "./promptList";

// PromptListTable renders one prompt table for the library page (#1010):
// name (+status/attribution/inactive marks), optional collection chip,
// description, and the usage columns. sortable=false (search mode) renders
// plain headers, since search results keep their server rank order.

interface Props {
  rows: Row[];
  showCollection: boolean;
  sortable: boolean;
  showStatus: boolean;
  sortBy: SortKey;
  sortDir: SortDir;
  onSort: (key: SortKey) => void;
  usageMap: Record<string, PromptUsage> | undefined;
  usageReady: boolean;
  collectionById: Map<string, PromptCollection>;
  onOpen: (p: Prompt) => void;
}

export function PromptListTable({
  rows,
  showCollection,
  sortable,
  showStatus,
  sortBy,
  sortDir,
  onSort,
  usageMap,
  usageReady,
  collectionById,
  onOpen,
}: Props) {
  const header = (label: string, key?: SortKey, extra?: string) =>
    sortable && key ? (
      <SortHeader label={label} sortKey={key} sortBy={sortBy} sortDir={sortDir} onSort={onSort} extra={extra} />
    ) : (
      <th className={cn("px-4 py-2 text-left font-medium text-muted-foreground", extra)}>{label}</th>
    );

  return (
    <div className="rounded-lg border bg-card overflow-hidden">
      <table className="w-full text-sm table-fixed">
        <colgroup>
          <col className="w-[32%]" />
          {showCollection && <col className="hidden md:table-column w-[140px]" />}
          <col />
          <col className="w-[70px]" />
          <col className="w-[100px]" />
        </colgroup>
        <thead className="border-b bg-muted/50">
          <tr>
            {header("Name", "name")}
            {showCollection && header("Collection", undefined, "hidden md:table-cell")}
            {header("Description")}
            {header("Runs", "runs", "text-right")}
            {header("Last run", "lastRun")}
          </tr>
        </thead>
        <tbody className="divide-y">
          {rows.map((r) => (
            <PromptRow
              key={`${r.prompt.id}-${r.sharedBy ?? "own"}`}
              row={r}
              showCollection={showCollection}
              showStatus={showStatus}
              usage={usageMap?.[r.prompt.id]}
              usageReady={usageReady}
              collection={r.prompt.collection_id ? collectionById.get(r.prompt.collection_id) : undefined}
              onOpen={onOpen}
            />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SortHeader({
  label,
  sortKey,
  sortBy,
  sortDir,
  onSort,
  extra,
}: {
  label: string;
  sortKey: SortKey;
  sortBy: SortKey;
  sortDir: SortDir;
  onSort: (key: SortKey) => void;
  extra?: string;
}) {
  const active = sortBy === sortKey;
  const Chevron = active ? (sortDir === "asc" ? ChevronUp : ChevronDown) : ChevronsUpDown;
  return (
    <th
      onClick={() => onSort(sortKey)}
      className={cn(
        "px-4 py-2 text-left font-medium text-muted-foreground cursor-pointer select-none hover:bg-muted/80",
        extra,
      )}
    >
      <span className="inline-flex items-center gap-1">
        {label}
        <Chevron className={cn("h-3 w-3", active ? "text-foreground" : "text-muted-foreground/50")} />
      </span>
    </th>
  );
}

function PromptRow({
  row,
  showCollection,
  showStatus,
  usage,
  usageReady,
  collection,
  onOpen,
}: {
  row: Row;
  showCollection: boolean;
  showStatus: boolean;
  usage: PromptUsage | undefined;
  usageReady: boolean;
  collection: PromptCollection | undefined;
  onOpen: (p: Prompt) => void;
}) {
  const p = row.prompt;
  const inactive = usageReady && isInactive(usage);
  return (
    <tr className="hover:bg-muted/30 cursor-pointer" onClick={() => onOpen(p)}>
      <td className="px-4 py-2 align-top">
        <NameBadges row={row} showStatus={showStatus} inactive={inactive} />
        {row.sharedBy && (
          <div className="mt-0.5 flex items-center gap-1 text-[11px] text-muted-foreground">
            <Users className="h-3 w-3" /> Shared by {row.sharedBy}
          </div>
        )}
      </td>
      {showCollection && (
        <td className="px-4 py-2 align-top hidden md:table-cell">
          {collection ? (
            <span className="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] text-muted-foreground">
              <FolderOpen className="h-3 w-3" /> {collection.name}
            </span>
          ) : (
            <span className="text-xs text-muted-foreground/60">–</span>
          )}
        </td>
      )}
      <td className="px-4 py-2 align-top text-muted-foreground">
        <div className="break-words whitespace-normal">{markdownToPlainText(p.description)}</div>
      </td>
      <td className="px-4 py-2 align-top text-right tabular-nums text-muted-foreground">
        {usageReady ? (usage?.run_count ?? 0) : "–"}
      </td>
      <td className="px-4 py-2 align-top text-xs text-muted-foreground whitespace-nowrap">
        {usageReady ? formatLastRun(usage) : "–"}
      </td>
    </tr>
  );
}

function NameBadges({ row, showStatus, inactive }: { row: Row; showStatus: boolean; inactive: boolean }) {
  const p = row.prompt;
  return (
    <div className="flex items-center gap-2 flex-wrap">
      <span className={cn("font-medium break-words", inactive && "text-muted-foreground")}>
        {p.display_name || p.name}
      </span>
      {showStatus && p.scope !== "system" && !row.sharedBy && <PromptStatusBadge status={p.status} />}
      {p.review_requested && (
        <span className="inline-flex items-center rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-400">
          promotion requested
        </span>
      )}
      {inactive && (
        <span
          className="inline-flex items-center rounded-full border border-zinc-500/30 bg-zinc-500/10 px-2 py-0.5 text-[11px] font-medium text-zinc-400"
          title={`Never run, or last run more than ${staleAfterDays} days ago`}
        >
          inactive
        </span>
      )}
    </div>
  );
}
