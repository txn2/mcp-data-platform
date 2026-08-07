import type { LucideIcon } from "lucide-react";
import { AuthImg } from "@/components/AuthImg";
import { cn } from "@/lib/utils";

/**
 * ThumbCard is one clickable tile in a gallery of saved things: a rendered
 * thumbnail (or the icon standing in for one that has not been rendered yet)
 * above whatever the list wants to say about the item.
 *
 * It is a button rather than a Card holding a link because the whole tile is
 * the target — the same shape `VocabCard` uses in the catalog.
 */
export function ThumbCard({
  onClick,
  thumbnailSrc,
  fallbackIcon: Icon,
  aspect = "aspect-[4/3]",
  overlay,
  bodyClassName,
  className,
  children,
}: {
  onClick: () => void;
  /** The thumbnail endpoint, or undefined while the item has no thumbnail. */
  thumbnailSrc?: string;
  fallbackIcon: LucideIcon;
  /** The thumbnail's shape, or null for a tile with no thumbnail region. */
  aspect?: string | null;
  /** Corner content floated over the thumbnail (share state, badges). */
  overlay?: React.ReactNode;
  bodyClassName?: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "relative flex w-full flex-col items-start overflow-hidden rounded-xl border bg-card text-left shadow-sm transition-colors hover:border-primary/50 hover:bg-muted/50",
        className,
      )}
    >
      {aspect !== null && (
        <div className={cn("w-full bg-muted", aspect)}>
          {thumbnailSrc ? (
            <AuthImg src={thumbnailSrc} alt="" className="h-full w-full object-cover object-top" />
          ) : (
            <div className="flex h-full w-full items-center justify-center">
              <Icon className="size-8 text-muted-foreground/30" />
            </div>
          )}
        </div>
      )}
      {overlay}
      <div className={cn("w-full p-4", bodyClassName)}>{children}</div>
    </button>
  );
}
