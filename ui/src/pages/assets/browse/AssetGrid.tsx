import { rowDate } from "@/components/listView";
import { FolderOpen } from "lucide-react";
import type { ShareSummary } from "@/api/portal/types";
import { ThumbCard } from "@/components/cards/ThumbCard";
import { contentTypeIcon, ContentTypeBadge } from "@/components/ContentTypeBadge";
import { FeedbackCountBadge } from "@/components/feedback/FeedbackCountBadge";
import { ShareIndicators } from "@/components/ShareIndicators";
import { useIsMobile } from "@/hooks/useIsMobile";
import { SharePermissionBadge } from "@/components/SharePermissionBadge";
import { Badge } from "@/components/ui/badge";
import { formatBytes } from "@/lib/format";
import { markdownToPlainText } from "@/lib/markdownText";
import { assetThumbnailSrc } from "@/lib/thumbnailSupport";
import { dateLabelFor, type DateColumn } from "@/components/listSort";
import type { DisplayAsset } from "./types";
import { IncompleteBadge } from "@/components/IncompleteBadge";
import { AssetMarkBadges } from "@/components/assetmarks/AssetMarkBadges";
import { isTruncated } from "@/lib/truncation";
import { sharedLine } from "@/lib/sharedLine";
import { StarButton } from "@/components/library/StarButton";
import { SelectBox } from "./bulk/SelectBox";
import type { RowSelection } from "./bulk/AssetBulk";

/** The Assets list as a gallery of thumbnails. */
export function AssetGrid({
  items,
  shareSummaries,
  threadCounts,
  isDark,
  dateKey,
  onNavigate,
  selection,
}: {
  items: DisplayAsset[];
  shareSummaries?: Record<string, ShareSummary>;
  threadCounts?: Record<string, number>;
  /** Assets that render a dark variant get it when the portal is dark. */
  isDark: boolean;
  /** The timestamp the list is ordered by, which is the one each card shows. */
  dateKey: DateColumn;
  onNavigate: (path: string) => void;
  /** The bulk selection (#2507); omitted, the cards carry no box. */
  selection?: RowSelection;
}) {
  const compact = useIsMobile();
  return (
    // Two narrow cards to a row on a phone (#2723), one row of three on a wide screen.
    <div className="grid grid-cols-2 gap-3 md:gap-4 lg:grid-cols-3">
      {items.map(({ asset, share }) => (
        <div key={asset.id} className="relative">
          {compact ? (
            <CompactAssetCard asset={asset} share={share} isDark={isDark} dateKey={dateKey} onNavigate={onNavigate} selected={selection?.has(asset.id)} />
          ) : (
            <AssetCard
              asset={asset}
              share={share}
              summary={shareSummaries?.[asset.id]}
              threadCount={threadCounts?.[asset.id]}
              isDark={isDark}
              dateKey={dateKey}
              onNavigate={onNavigate}
              selected={selection?.has(asset.id)}
            />
          )}
          {selection && (
            <SelectBox
              label={`Select ${asset.name}`}
              checked={selection.has(asset.id)}
              onToggle={(shift) => selection.onToggle(asset.id, shift)}
              className="absolute top-2 left-2 size-11 rounded-md bg-background/90 shadow-sm sm:size-8"
            />
          )}
        </div>
      ))}
    </div>
  );
}

