import { Eye, MoreHorizontal } from "lucide-react";
import type { Asset, ShareSummary } from "@/api/portal/types";
import { ContentTypeBadge } from "@/components/ContentTypeBadge";
import { AssetMarkBadges } from "@/components/assetmarks/AssetMarkBadges";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { rowDate } from "@/components/listView";
import { dateColumnFor, type AssetSortKey, type ListSort } from "@/components/listSort";
import { formatBytes } from "@/lib/format";
import { sharedLine } from "@/lib/sharedLine";
import { useAuthStore } from "@/stores/auth";
import type { DisplayAsset } from "./types";
import { SelectBox } from "./bulk/SelectBox";
import type { RowSelection } from "./bulk/AssetBulk";

/**
 * AssetRows is the Assets list on a phone (#2723): one row per asset with its
 * name on up to two lines, its type, status and Verified badges, and one quiet
 * line of who, when and how big. Tags, collections and share detail are on the
 * asset's page. While any row is selected, tapping a row selects it instead of
 * opening it, since a phone has no shift-click.
 */
export function AssetRows({
  items,
  shareSummaries,
  sort,
  onNavigate,
  onPreview,
  selection,
}: {
  items: DisplayAsset[];
  shareSummaries?: Record<string, ShareSummary>;
  sort: ListSort<AssetSortKey>;
  onNavigate: (path: string) => void;
  onPreview: (asset: Asset) => void;
  selection?: RowSelection;
}) {
  const me = useAuthStore((s) => s.user?.email ?? "");
  const dateKey = dateColumnFor(sort.key);
  const selecting = !!selection && items.some(({ asset }) => selection.has(asset.id));
  return (
    <ul className="-mx-1 flex flex-col" data-testid="asset-rows">
      {items.map((item) => (
        <AssetRow
          key={item.asset.id}
          item={item}
          meta={rowMeta(item, me, dateKey, shareSummaries?.[item.asset.id])}
          selection={selection}
          selecting={selecting}
          onNavigate={onNavigate}
          onPreview={onPreview}
        />
      ))}
    </ul>
  );
}

/** A row's one quiet line: who, when, how big, and whether it is shared. */
function rowMeta(
  { asset, share }: DisplayAsset,
  me: string,
  dateKey: ReturnType<typeof dateColumnFor>,
  summary?: ShareSummary,
): string {
  const who = share ? sharedLine(share) : ownerOf(asset.owner_email, me);
  const shared = !share && !!(summary?.has_user_share || summary?.has_public_link);
  const when = `${dateKey === "created_at" ? "Created" : "Updated"} ${new Date(rowDate(share, asset[dateKey])).toLocaleDateString()}`;
  return [who, when, formatBytes(asset.size_bytes), shared ? "Shared" : ""].filter(Boolean).join(" · ");
}

/** "You" for the reader's own, the owner's address otherwise. */
function ownerOf(owner: string | undefined, me: string): string {
  return owner && owner !== me ? owner : "You";
}

function AssetRow({
  item: { asset },
  meta,
  selection,
  selecting,
  onNavigate,
  onPreview,
}: {
  item: DisplayAsset;
  meta: string;
  selection?: RowSelection;
  selecting: boolean;
  onNavigate: (path: string) => void;
  onPreview: (asset: Asset) => void;
}) {
  const on = !!selection?.has(asset.id);
  const tap = () => (selecting && selection ? selection.onToggle(asset.id, false) : onNavigate(`/assets/${asset.id}`));
  return (
    <li
      data-testid="asset-row"
      aria-selected={selection ? on : undefined}
      className={`flex items-start gap-1 border-b py-2 ${on ? "bg-primary/5" : ""}`}
    >
      {selection && (
        <SelectBox
          label={`Select ${asset.name}`}
          checked={on}
          onToggle={() => selection.onToggle(asset.id, false)}
          className="size-11 shrink-0"
        />
      )}
      <button type="button" className="flex min-w-0 flex-1 flex-col gap-1.5 pt-1 text-left" onClick={tap}>
        <span className="line-clamp-2 text-[15px] leading-snug font-medium" data-testid="asset-row-name">
          {asset.name}
        </span>
        <span className="flex flex-wrap items-center gap-1.5" data-testid="asset-row-type">
          <ContentTypeBadge contentType={asset.content_type} />
          {(asset.status || asset.verification) && <AssetMarkBadges asset={asset} small />}
        </span>
        <span className="truncate text-xs text-muted-foreground" data-testid="asset-row-meta">
          {meta}
        </span>
      </button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" className="size-11 shrink-0" aria-label={`More actions for ${asset.name}`}>
            <MoreHorizontal />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onSelect={() => onPreview(asset)}>
            <Eye />
            Quick preview
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </li>
  );
}
