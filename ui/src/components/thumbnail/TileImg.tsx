import { useState, type ComponentProps } from "react";
import { AuthImg } from "@/components/AuthImg";
import { cn } from "@/lib/utils";

/**
 * TileImg is a stored tile, shown on the image viewer's checkerboard (#1991).
 *
 * An SVG or an image is captured with its transparent areas left transparent,
 * so a white logo meant for a dark page is not a white rectangle. The
 * checkerboard is the element's own background, so it shows only through the
 * tile's transparent pixels: every other tile is opaque and covers it. It is
 * put on once the image has loaded and decoded: the load event comes before an
 * async decode is painted, and a board put on at load showed on every card of
 * a grid for a moment before its picture did.
 */
export function TileImg({ className, onLoad, src, ...props }: ComponentProps<typeof AuthImg>) {
  const [loaded, setLoaded] = useState<string | undefined>(undefined);
  return (
    <AuthImg
      {...props}
      src={src}
      className={cn(className, loaded !== undefined && loaded === src && "checkerboard")}
      onLoad={(e) => {
        const img = e.currentTarget;
        const show = () => setLoaded(src);
        if (typeof img.decode === "function") void img.decode().then(show, show);
        else show();
        onLoad?.(e);
      }}
    />
  );
}
