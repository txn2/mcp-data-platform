import { ThumbCard } from "@/components/cards/ThumbCard";
import { contentTypeIcon, ContentTypeBadge } from "@/components/ContentTypeBadge";
import { Badge } from "@/components/ui/badge";
import { BASE_URL } from "@/api/resources/client";
import { formatBytes } from "@/lib/format";
import { markdownToPlainText } from "@/lib/markdownText";
import { cn } from "@/lib/utils";
import type { Resource } from "@/api/resources/types";
import { ScopeBadge } from "./badges";
import { dragResources } from "./drag";
import { neverRead } from "./groups";
import type { Selection } from "./selection";

/**
 * The image a tile shows, or undefined for the icon that stands in for one.
 *
 * It is the resource's stored capture (#1554): a PNG a portal tab rendered and
 * uploaded, served from the resource's own thumbnail route. The URL carries the
 * moment the capture was taken, so a re-capture is a different URL and the old
 * one can be cached for the hour the route allows.
 *
 * The tile used to be the original object -- an image scaled down by CSS, and
 * nothing at all past a size cutoff -- so a library of documents was a wall of
 * identical icons and an image library pulled megabytes to draw postage stamps.
 * A resource with no capture yet, and one of a type nothing can rasterize,
 * returns undefined and draws its content-type icon.
 */
export function tileImage(r: Resource): string | undefined {
  if (!r.thumbnail_s3_key) return undefined;
  const taken = r.thumbnail_captured_at ?? "";
  return `${BASE_URL}/${r.id}/thumbnail?c=${encodeURIComponent(taken)}`;
}

/**
 * A library's files, as tiles rather than rows.
 *
 * Every file gets one. It used to be images only, chosen by the folder rather
 * than by the reader — a folder with one PDF among fifty photographs was fifty
 * rows of filename and size (#1553) — so the layout is now the reader's choice
 * and this draws whatever the folder holds.
 *
 * Under the cutoff an image tile is the shared thumbnail card, which marks its
 * image `loading="lazy"`. That defers a fetch the element issues for itself,
 * which is what a cookie session does; an API-key session resolves the source
 * through the store's credential on mount and does not defer.
 */
export function ResourceGrid({
  resources,
  admin,
  selection,
  onOpen,
}: {
  resources: Resource[];
  // The administrator's library spans every scope and is read to find dead
  // weight, so a tile carries the two things the admin table's own columns
  // carry: which library the file is in, and whether anything has read it.
  admin: boolean;
  // The same selection the row table carries, so picking files in one folder
  // and picking them in a folder of photographs are the same act.
  selection?: Selection;
  onOpen: (resource: Resource) => void;
}) {
  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
      {resources.map((r) => (
        <div
          key={r.id}
          data-testid={`resource-tile-${r.id}`}
          className={cn("relative rounded-lg", selection?.has(r.id) && "ring-2 ring-primary")}
          draggable={selection !== undefined}
          onDragStart={(e) => selection && dragResources(e.dataTransfer, r.id, selection.ids)}
        >
          {selection && (
            <input
              type="checkbox"
              checked={selection.has(r.id)}
              onChange={() => selection.toggle(r.id)}
              onClick={(e) => e.stopPropagation()}
              aria-label={`Select ${r.display_name}`}
              className="absolute top-2 left-2 z-10"
            />
          )}
          <ResourceCard resource={r} admin={admin} onOpen={() => onOpen(r)} />
        </div>
      ))}
    </div>
  );
}

/** One file's tile: what it looks like, what it is, and what it weighs. */
function ResourceCard({
  resource: r,
  admin,
  onOpen,
}: {
  resource: Resource;
  admin: boolean;
  onOpen: () => void;
}) {
  const Icon = contentTypeIcon(r.mime_type);
  const tags = r.tags ?? [];
  return (
    <ThumbCard onClick={onOpen} thumbnailSrc={tileImage(r)} fallbackIcon={Icon}>
      <div className="mb-2 flex w-full items-center gap-2">
        <Icon className="size-5 shrink-0 text-muted-foreground" />
        <span className="flex-1 truncate text-sm font-medium" title={r.display_name}>
          {r.display_name}
        </span>
      </div>
      {r.description && (
        <p className="mb-2 line-clamp-2 text-xs text-muted-foreground">
          {markdownToPlainText(r.description)}
        </p>
      )}
      <div className="mb-2 flex flex-wrap gap-1.5">
        <ContentTypeBadge contentType={r.mime_type} />
        {admin && <ScopeBadge scope={r.scope} scopeId={r.scope_id} />}
        {tags.slice(0, 3).map((t) => (
          <Badge key={t} variant="muted" className="px-1.5">
            {t}
          </Badge>
        ))}
      </div>
      <div className="flex w-full items-center justify-between text-xs text-muted-foreground">
        <span>{formatBytes(r.size_bytes)}</span>
        {/* An administrator's library is read to find dead weight, so the date
            a never-read file carries is the flag rather than the timestamp. */}
        {admin && neverRead(r) ? (
          <span
            className="text-amber-600 dark:text-amber-400"
            data-testid={`resource-tile-never-read-${r.id}`}
            title="No reads since it was uploaded"
          >
            Never read
          </span>
        ) : (
          <span title="Last updated">{new Date(r.updated_at).toLocaleDateString()}</span>
        )}
      </div>
    </ThumbCard>
  );
}
