import { Clock } from "lucide-react";
import { useResources } from "@/api/resources/hooks";
import { ThumbCard } from "@/components/cards/ThumbCard";
import { contentTypeIcon } from "@/components/ContentTypeBadge";
import type { ViewMode } from "@/components/listView";
import { formatBytes } from "@/lib/format";
import { resourceThumbnailSrc } from "@/lib/thumbnailSupport";
import { useResolvedDark } from "@/stores/theme";
import type { Resource } from "@/api/resources/types";
import { scopeParams } from "./useResourceLibrary";

/** How many of the most recently changed files the strip carries. */
export const RECENT_COUNT = 10;

/**
 * The most recently changed files in the library on screen, above its folders.
 *
 * A library opens on a list of folder names, and the folder somebody wants is
 * usually the one somebody else has just changed — which the folder names do
 * not say. This is what says it.
 *
 * It is a library-root section, and the page leaves it out inside a folder and
 * while a search or a tag filter is running: those views are already an answer
 * to "what here is relevant", and a second, differently-ordered answer above
 * them competes with the one that was asked for.
 */
export function RecentResources({
  activeTab,
  viewMode,
  onOpen,
}: {
  /** The library in view, which is the one this is drawn from. */
  activeTab: string;
  /**
   * Tiles or rows, the reader's standing choice. The strip drew tiles whatever
   * the switch said, which is most of what is on a library's root -- so the
   * switch appeared to do nothing at all (#1553).
   */
  viewMode: ViewMode;
  onOpen: (resource: Resource) => void;
}) {
  // Its own request rather than a slice of the library listing: that listing is
  // ordered by whatever the reader chose and paged from the top of the folder
  // tree, so the ten most recently changed files in the library are not
  // generally in the page it has loaded.
  const { data, isLoading } = useResources({
    ...scopeParams(activeTab),
    sort: "updated",
    limit: RECENT_COUNT,
  });
  const recent = data?.resources ?? [];
  // The same capture the grid draws, in the scheme the reader is in (#1568).
  const isDark = useResolvedDark();

  // Nothing to say while it loads, and nothing to say about an empty library:
  // the folder list below already carries that library's own empty state.
  if (isLoading || recent.length === 0) return null;

  return (
    <section className="space-y-2" data-testid="recent-resources">
      <h2 className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        <Clock className="size-3.5" />
        Recently updated
      </h2>
      {viewMode === "grid" ? (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
          {recent.map((r) => (
            <ThumbCard
              key={r.id}
              onClick={() => onOpen(r)}
              thumbnailSrc={resourceThumbnailSrc(r, isDark)}
              fallbackIcon={contentTypeIcon(r.mime_type)}
              aspect="aspect-video"
              bodyClassName="p-2"
            >
              <p className="w-full truncate text-xs font-medium" title={r.display_name}>
                {r.display_name}
              </p>
              <p className="w-full truncate text-xs text-muted-foreground">
                {/* The folder, so a tile says where the file is and not only
                    that it changed — following it is the point of the strip. */}
                {r.path} &middot; {new Date(r.updated_at).toLocaleDateString()}
              </p>
            </ThumbCard>
          ))}
        </div>
      ) : (
        <ul className="divide-y overflow-hidden rounded-lg border bg-card">
          {recent.map((r) => (
            <RecentRow key={r.id} resource={r} onOpen={() => onOpen(r)} />
          ))}
        </ul>
      )}
    </section>
  );
}

/** One recently changed file as a row, for a reader who asked for rows. */
function RecentRow({ resource: r, onOpen }: { resource: Resource; onOpen: () => void }) {
  const Icon = contentTypeIcon(r.mime_type);
  return (
    <li>
      <div
        role="button"
        tabIndex={0}
        data-testid={`recent-row-${r.id}`}
        onClick={onOpen}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onOpen();
          }
        }}
        className="flex cursor-pointer items-center gap-2 px-4 py-2 text-sm hover:bg-muted/50"
      >
        <Icon className="size-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate font-medium">{r.display_name}</span>
        <span className="hidden min-w-0 flex-1 truncate text-xs text-muted-foreground sm:block">
          {r.path}
        </span>
        <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
          {formatBytes(r.size_bytes)}
        </span>
        <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
          {new Date(r.updated_at).toLocaleDateString()}
        </span>
      </div>
    </li>
  );
}
