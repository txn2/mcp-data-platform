import { useEffect, useState } from "react";
import type { LucideIcon } from "lucide-react";
import { AuthImg } from "@/components/AuthImg";
import { Card } from "@/components/ui/card";
import { cn } from "@/lib/utils";

/**
 * ThumbCard is one clickable tile in a gallery of saved things: a rendered
 * thumbnail (or the icon standing in for one that has not been rendered yet)
 * above whatever the list wants to say about the item.
 *
 * The whole tile is the target, so the card face rides a button through
 * `Card asChild` rather than wrapping one — the tile keeps the button role and
 * still cannot drift from the card face every other box on the page wears.
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
  const [broken, setBroken] = useState(false);
  // A new source is a new chance: a re-capture, or a card recycled onto another
  // item, must not inherit the last one's failure.
  useEffect(() => setBroken(false), [thumbnailSrc]);

  return (
    <Card
      asChild
      className={cn(
        "relative w-full items-start gap-0 overflow-hidden py-0 text-left transition-colors hover:border-primary/50 hover:bg-muted/50",
        className,
      )}
    >
      <button type="button" onClick={onClick}>
        {aspect !== null && (
          <div className={cn("w-full bg-muted", aspect)}>
            {thumbnailSrc && !broken ? (
              <AuthImg
                src={thumbnailSrc}
                alt=""
                className="h-full w-full object-cover object-top"
                // A recorded thumbnail whose object is gone, and an image the
                // browser cannot decode, both used to paint the broken-image
                // glyph: the fallback below was reached only when there was no
                // URL at all. A tile that cannot draw its image is a tile with
                // no image, which is what the icon is for (#1554).
                onError={() => setBroken(true)}
                // And on an API-key session, where the bytes are fetched ahead
                // of the element: a refused read produces no element to error,
                // so without this the tile stayed an empty box rather than
                // falling back to its icon (#1568).
                onLoadFailed={() => setBroken(true)}
              />
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
    </Card>
  );
}
