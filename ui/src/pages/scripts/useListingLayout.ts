import { useCallback, useEffect, useState } from "react";
import { useIsMobile } from "@/hooks/useIsMobile";

// The Automations listing's layout follows the width the listing HAS, not the
// window's (#2601): the portal's sidebar takes 16rem beside it, so a 1280px
// window gives the table about 940px, and a 900px one about 590.
//
//   wide    every column, at the widths the column needs;
//   medium  Endpoint drops out and a bound endpoint shows as a chip under the
//           name;
//   narrow  Updated drops out too, and the fixed columns narrow;
//   list    a phone, or a width no table fits: one line of what each does.
//
// Every layout keeps a row at two lines with nothing clipped at the card's
// edge, and none of them scrolls sideways.

export type ListingLayout = "wide" | "medium" | "narrow" | "list";

/** The least width each table layout fits in; below the last, a list. */
export type LayoutWidths = { readonly wide: number; readonly medium: number; readonly narrow: number };

/** The Automations listing's widths; the Sites list has its own (#2612). */
export const LAYOUT_WIDTHS: LayoutWidths = { wide: 1100, medium: 760, narrow: 600 };

/** layoutFor is the layout a width fits; an unmeasured width is the widest. */
export function layoutFor(width: number | undefined, phone: boolean, widths: LayoutWidths = LAYOUT_WIDTHS): ListingLayout {
  if (phone) return "list";
  if (width === undefined || width === 0) return "wide";
  if (width >= widths.wide) return "wide";
  if (width >= widths.medium) return "medium";
  if (width >= widths.narrow) return "narrow";
  return "list";
}

/**
 * useListingLayout measures the element its ref is set on and says which
 * layout fits it, following the width as the window or the sidebar changes.
 * Where nothing can be measured (no ResizeObserver) it is the widest table.
 */
export function useListingLayout(widths: LayoutWidths = LAYOUT_WIDTHS): { ref: (el: HTMLElement | null) => void; layout: ListingLayout } {
  const phone = useIsMobile();
  const [el, setEl] = useState<HTMLElement | null>(null);
  const [width, setWidth] = useState<number | undefined>(undefined);
  const ref = useCallback((node: HTMLElement | null) => setEl(node), []);
  useEffect(() => {
    if (!el || typeof ResizeObserver === "undefined") return;
    setWidth(el.getBoundingClientRect().width);
    const observer = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (entry) setWidth(entry.contentRect.width);
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, [el]);
  return { ref, layout: layoutFor(width, phone, widths) };
}
