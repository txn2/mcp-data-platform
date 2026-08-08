import { cn } from "@/lib/utils";

/**
 * The geometry every centered modal in the portal shares, whether it is built
 * on Radix Dialog (`components/ui/dialog.tsx`) or is one of the hand-rolled
 * overlays (`components/ModalShell.tsx`). It lives here, below both, so a modal
 * cannot be half-fixed by taking one route or the other.
 *
 * A centered overlay whose panel has no height bound is unusable once its
 * content outgrows the viewport: the panel spills past both edges, and
 * `items-center` makes it worse than a plain overflow, because a scroll
 * container cannot scroll above its own start -- the top of the panel, title
 * bar included, becomes permanently unreachable.
 *
 * The fix is three cooperating parts, which is why they live here as one unit
 * rather than as classes each modal remembers to copy:
 *
 *   1. the overlay scrolls (`overflow-y-auto`), so nothing is ever clipped;
 *   2. the row is `items-start` with `my-auto` on the panel, which centers the
 *      panel while it fits and pins it to the top once it does not, keeping
 *      the top edge reachable;
 *   3. the panel is capped at the viewport and lays out as a column, so a
 *      header and footer stay put while the body scrolls.
 *
 * Side drawers (full-height panels pinned to an edge) are a different shape
 * and do not use this -- see `patterns/DrawerShell`.
 */
const MODAL_OVERLAY = "fixed inset-0 z-50 overflow-y-auto bg-black/50";
const MODAL_ROW = "flex min-h-full items-start justify-center p-4";
const MODAL_PANEL =
  "my-auto flex max-h-[calc(100vh-2rem)] w-full flex-col rounded-lg border bg-card shadow-lg";

/** modalOverlayClass is the scrolling backdrop. */
export const modalOverlayClass = MODAL_OVERLAY;

/** modalRowClass centers the panel horizontally and tops it out vertically. */
export const modalRowClass = MODAL_ROW;

/**
 * modalPanelClass is the height-capped column a modal's content sits in.
 * `width` is the Tailwind max-width for this modal (e.g. "max-w-lg").
 *
 * It carries no padding: a capped modal is a header, a scrolling body and an
 * optional footer, and each of those regions pads itself so only the body
 * moves under the header.
 */
export function modalPanelClass(width: string, extra?: string): string {
  return cn(MODAL_PANEL, width, extra);
}

/**
 * modalNaturalClass is the panel that keeps its natural height and lets the
 * backdrop scroll -- the shape for a modal that is one block of content with
 * no header or footer to hold in place.
 *
 * `my-auto` is what makes it safe: paired with the row's `items-start`, the
 * panel centers while it fits and tops out once it does not, so its first line
 * is always reachable. It carries no panel chrome either, so a caller that
 * brings its own box (a preview that is already a card) can use it as-is.
 */
export function modalNaturalClass(width: string, extra?: string): string {
  return cn("my-auto w-full", width, extra);
}
