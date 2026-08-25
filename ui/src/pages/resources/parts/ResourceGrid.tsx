import { ImageOff } from "lucide-react";
import { ThumbCard } from "@/components/cards/ThumbCard";
import { BASE_URL } from "@/api/resources/client";
import { formatBytes } from "@/lib/format";
import type { Resource } from "@/api/resources/types";
import { ScopeBadge } from "./badges";
import { exceedsTileLimit, neverRead } from "./groups";

/**
 * The address a tile draws its image from.
 *
 * `preview=1` is the portal saying why it is reading: the library is drawing
 * itself, not somebody using the file. The server audits the read either way
 * and records it as `portal_preview`, which is the one surface that does not
 * stamp the resource's last-read time — otherwise opening the library would
 * clear the never-read flag on every image in it (#1471).
 */
export function previewURL(id: string): string {
  return `${BASE_URL}/${id}/content?preview=1`;
}

/**
 * A section of images, as tiles rather than rows.
 *
 * The tile is the original object: a resource has no stored thumbnail, so
 * there is nothing smaller to point at. That is what the size cutoff is for —
 * past it the tile carries the name and the size and issues no request at all,
 * and the image is loaded when the resource itself is opened.
 *
 * Under the cutoff the tile is the shared thumbnail card, which marks its image
 * `loading="lazy"`. That defers a fetch the element issues for itself, which is
 * what a cookie session does; an API-key session resolves the source through
 * the store's credential on mount and does not defer.
 */
export function ResourceGrid({
  resources,
  admin,
  onOpen,
}: {
  resources: Resource[];
  // The administrator's library spans every scope and is read to find dead
  // weight, so a tile carries the two things the admin table's own columns
  // carry: which library the file is in, and whether anything has read it.
  // Without them an image section is the one place those answers go missing.
  admin: boolean;
  onOpen: (resource: Resource) => void;
}) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-6">
      {resources.map((r) => (
        <div key={r.id} data-testid={`resource-tile-${r.id}`}>
          <ThumbCard
            onClick={() => onOpen(r)}
            thumbnailSrc={exceedsTileLimit(r) ? undefined : previewURL(r.id)}
            fallbackIcon={ImageOff}
            aspect="aspect-square"
            bodyClassName="p-2"
          >
            <p className="truncate text-xs font-medium" title={r.display_name}>
              {r.display_name}
            </p>
            <p className="text-xs text-muted-foreground">
              {formatBytes(r.size_bytes)}
              {admin && neverRead(r) && (
                <span
                  className="ml-1 text-amber-600 dark:text-amber-400"
                  data-testid={`resource-tile-never-read-${r.id}`}
                >
                  Never read
                </span>
              )}
            </p>
            {admin && (
              <div className="mt-1 flex">
                <ScopeBadge scope={r.scope} scopeId={r.scope_id} />
              </div>
            )}
          </ThumbCard>
        </div>
      ))}
    </div>
  );
}
