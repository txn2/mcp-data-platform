import { FolderOpen, Users } from "lucide-react";
import type { Prompt, PromptCollection, PromptUsage } from "@/api/admin/types";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { markdownToPlainText } from "@/lib/markdownText";
import { cn } from "@/lib/utils";
import { SortableHead } from "./primitives";
import { PromptStatusBadge } from "./PromptStatusBadge";
import { formatLastRun, usageBadge, type UsageBadgeInfo } from "./promptUsage";
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
      <SortableHead
        label={label}
        sortKey={key}
        sortBy={sortBy}
        sortDir={sortDir}
        onSort={onSort}
        className={cn("px-4", extra)}
      />
    ) : (
      <TableHead className={cn("px-4 text-muted-foreground", extra)}>{label}</TableHead>
    );

  return (
    <div className="overflow-hidden rounded-lg border bg-card">
      <Table className="table-fixed">
        <colgroup>
          <col className="w-[32%]" />
          {showCollection && <col className="hidden md:table-column w-[140px]" />}
          <col />
          <col className="w-[70px]" />
          <col className="w-[100px]" />
        </colgroup>
        <TableHeader>
          <TableRow className="bg-muted/50 hover:bg-muted/50">
            {header("Name", "name")}
            {showCollection && header("Collection", undefined, "hidden md:table-cell")}
            {header("Description")}
            {header("Runs", "runs", "text-right")}
            {header("Last run", "lastRun")}
          </TableRow>
        </TableHeader>
        <TableBody>
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
        </TableBody>
      </Table>
    </div>
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
  const badge = usageReady ? usageBadge(usage, p.created_at) : null;
  return (
    <TableRow className="cursor-pointer" onClick={() => onOpen(p)}>
      <TableCell className="px-4 py-2 align-top whitespace-normal">
        <NameBadges row={row} showStatus={showStatus} badge={badge} />
        {row.sharedBy && (
          <div className="mt-0.5 flex items-center gap-1 text-[11px] text-muted-foreground">
            <Users className="size-3" /> Shared by {row.sharedBy}
          </div>
        )}
      </TableCell>
      {showCollection && (
        <TableCell className="hidden px-4 py-2 align-top md:table-cell">
          {collection ? (
            <Badge variant="outline" className="text-[11px] text-muted-foreground">
              <FolderOpen /> {collection.name}
            </Badge>
          ) : (
            <span className="text-xs text-muted-foreground/60">–</span>
          )}
        </TableCell>
      )}
      <TableCell className="px-4 py-2 align-top text-muted-foreground whitespace-normal">
        <div className="break-words">{markdownToPlainText(p.description)}</div>
      </TableCell>
      <TableCell className="px-4 py-2 text-right align-top tabular-nums text-muted-foreground">
        {usageReady ? (usage?.run_count ?? 0) : "–"}
      </TableCell>
      <TableCell className="px-4 py-2 align-top text-xs text-muted-foreground">
        {usageReady ? formatLastRun(usage) : "–"}
      </TableCell>
    </TableRow>
  );
}

// ScopeChip names a shared prompt's scope. My Prompts holds prompts of any
// scope the caller owns (#1124), so a shared prompt is labeled there; personal
// prompts carry no chip.
function ScopeChip({ prompt }: { prompt: Prompt }) {
  if (prompt.scope !== "global" && prompt.scope !== "persona") return null;
  const label =
    prompt.scope === "global" ? "global" : prompt.personas?.length ? prompt.personas.join(", ") : "persona";
  return (
    <Badge variant="info" className="text-[11px]">
      {label}
    </Badge>
  );
}

function NameBadges({ row, showStatus, badge }: { row: Row; showStatus: boolean; badge: UsageBadgeInfo | null }) {
  const p = row.prompt;
  const ownBadges = showStatus && !row.sharedBy;
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className={cn("font-medium break-words", badge && "text-muted-foreground")}>
        {p.display_name || p.name}
      </span>
      {ownBadges && <ScopeChip prompt={p} />}
      {ownBadges && p.scope !== "system" && <PromptStatusBadge status={p.status} />}
      {p.review_requested && (
        <Badge variant="warning" className="text-[11px]">
          promotion requested
        </Badge>
      )}
      {badge && (
        <Badge variant="muted" className="text-[11px]" title={badge.title}>
          {badge.label}
        </Badge>
      )}
    </div>
  );
}