function AssetCard({
  asset,
  share,
  summary,
  threadCount,
  isDark,
  dateKey,
  onNavigate,
  selected,
}: {
  asset: DisplayAsset["asset"];
  share?: DisplayAsset["share"];
  summary?: ShareSummary;
  threadCount?: number;
  isDark: boolean;
  dateKey: DateColumn;
  onNavigate: (path: string) => void;
  selected?: boolean;
}) {
  const Icon = contentTypeIcon(asset.content_type);
  const collections = asset.collections ?? [];
  return (
    <ThumbCard
      onClick={() => onNavigate(`/assets/${asset.id}`)}
      thumbnailSrc={assetThumbnailSrc(asset, isDark)}
      fallbackIcon={Icon}
      className={selected ? "border-primary ring-2 ring-primary" : undefined}
      overlay={
        <>
          {/* Share state belongs to the owner's own view of an asset; on one
              shared *with* the reader the permission pill below says it instead. */}
          {share ? undefined : (
            <ShareIndicators
              summary={summary}
              className="absolute top-2 right-2 rounded-full bg-background/80 px-1.5 py-0.5"
            />
          )}
          <StarButton kind="asset" id={asset.id} variant="icon" className="absolute right-2 bottom-2" />
        </>
      }
    >
      <div className="mb-2 flex w-full items-center gap-2">
        <Icon className="size-5 shrink-0 text-muted-foreground" />
        <span className="flex-1 truncate text-sm font-medium">{asset.name}</span>
        {isTruncated(asset.tags) && <IncompleteBadge className="px-1.5" />}
        <FeedbackCountBadge count={threadCount} />
      </div>
      {asset.description && (
        <p className="mb-2 line-clamp-2 text-xs text-muted-foreground">
          {markdownToPlainText(asset.description)}
        </p>
      )}
      <div className="mb-2 flex flex-wrap gap-1.5">
        {/* Status and Verified (#2502, #2503). */}
        <AssetMarkBadges asset={asset} small />
        <ContentTypeBadge contentType={asset.content_type} />
        {(asset.tags ?? []).slice(0, 3).map((t) => (
          <Badge key={t} variant="muted" className="px-1.5">
            {t}
          </Badge>
        ))}
      </div>
      {collections.length > 0 && (
        <div className="mb-2 flex flex-wrap gap-1">
          {collections.slice(0, 2).map((c) => (
            <Badge key={c.id} variant="info" className="px-1.5">
              <FolderOpen />
              {c.name}
            </Badge>
          ))}
        </div>
      )}
      {share && (
        <div className="mb-2 flex items-center gap-1.5 text-xs text-muted-foreground">
          <span className="truncate">{sharedLine(share)}</span>
          <SharePermissionBadge permission={share.permission} />
        </div>
      )}
      <div className="flex w-full items-center justify-between text-xs text-muted-foreground">
        <span>{formatBytes(asset.size_bytes)}</span>
        {/* The card's date carries no visible label, so the title says which
            timestamp it is — the same one the list is ordered by. */}
        <span title={dateLabelFor(dateKey, !!share)}>
          {new Date(rowDate(share, asset[dateKey])).toLocaleDateString()}
        </span>
      </div>
    </ThumbCard>
  );
}

/** A phone's card (#2723): the tile, the name on two lines, its badges and its date. */
function CompactAssetCard({
  asset,
  share,
  isDark,
  dateKey,
  onNavigate,
  selected,
}: {
  asset: DisplayAsset["asset"];
  share?: DisplayAsset["share"];
  isDark: boolean;
  dateKey: DateColumn;
  onNavigate: (path: string) => void;
  selected?: boolean;
}) {
  return (
    <ThumbCard
      onClick={() => onNavigate(`/assets/${asset.id}`)}
      thumbnailSrc={assetThumbnailSrc(asset, isDark)}
      fallbackIcon={contentTypeIcon(asset.content_type)}
      className={selected ? "border-primary ring-2 ring-primary" : undefined}
      bodyClassName="flex flex-col gap-1.5 p-2.5"
    >
      <span className="line-clamp-2 text-sm leading-snug font-medium" data-testid="asset-card-name">
        {asset.name}
      </span>
      <span className="flex flex-wrap gap-1">
        <ContentTypeBadge contentType={asset.content_type} />
        <AssetMarkBadges asset={asset} small />
      </span>
      <span className="text-xs text-muted-foreground" title={dateLabelFor(dateKey, !!share)}>
        {new Date(rowDate(share, asset[dateKey])).toLocaleDateString()}
      </span>
    </ThumbCard>
  );
}
