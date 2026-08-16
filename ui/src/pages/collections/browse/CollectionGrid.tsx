import { FolderOpen } from "lucide-react";
import type { ShareSummary } from "@/api/portal/types";
import { ThumbCard } from "@/components/cards/ThumbCard";
import { FeedbackCountBadge } from "@/components/feedback/FeedbackCountBadge";
import { ShareIndicators } from "@/components/ShareIndicators";
import { SharePermissionBadge } from "@/components/SharePermissionBadge";
import { Badge } from "@/components/ui/badge";
import { markdownToPlainText } from "@/lib/markdownText";
import { dateLabelFor, type DateColumn } from "@/components/listSort";
import type { DisplayCollection } from "./types";

/** The Collections list as a gallery of thumbnails. */
export function CollectionGrid({
  items,
  shareSummaries,
  threadCounts,
  dateKey,
  onNavigate,
}: {
  items: DisplayCollection[];
  shareSummaries?: Record<string, ShareSummary>;
  threadCounts?: Record<string, number>;
  /** The timestamp the list is ordered by, which is the one each card shows. */
  dateKey: DateColumn;
  onNavigate: (path: string) => void;
}) {
  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
      {items.map(({ collection, share }) => (
        <CollectionCard
          key={collection.id}
          collection={collection}
          share={share}
          summary={shareSummaries?.[collection.id]}
          threadCount={threadCounts?.[collection.id]}
          dateKey={dateKey}
          onNavigate={onNavigate}
        />
      ))}
    </div>
  );
}

function CollectionCard({
  collection: coll,
  share,
  summary,
  threadCount,
  dateKey,
  onNavigate,
}: {
  collection: DisplayCollection["collection"];
  share?: DisplayCollection["share"];
  summary?: ShareSummary;
  threadCount?: number;
  dateKey: DateColumn;
  onNavigate: (path: string) => void;
}) {
  const tags = coll.asset_tags ?? [];
  return (
    <ThumbCard
      onClick={() => onNavigate(`/collections/${coll.id}`)}
      thumbnailSrc={
        coll.thumbnail_s3_key ? `/api/v1/portal/collections/${coll.id}/thumbnail` : undefined
      }
      fallbackIcon={FolderOpen}
      overlay={
        // Share state belongs to the owner's own view; on a collection shared
        // *with* the reader the permission pill below says it instead.
        share ? undefined : (
          <ShareIndicators
            summary={summary}
            className="absolute top-2 right-2 rounded-full bg-background/80 px-1.5 py-0.5"
          />
        )
      }
    >
      <div className="mb-2 flex w-full items-center gap-2">
        <FolderOpen className="size-5 shrink-0 text-muted-foreground" />
        <span className="flex-1 truncate text-sm font-medium">{coll.name}</span>
        <FeedbackCountBadge count={threadCount} />
      </div>
      {coll.description && (
        <p className="mb-2 line-clamp-2 text-xs text-muted-foreground">
          {markdownToPlainText(coll.description)}
        </p>
      )}
      {tags.length > 0 && (
        <div className="mb-2 flex flex-wrap gap-1">
          {tags.slice(0, 4).map((t) => (
            <Badge key={t} variant="muted" className="px-1.5">
              {t}
            </Badge>
          ))}
          {tags.length > 4 && <span className="text-xs text-muted-foreground">+{tags.length - 4}</span>}
        </div>
      )}
      {share && (
        <div className="mb-2 flex items-center gap-1.5 text-xs text-muted-foreground">
          <span className="truncate">Shared by {share.shared_by}</span>
          <SharePermissionBadge permission={share.permission} />
        </div>
      )}
      <div className="flex w-full items-center justify-between text-xs text-muted-foreground">
        {/* The card's date carries no visible label, so the title says which
            timestamp it is — the same one the list is ordered by. */}
        <span title={dateLabelFor(dateKey, !!share)}>
          {new Date(share ? share.shared_at : coll[dateKey]).toLocaleDateString()}
        </span>
      </div>
    </ThumbCard>
  );
}
