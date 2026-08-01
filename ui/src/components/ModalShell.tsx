import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/**
 * The geometry every centered modal in the portal shares.
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
 * and do not use this.
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
 * Exported for the Radix dialogs, whose Content element must carry these
 * classes itself; the plain overlays below use ModalShell instead. Both routes
 * resolve to the same geometry, so a modal cannot be half-fixed.
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
 * is always reachable. Used by ModalScroll and by the Radix dialogs, whose
 * Content element carries these classes directly.
 */
export function modalNaturalClass(width: string, extra?: string): string {
  return cn("my-auto w-full", width, extra);
}

/**
 * ModalScroll is the backdrop and centering behavior on their own, for a
 * modal whose child already carries its own panel chrome (border, background,
 * padding). The child grows to its natural height and the backdrop scrolls,
 * which needs no cooperation from the child at all -- the point being that
 * such a modal cannot be fixed by capping a panel it does not own.
 *
 * ModalShell below is the other shape: it owns the panel, so it can cap the
 * height and keep a header and footer in place while only the body scrolls.
 * Both are built from the same three constants, so neither can drift.
 */
export function ModalScroll({
  onClose,
  width = "max-w-lg",
  label,
  children,
}: {
  onClose: () => void;
  width?: string;
  label?: string;
  children: ReactNode;
}) {
  return (
    <div className={MODAL_OVERLAY} onClick={onClose}>
      <div className={MODAL_ROW}>
        <div
          role="dialog"
          aria-modal="true"
          aria-label={label}
          className={modalNaturalClass(width)}
          onClick={(e) => e.stopPropagation()}
        >
          {children}
        </div>
      </div>
    </div>
  );
}

interface Props {
  /** onClose fires on a backdrop click. */
  onClose: () => void;
  /** width is the Tailwind max-width class, e.g. "max-w-lg". */
  width?: string;
  /**
   * header and footer stay fixed while the body scrolls. Omit both and the
   * whole of children scrolls, which is right for a modal that is one block
   * of content.
   */
  header?: ReactNode;
  footer?: ReactNode;
  /** bodyClass adds padding or layout to the scrolling region. */
  bodyClass?: string;
  /** label names the dialog for assistive technology. */
  label?: string;
  children: ReactNode;
}

/**
 * ModalShell is the centered-modal container for the portal's hand-rolled
 * overlays -- the ones not built on Radix Dialog. It owns the backdrop, the
 * scroll behavior, and the height cap, so a modal only has to describe its
 * content.
 *
 * It is deliberately not a Radix wrapper: the Radix dialogs already bring
 * their own focus trap, Escape handling and ARIA wiring, and only need the
 * geometry, which they take from modalPanelClass above.
 */
export function ModalShell({
  onClose,
  width = "max-w-lg",
  header,
  footer,
  bodyClass,
  label,
  children,
}: Props) {
  return (
    <div className={MODAL_OVERLAY} onClick={onClose}>
      <div className={MODAL_ROW}>
        <div
          role="dialog"
          aria-modal="true"
          aria-label={label}
          className={modalPanelClass(width)}
          // The backdrop closes on click; the panel must not, or every click
          // inside the modal would dismiss it.
          onClick={(e) => e.stopPropagation()}
        >
          {header}
          <div className={cn("min-h-0 flex-1 overflow-y-auto", bodyClass)}>
            {children}
          </div>
          {footer}
        </div>
      </div>
    </div>
  );
}
