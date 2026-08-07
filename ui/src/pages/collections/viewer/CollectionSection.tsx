import type { CollectionItem, CollectionSection as Section } from "@/api/portal/types";
import { ThumbCard } from "@/components/cards/ThumbCard";
import { contentTypeIcon, ContentTypeBadge } from "@/components/ContentTypeBadge";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { markdownToPlainText } from "@/lib/markdownText";
import { cn } from "@/lib/utils";
import { THUMB_SIZES, type ThumbSize } from "./thumbSize";

/** One curated section of a collection: its prose, then the assets it holds. */
export function CollectionSection({
  section,
  thumbSize,
  onOpenItem,
}: {
  section: Section;
  thumbSize: ThumbSize;
  onOpenItem: (assetId: string) => void;
}) {
  const cfg = THUMB_SIZES[thumbSize];
  return (
    <div className="space-y-3">
      {section.title && (
        <h2 className="border-b pb-2 text-lg font-semibold">{section.title}</h2>
      )}
      {section.description && (
        <div className="prose prose-sm max-w-none dark:prose-invert">
          <MarkdownRenderer content={section.description} bare />
        </div>
      )}
      <div className={cn("mt-4 grid gap-4", cfg.grid)}>
        {section.items.map((item) => (
          <ItemCard
            key={item.id}
            item={item}
            thumbSize={thumbSize}
            onOpen={() => onOpenItem(item.asset_id)}
          />
        ))}
      </div>
    </div>
  );
}

function ItemCard({
  item,
  thumbSize,
  onOpen,
}: {
  item: CollectionItem;
  thumbSize: ThumbSize;
  onOpen: () => void;
}) {
  const contentType = item.asset_content_type || "";
  const Icon = contentTypeIcon(contentType);
  const bare = thumbSize === "none";
  return (
    <ThumbCard
      onClick={onOpen}
      thumbnailSrc={
        item.asset_thumbnail_s3_key
          ? `/api/v1/portal/assets/${item.asset_id}/thumbnail`
          : undefined
      }
      fallbackIcon={Icon}
      aspect={THUMB_SIZES[thumbSize].aspect}
      // With no thumbnail the tile is a row, so the body stops being a block
      // under an image and becomes the row's one flexible column.
      className={cn(bare && "flex-row items-center gap-3 p-3")}
      bodyClassName={bare ? "min-w-0 flex-1 p-0" : "p-3"}
    >
      <div className="mb-1 flex items-center gap-2">
        <Icon className="size-4 shrink-0 text-muted-foreground" />
        <span className="flex-1 truncate text-sm font-medium">
          {item.asset_name || "Untitled Asset"}
        </span>
      </div>
      {item.asset_description && (
        <p className="mb-1.5 line-clamp-2 text-xs text-muted-foreground">
          {markdownToPlainText(item.asset_description)}
        </p>
      )}
      {item.asset_content_type && <ContentTypeBadge contentType={item.asset_content_type} />}
    </ThumbCard>
  );
}
