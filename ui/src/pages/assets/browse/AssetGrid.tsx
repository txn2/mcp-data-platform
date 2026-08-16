import { FolderOpen } from "lucide-react";
import type { ShareSummary } from "@/api/portal/types";
import { ThumbCard } from "@/components/cards/ThumbCard";
import { contentTypeIcon, ContentTypeBadge } from "@/components/ContentTypeBadge";
import { FeedbackCountBadge } from "@/components/feedback/FeedbackCountBadge";
import { ShareIndicators } from "@/components/ShareIndicators";
import { SharePermissionBadge } from "@/components/SharePermissionBadge";
import { Badge } from "@/components/ui/badge";
import { formatBytes } from "@/lib/format";
import { markdownToPlainText } from "@/lib/markdownText";
import { dateLabelFor, type DateColumn } from "@/components/listSort";
import type { DisplayAsset } from "./types";

/** The Assets list as a gallery of thumbnails. */
export function AssetGrid({
  items,
  shareSummaries,
  threadCounts,
  isDark,
  dateKey,
  onNavigate,
}: {
  items: DisplayAsset[];
  shareSummaries?: Record<string, ShareSummary>;
  threadCounts?: Record<string, number>;
  /** Assets that render a dark variant get it when the portal is dark. */
  isDark: boolean;
  /** The timestamp the list is ordered by, which is the one each card shows. */
  dateKey: DateColumn;
  onNavigate: (path: string) => void;
}) {
  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
      {items.map(({ asset, share }) => (
        <AssetCard
          key={asset.id}
          asset={asset}
          share={share}
          summary={shareSummaries?.[asset.id]}
          threadCount={threadCounts?.[asset.id]}
          isDark={isDark}
          dateKey={dateKey}
          onNavigate={onNavigate}
        />
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
}: {
  asset: DisplayAsset["asset"];
  share?: DisplayAsset["share"];
  summary?: ShareSummary;
  threadCount?: number;
  isDark: boolean;
  dateKey: DateColumn;
  onNavigate: (path: string) => void;
}) {
  const Icon = contentTypeIcon(asset.content_type);
  const collections = asset.collections ?? [];
  return (
    <ThumbCard
      onClick={() => onNavigate(`/assets/${asset.id}`)}
      thumbnailSrc={thumbnailSrc(asset, isDark)}
      fallbackIcon={Icon}
      overlay={
        // Share state belongs to the owner's own view of an asset; on one
        // shared *with* the reader the permission pill below says it instead.
        share ? undefined : (
          <ShareIndicators
            summary={summary}
            className="absolute top-2 right-2 rounded-full bg-background/80 px-1.5 py-0.5"
          />
        )
      }
    >
      <div className="mb-2 flex w-full items-center gap-2">
        <Icon className="size-5 shrink-0 text-muted-foreground" />
        <span className="flex-1 truncate text-sm font-medium">{asset.name}</span>
        <FeedbackCountBadge count={threadCount} />
      </div>
      {asset.description && (
        <p className="mb-2 line-clamp-2 text-xs text-muted-foreground">
          {markdownToPlainText(asset.description)}
        </p>
      )}
      <div className="mb-2 flex flex-wrap gap-1.5">
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
          <span className="truncate">Shared by {share.shared_by}</span>
          <SharePermissionBadge permission={share.permission} />
        </div>
      )}
      <div className="flex w-full items-center justify-between text-xs text-muted-foreground">
        <span>{formatBytes(asset.size_bytes)}</span>
        {/* The card's date carries no visible label, so the title says which
            timestamp it is — the same one the list is ordered by. */}
        <span title={dateLabelFor(dateKey, !!share)}>
          {new Date(share ? share.shared_at : asset[dateKey]).toLocaleDateString()}
        </span>
      </div>
    </ThumbCard>
  );
}

/**
 * The thumbnail endpoint for an asset, or undefined while none has been
 * rendered. An asset whose renderer produces a dark variant serves it when the
 * portal is dark, so a light-on-white chart is not shown on a dark page.
 */
function thumbnailSrc(asset: DisplayAsset["asset"], isDark: boolean): string | undefined {
  if (!asset.thumbnail_s3_key) return undefined;
  const variant = isDark && asset.thumbnail_dark_s3_key ? "?variant=dark" : "";
  return `/api/v1/portal/assets/${asset.id}/thumbnail${variant}`;
}
